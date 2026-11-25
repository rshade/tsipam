// Command tsipam manages a declarative registry of Tailscale address
// space -- 4via6 site IDs, subnet CIDR pools, per-service IPv4 addresses, and
// per-device UDP listen ports -- and plans/checks that
// registry against live control-plane state.
//
// "site-id reserve/release/list", "subnet reserve/release/list",
// "service reserve/release/list", and "port reserve/release/list" edit and
// read the local registry file
// (comment-preserving HuJSON, see
// internal/registry); "devices list" reads live inventory through tscli.
// "plan" and "check" compare the registry with live allocation evidence.
// See ../../README.md.
//
// Output contract (AX conventions, see ../../docs/reference/json-envelope.md):
// command results are written to stdout, as human text on a terminal and as one
// JSON document otherwise; structured logs and the error object are written to
// stderr.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/rshade/ax-go"
	"github.com/spf13/cobra"
	goahttp "goa.design/goa/v3/http"

	"github.com/rshade/tsacl/pkg/policycore/sanitize"

	"github.com/rshade/tsipam/internal/diagnostics"
	"github.com/rshade/tsipam/internal/registry"
	"github.com/rshade/tsipam/internal/registry/hujsonstore"
)

const (
	toolName            = "tsipam"
	outputSchemaVersion = "2"
	toolSchemaVersion   = "1"
	formatHuman         = "human"
	formatJSON          = "json"
)

// Exit codes, matching the AX scheme documented in ../../docs/reference/exit-codes.md:
// 0 success, 1 a check/operation was refused (e.g. a site-ID/subnet-pool
// collision or a calc split that would produce too many subnets to materialize),
// 2 usage error or the tool/registry could not be read at all.
const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

type commandState struct {
	verbose            bool
	showSchema         bool
	sanitizeLevel      string
	sanitizeResolution sanitize.Resolution
	logger             diagnostics.Logger
	getenv             func(string) string
	sanitizer          *sanitize.Sanitizer
	// argv is the normalized command line, for failures raised before the
	// command that would register its own identities runs.
	argv []string
	// stdout is the run's own stdout, for the root command.
	stdout io.Writer
	// serving is true once this process serves MCP tool calls.
	serving bool
	// stdinIsTTY is true when a person can answer a prompt.
	stdinIsTTY bool
}

// reset drops what one command run leaves behind. An MCP server runs many on
// one tree, and no run may see another's redaction.
func (s *commandState) reset() {
	s.sanitizer = nil
	s.sanitizeResolution = sanitize.Resolution{}
	s.logger = nil
}

func (s *commandState) outputSanitizer(tailnet string) (*sanitize.Sanitizer, error) {
	if s.sanitizer != nil {
		return s.sanitizer, nil
	}
	sanitizer, err := sanitize.New(sanitize.Options{
		Level: s.sanitizeResolution.Level, Tool: toolName, Tailnet: tailnet,
		KeyHex: s.getenv(sanitize.KeyEnv),
	})
	if err != nil {
		return nil, err
	}
	// Pre-register the tailnet itself so an error's free-text sweep (see
	// sanitizeErrorIfNeeded) can already redact it even if the command fails
	// before registerOutputIdentities (output.go's success-path registration)
	// ever runs. Kind matches registerOutputIdentities's kindTailnet so both
	// paths resolve to the same placeholder for the same value.
	registerSweepValue(sanitizer, kindTailnet, tailnet)
	s.sanitizer = sanitizer
	return sanitizer, nil
}

// minSweepValueLength is the shortest value Step 1's identity inventory (see
// registerKnownIdentityInventory) registers for Sanitizer.Text's free-text
// error sweep. A tailnet name or path fragment shorter than this risks
// matching an unrelated short substring elsewhere in an error message; the
// exact-field substitution in output.go is a direct assignment, not a text
// search, so it is unaffected by this guard.
const minSweepValueLength = 3

// registerSweepValue registers value with sanitizer for Sanitizer.Text's
// free-text sweep, skipping anything shorter than minSweepValueLength. The
// svc: selector token is deliberately omitted only for a kindService
// registration, since there it is grammar (a Tailscale policy selector), not
// identity -- NormalizeSiteName legitimately accepts a site literally named
// "svc:", so the skip must not apply to kindSite or any other kind. It also
// registers value's %q-escape closure -- see registerQuotedForm's doc
// comment -- so an identity-echoing error built with %q is covered even when
// escaping means the raw value never appears as a literal substring of that
// error's text.
func registerSweepValue(sanitizer *sanitize.Sanitizer, kind, value string) {
	if len(value) < minSweepValueLength {
		return
	}
	if kind == kindService && value == svcSelectorToken {
		return
	}
	sanitizer.Value(kind, value)
	registerQuotedForm(sanitizer, kind, value)
}

// registerQuotedForm additionally registers, under kind, the literal bytes
// [strconv.Quote] renders inside value's surrounding quotes whenever quoting
// changes them -- for example a raw TAB byte becomes the two ASCII bytes '\'
// and 't'. This closes the identity-echoing %q closure that registering only
// the raw value can never reach: an error built with [fmt.Errorf]("... %q
// ...", value) never contains the raw value as a contiguous substring once
// escaping has rewritten even one byte (a control character, an embedded
// quote, a backslash, or invalid UTF-8), so Sanitizer.Text's literal
// token-delimited byte match against the raw registration can never find it.
// The surrounding quotes themselves are deliberately not part of the
// registered form: '"' is not a tokenByte
// (tsacl policycore/sanitize.tokenByte), so the escaped inner text alone is still
// correctly delimited by the %q quote marks that surround it in the
// rendered error text -- verified empirically against a real %q-formatted
// error (see the round-2 fix report's revert-proof evidence).
//
// Additionally registers the second-escaped form that appears in undeclared
// goa errors (which are returned as 500 faults with JSON-escaped bodies),
// since such errors apply a second layer of encoding/json escaping to the
// error message itself.
func registerQuotedForm(sanitizer *sanitize.Sanitizer, kind, value string) {
	quoted := strconv.Quote(value)
	if quoted == `"`+value+`"` {
		return
	}
	innerQuoted := quoted[1 : len(quoted)-1]
	sanitizer.Value(kind, innerQuoted)

	b, err := json.Marshal(innerQuoted)
	if err == nil {
		reescaped := string(b[1 : len(b)-1])
		if reescaped != innerQuoted {
			sanitizer.Value(kind, reescaped)
		}
	}
}

// registerAndValidate registers value for the output sweep and then reports
// whether it is valid caller input. Registration happens first so that a
// rejection's error text is already sweepable: the raw argv reaches the
// sanitizer before any error that might echo it is returned.
//
// The check runs client-side because the JSON request body is lossy in one
// direction -- encoding/json rewrites invalid UTF-8 to U+FFFD, which is
// itself valid -- so once the value crosses the wire the server can no longer
// tell a mangled label from a legitimate one.
func registerAndValidate(
	sanitizer *sanitize.Sanitizer, kind, value string, normalize func(string) (string, error),
) error {
	registerSweepValueWithCanonical(sanitizer, kind, value, normalize)
	_, err := normalize(value)
	return err
}

// registerSweepValueWithCanonical registers value under kind, and when
// normalize accepts it and changes its spelling, the canonical form too.
// Error frames built after normalization embed the canonical spelling, so
// registering only the value as written would leave that form unswept. Also
// registers the trimmed form when it differs from the raw value, since
// normalizers call [strings.TrimSpace] before validation and error messages
// echo the trimmed spelling -- each of raw/trimmed/canonical also gets its
// own %q-escape closure registered automatically, through registerSweepValue
// calling registerQuotedForm, so a trimmed value's control-character error is
// covered too.
func registerSweepValueWithCanonical(
	sanitizer *sanitize.Sanitizer, kind, value string, normalize func(string) (string, error),
) {
	registerSweepValue(sanitizer, kind, value)
	trimmed := strings.TrimSpace(value)
	if trimmed != value {
		registerSweepValue(sanitizer, kind, trimmed)
	}
	if canonical, err := normalize(value); err == nil && canonical != value {
		registerSweepValue(sanitizer, kind, canonical)
	}
}

// registerKnownIdentityInventory registers the identity values this CLI
// itself already knows from its own inputs -- the resolved tailnet, the
// registry directory, the full computed registry file path, and that path's
// bare filename -- so sanitizeErrorIfNeeded's free-text sweep can redact them
// from an error's text wherever they appear verbatim. This closes registry
// path leaks that originate deep in tsacl policycore/hujsonedit's own error
// strings (which this tool does not control the wording of) without any
// quote-parsing: Sanitizer.Text finds and replaces a known value byte-for-byte
// regardless of where in the error text it surfaces.
//
// executed is the *cobra.Command that failed. A command with no "registry-dir" flag (for example
// "devices list") has nothing to add here.
func registerKnownIdentityInventory(executed *cobra.Command, state *commandState) {
	if state.sanitizer == nil || executed == nil {
		return
	}
	// Aggregate exposes registry selectors in both modes, but positional
	// arithmetic must remain independent of registry data even on failure.
	if flag := executed.Flags().Lookup("from-registry"); flag != nil && flag.Value.String() != "true" {
		return
	}
	registryDirFlag := executed.Flags().Lookup("registry-dir")
	if registryDirFlag == nil {
		return
	}
	registryDir := registryDirFlag.Value.String()
	tailnetFlagValue := ""
	if flag := executed.Flags().Lookup("tailnet"); flag != nil {
		tailnetFlagValue = flag.Value.String()
	}
	tailnet := resolveTailnetWithEnv(tailnetFlagValue, state.getenv)

	registerSweepValue(state.sanitizer, kindTailnet, tailnet)
	registerSweepValue(state.sanitizer, kindPath, registryDir)
	filename := hujsonstore.FileName(tailnet)
	registryPath := hujsonstore.Path(registryDir, tailnet)
	registerSweepValue(state.sanitizer, kindPath, registryPath)
	registerSweepValue(state.sanitizer, kindPath, filename)

	siteIDs, subnetPools, serviceAddresses, owners := hujsonstore.RawEntries(registryPath)
	for _, site := range siteIDs {
		registerSweepValue(state.sanitizer, kindSite, site)
	}
	for cidr, site := range subnetPools {
		registerSweepValueWithCanonical(state.sanitizer, kindCIDR, cidr, registry.NormalizeSubnetCIDR)
		registerCanonicalCIDRSuggestion(state.sanitizer, cidr)
		registerSweepValue(state.sanitizer, kindSite, site)
	}
	for _, owner := range owners {
		registerSweepValue(state.sanitizer, kindOwner, owner)
	}
	for address, service := range serviceAddresses {
		// Service-address keys and identities each have two legal spellings
		// (bare vs /32 alias, bare name vs svc: selector), so both the raw and
		// canonical forms share one sweep domain.
		registerSweepValueWithCanonical(state.sanitizer, kindIP, address, registry.NormalizeServiceAddress)
		registerSweepValueWithCanonical(state.sanitizer, kindService, service, registry.NormalizeServiceName)
	}
	for _, entry := range hujsonstore.RawUDPPorts(registryPath) {
		registerSweepValueWithCanonical(state.sanitizer, kindSite, entry.Site, registry.NormalizeSiteName)
		registerSweepValueWithCanonical(state.sanitizer, kindHostname, entry.Device, registry.NormalizeDeviceName)
	}
}

type commandDependencies struct {
	getenv      func(string) string
	listDevices devicesListFunc
	collectLive liveSnapshotFunc
}

func main() { os.Exit(realMain()) }

func realMain() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	return runCLI(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr, nil)
}

// collisionErrorPattern is anchored to SiteIDCollisionError.Error()'s exact
// format string (registry.go: "site ID %s is already reserved for %q, not
// %q") so the two site names it embeds can be redacted by byte position
// rather than by searching the error text for their (possibly %q-escaped)
// bytes. A text search is unsafe here: [strconv.Unquote] recovers the true,
// unescaped value from a %q segment, but that unescaped value is not
// guaranteed to appear as a literal substring of the still-escaped error
// text whenever the value contains a quote, a backslash, a control
// character, or invalid UTF-8 -- exactly the values %q exists to escape.
// Capture groups: 1 = the site ID (never sanitized, see resolved decision
// D2 -- IDs are not identity), 2 = the %q-quoted existing site including its
// surrounding quotes, 3 = the %q-quoted wanted site, ditto. D2 governs only
// this collision splice and the output envelope's id field; the reserve
// path's own validation errors do sweep the raw ID argument, under its own
// kindSiteID (see siteid.go).
var collisionErrorPattern = regexp.MustCompile(
	`^site ID (\S+) is already reserved for ("(?:[^"\\]|\\.)*"), not ("(?:[^"\\]|\\.)*")$`,
)

// subnetPoolCollisionErrorPattern is the matching format-drift guard for
// SubnetPoolCollisionError.Error()'s overlapping-CIDR branch. Capture groups:
// 1 = requested CIDR, 2 = overlapping existing CIDR, 3 = existing site,
// 4 = wanted site.
var subnetPoolCollisionErrorPattern = regexp.MustCompile(
	`^subnet CIDR (\S+) overlaps (\S+) reserved for ("(?:[^"\\]|\\.)*"), not ("(?:[^"\\]|\\.)*")$`,
)

// subnetPoolExactCollisionErrorPattern guards SubnetPoolCollisionError.Error()'s
// exact-key branch. Capture groups: 1 = requested/existing CIDR, 2 = existing
// site, 3 = wanted site.
var subnetPoolExactCollisionErrorPattern = regexp.MustCompile(
	`^subnet CIDR (\S+) is already reserved for ("(?:[^"\\]|\\.)*"), not ("(?:[^"\\]|\\.)*")$`,
)

// serviceAddressCollisionErrorPattern guards
// ServiceAddressCollisionError.Error(). Capture groups: 1 = canonical bare
// IPv4 address, 2 = existing canonical service identity, 3 = wanted canonical
// service identity.
var serviceAddressCollisionErrorPattern = regexp.MustCompile(
	`^service address (\S+) is already reserved for ("(?:[^"\\]|\\.)*"), not ("(?:[^"\\]|\\.)*")$`,
)

// udpPortCollisionErrorPattern guards UDPPortCollisionError.Error(). Capture
// groups: 1 = canonical port (not identity and therefore left visible),
// 2 = site, 3 = existing device, 4 = wanted device.
var udpPortCollisionErrorPattern = regexp.MustCompile(
	`^UDP port (\S+) in site ("(?:[^"\\]|\\.)*") is already reserved for ("(?:[^"\\]|\\.)*"), not ("(?:[^"\\]|\\.)*")$`,
)

const (
	siteIDCollisionExistingSiteGroup  = 2
	siteIDCollisionWantedSiteGroup    = 3
	subnetExactCollisionCIDRGroup     = 1
	subnetExactCollisionExistingGroup = 2
	subnetExactCollisionWantedGroup   = 3
	subnetCollisionRequestedCIDRGroup = 1
	subnetCollisionExistingCIDRGroup  = 2
	subnetCollisionExistingSiteGroup  = 3
	subnetCollisionWantedSiteGroup    = 4
	serviceCollisionAddressGroup      = 1
	serviceCollisionExistingGroup     = 2
	serviceCollisionWantedGroup       = 3
	udpPortCollisionSiteGroup         = 2
	udpPortCollisionExistingGroup     = 3
	udpPortCollisionWantedGroup       = 4
)

// unprintableIdentityPlaceholder replaces a matched quoted segment that
// [strconv.Unquote] cannot decode: failing closed with a fixed, non-identity
// placeholder rather than falling through to the raw (still possibly
// identity-bearing) escaped bytes.
const unprintableIdentityPlaceholder = `"<unprintable>"`

// collisionSegment names one submatch group within a collision error's
// regex match to redact by byte-offset splice: group is the regex capture
// group number (1-based, matching FindStringSubmatchIndex's numbering --
// group N's byte range is match[2N:2N+2]), and quoted reports whether the
// segment is a [strconv.Unquote]-able %q-quoted Go string literal (site or
// service identity) or a bare token printed with %s (CIDR or address). kind
// is the sanitizer.Value kind to register it under.
type collisionSegment struct {
	group  int
	kind   string
	quoted bool
}

const (
	kindSite     = "site"
	kindSiteID   = "site-id"
	kindCIDR     = "cidr"
	kindIP       = "ip"
	kindService  = "service"
	kindTailnet  = "tailnet"
	kindPath     = "path"
	kindOwner    = "owner"
	kindHostname = "hostname"
	kindLogin    = "login"
	kindTag      = "tag"
	kindPort     = "port"
	kindAddress  = "address"

	svcSelectorToken = "svc:"
)

//nolint:gochecknoglobals // collision segment specs are data-driven and factored out for sharing with spliceCollisionSegments
var siteIDCollisionSegments = []collisionSegment{
	{group: siteIDCollisionExistingSiteGroup, kind: kindSite, quoted: true},
	{group: siteIDCollisionWantedSiteGroup, kind: kindSite, quoted: true},
}

//nolint:gochecknoglobals // collision segment specs are data-driven and factored out for sharing with spliceCollisionSegments
var subnetCollisionSegments = []collisionSegment{
	{group: subnetCollisionRequestedCIDRGroup, kind: kindCIDR, quoted: false},
	{group: subnetCollisionExistingCIDRGroup, kind: kindCIDR, quoted: false},
	{group: subnetCollisionExistingSiteGroup, kind: kindSite, quoted: true},
	{group: subnetCollisionWantedSiteGroup, kind: kindSite, quoted: true},
}

//nolint:gochecknoglobals // collision segment specs are data-driven and factored out for sharing with spliceCollisionSegments
var subnetExactCollisionSegments = []collisionSegment{
	{group: subnetExactCollisionCIDRGroup, kind: kindCIDR, quoted: false},
	{group: subnetExactCollisionExistingGroup, kind: kindSite, quoted: true},
	{group: subnetExactCollisionWantedGroup, kind: kindSite, quoted: true},
}

//nolint:gochecknoglobals // collision segment specs are data-driven and factored out for sharing with spliceCollisionSegments
var serviceAddressCollisionSegments = []collisionSegment{
	{group: serviceCollisionAddressGroup, kind: kindIP, quoted: false},
	{group: serviceCollisionExistingGroup, kind: kindService, quoted: true},
	{group: serviceCollisionWantedGroup, kind: kindService, quoted: true},
}

//nolint:gochecknoglobals // collision segment specs are data-driven and shared with spliceCollisionSegments
var udpPortCollisionSegments = []collisionSegment{
	{group: udpPortCollisionSiteGroup, kind: kindSite, quoted: true},
	{group: udpPortCollisionExistingGroup, kind: kindHostname, quoted: true},
	{group: udpPortCollisionWantedGroup, kind: kindHostname, quoted: true},
}

// sanitizeErrorIfNeeded returns a sanitized rendering of err's text when
// sanitization is active, or err unchanged otherwise.
//
// A genuine site-ID, subnet-pool, service-address, or UDP-port collision is
// redacted by splicing its known byte ranges out of the *original* text and
// back in as their
// sanitized renderings, using the segments' regex-matched offsets -- never a
// value-based search-and-replace, so escaping cannot desync the replacement
// from what's actually on the page. Every other byte of the collision
// message frame is copied through unchanged, so template words and message
// structure survive intact even when a site name or CIDR happens to collide
// with them.
//
// Any other error text is swept only against the identity inventory this
// tool already knows about from its own inputs (see
// registerKnownIdentityInventory and output.go's registerOutputIdentities) --
// it is not scanned for arbitrary quoted segments. A goa error-wrapper's JSON
// body, a cobra usage error, or a registry decode error naming one of its own
// field/key spellings is deliberately left otherwise intact: those quoted
// segments are not known identity, and rewriting them indiscriminately
// (Round 1/2's approach) corrupts unrelated, non-identity error text instead
// of protecting anything.
func sanitizeErrorIfNeeded(err error, sanitizer *sanitize.Sanitizer) error {
	if sanitizer == nil || sanitizer.Level() == sanitize.Off {
		return err
	}
	text := err.Error()
	if match := collisionErrorPattern.FindStringSubmatchIndex(text); match != nil {
		return errors.New(spliceCollisionSegments(text, match, siteIDCollisionSegments, sanitizer))
	}
	if match := subnetPoolExactCollisionErrorPattern.FindStringSubmatchIndex(text); match != nil {
		return errors.New(spliceCollisionSegments(text, match, subnetExactCollisionSegments, sanitizer))
	}
	if match := subnetPoolCollisionErrorPattern.FindStringSubmatchIndex(text); match != nil {
		return errors.New(spliceCollisionSegments(text, match, subnetCollisionSegments, sanitizer))
	}
	if match := serviceAddressCollisionErrorPattern.FindStringSubmatchIndex(text); match != nil {
		return errors.New(spliceCollisionSegments(text, match, serviceAddressCollisionSegments, sanitizer))
	}
	if match := udpPortCollisionErrorPattern.FindStringSubmatchIndex(text); match != nil {
		return errors.New(spliceCollisionSegments(text, match, udpPortCollisionSegments, sanitizer))
	}
	return errors.New(sanitizer.Text(text))
}

// spliceCollisionSegments rebuilds text with each segment's byte range
// replaced by its sanitized rendering. Every other byte of text -- including
// the message's own template words -- is copied through unchanged. This
// never falls back to a value-based sweep over the whole string: each
// identity value is registered and substituted only at its own known byte
// offset, so escaping cannot desync the replacement, and a site name or CIDR
// that happens to collide with a template word elsewhere in the message is
// never touched.
//
// segments is sorted by ascending text position before walking it: both
// current callers already pass it in that order (group number happens to
// equal text order for every collision shape so far), but sorting here
// removes that as a caller precondition rather than relying on every future
// segments literal to get it right by hand.
func spliceCollisionSegments(
	text string,
	match []int,
	segments []collisionSegment,
	sanitizer *sanitize.Sanitizer,
) string {
	sorted := make([]collisionSegment, len(segments))
	copy(sorted, segments)
	sort.Slice(sorted, func(i, j int) bool { return match[sorted[i].group*2] < match[sorted[j].group*2] })

	var out strings.Builder
	out.Grow(len(text))
	cursor := 0
	for _, seg := range sorted {
		start, end := match[seg.group*2], match[seg.group*2+1]
		out.WriteString(text[cursor:start])
		if seg.quoted {
			out.WriteString(sanitizedQuotedSegment(text[start:end], seg.kind, sanitizer))
		} else {
			out.WriteString(sanitizer.Value(seg.kind, text[start:end]))
		}
		cursor = end
	}
	out.WriteString(text[cursor:])
	return out.String()
}

// sanitizedQuotedSegment unquotes a %q-quoted segment (surrounding quotes
// included), registers the recovered value with sanitizer under kind, and
// returns the placeholder re-quoted so the surrounding message grammar is
// preserved. A segment [strconv.Unquote] cannot decode fails closed.
func sanitizedQuotedSegment(quoted, kind string, sanitizer *sanitize.Sanitizer) string {
	value, err := strconv.Unquote(quoted)
	if err != nil {
		return unprintableIdentityPlaceholder
	}
	return `"` + sanitizer.Value(kind, value) + `"`
}

// transportFaultError is a *goahttp.ClientError whose rendered text is the
// service's own message rather than the generated client's envelope. The
// original error stays in the chain for [errors.As] callers.
type transportFaultError struct {
	message string
	cause   error
}

func (e *transportFaultError) Error() string { return e.message }
func (e *transportFaultError) Unwrap() error { return e.cause }

// describeTransportFault unwraps the envelope the generated HTTP client builds
// around an error the design does not declare: "[ipam list_subnet_pools]:
// invalid response code 500, body: {"name":"fault",...,"message":"..."}".
// The loopback transport is in-process, so for an operator that envelope is
// noise around the one useful field. Declared errors (collision,
// invalid_input, comparison_failed, ...) decode to *goa.ServiceError and are
// untouched; so is any client error whose body is not a goa fault.
func describeTransportFault(err error) error {
	var clientErr *goahttp.ClientError
	if !errors.As(err, &clientErr) {
		return err
	}
	_, body, found := strings.Cut(clientErr.Message, ", body: ")
	if !found {
		return err
	}
	var fault struct {
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(body), &fault) != nil || fault.Message == "" {
		return err
	}
	return &transportFaultError{message: fault.Message, cause: err}
}

func newRootCommandWithDependencies(state *commandState, deps commandDependencies) *cobra.Command {
	if deps.getenv == nil {
		deps.getenv = func(string) string { return "" }
	}
	state.sanitizeLevel = string(sanitize.Off)
	state.getenv = deps.getenv
	root := &cobra.Command{
		Use:     toolName,
		Short:   "Allocate and plan Tailscale address space.",
		Version: buildVersion(),
		Long: "Allocate and plan Tailscale address space.\n\n" +
			"Results go to stdout: human text on a terminal, one JSON document otherwise.\n" +
			"--format or AGENT_MODE selects the format. A failure is one JSON error object\n" +
			"on stderr, with operational diagnostics. Registry commands use a\n" +
			"comment-preserving HuJSON file selected by --registry-dir and --tailnet.\n\n" +
			"Exit codes:\n" +
			"  0  success\n" +
			"  1  a valid operation was refused (for example, a registry collision or\n" +
			"     a calc split producing too many subnets to materialize), or check/check-policy found drift\n" +
			"  2  invalid invocation, incomplete comparison, unreadable input, or output failure",
		Example: "  tsipam site-id reserve 1 hq-vpc\n" +
			"  tsipam subnet reserve 10.0.0.0/24 hq-vpc\n" +
			"  tsipam service reserve 100.100.100.8 dashboard\n" +
			"  tsipam port reserve 41642 branch-fw01 --site branch-office\n" +
			"  tsipam site-id list -format json\n" +
			"  tsipam devices list -verbose\n" +
			"  tsipam -schema",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if state.showSchema {
				return printSchema(cmd.OutOrStdout(), cmd.Root())
			}
			return invalidInput(errors.New("a command is required; run \"tsipam --help\" for usage"))
		},
	}
	root.SetVersionTemplate("{{.Version}}\n")

	flags := root.PersistentFlags()
	flags.String("format", "",
		"output format: human|json (default: AGENT_MODE, else human on a terminal and json otherwise)")
	mustDeclare(ax.DeclareFlagEnum(root, "format", formatHuman, formatJSON))
	flags.StringVar(&state.sanitizeLevel, "sanitize", string(sanitize.Off),
		"output sanitization level: off|identity|strict (a non-off TAILSCALE_SANITIZE value overrides this flag)")
	flags.BoolVar(&state.verbose, "verbose", false, "log operational details to stderr")
	// Registered explicitly so the usage string is stable in -schema rather
	// than Cobra's generated "version for tsipam" wording.
	root.Flags().Bool("version", false, "print version and exit")
	root.Flags().BoolVar(&state.showSchema, "schema", false,
		"print the machine-readable tool schema (JSON) and exit")

	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		state.reset()
		state.restoreStdout(cmd)
		state.logger = newLogger(cmd, state.verbose)
		if err := refuseEmptyFormat(cmd); err != nil {
			return err
		}
		resolution, err := sanitize.Resolve(
			deps.getenv(sanitize.LevelEnv), state.sanitizeLevel,
			root.PersistentFlags().Changed("sanitize"),
		)
		if err != nil {
			return invalidInput(err)
		}
		// -schema reports static tool-catalog data unaffected by -format or
		// -sanitize; validate them above so a typo isn't silently accepted,
		// but skip the sanitizer state/notice below since -schema never
		// produces sanitized output.
		if state.showSchema {
			return nil
		}
		state.sanitizeResolution = resolution
		if notice := resolution.Notice(toolName); notice != "" {
			if _, err = fmt.Fprintln(cmd.ErrOrStderr(), notice); err != nil {
				return err
			}
		} else if resolution.Level != sanitize.Off {
			// Flag-driven sanitization (env var not set or off)
			if _, err = fmt.Fprintf(cmd.ErrOrStderr(),
				"%s: sanitize %s is active; output is sanitized\n", toolName, resolution.Level); err != nil {
				return err
			}
		}
		return validateRequiredFlags(cmd)
	}

	root.AddCommand(newDevicesCommand(state, deps.listDevices))
	root.AddCommand(newSiteIDCommand(state))
	root.AddCommand(newSubnetCommand(state, deps.listDevices))
	root.AddCommand(newServiceCommand(state))
	root.AddCommand(newUDPPortCommand(state))
	root.AddCommand(newCalcCommand(state, deps.listDevices))
	root.AddCommand(newPlanningCommand(state, commandPlan, deps.collectLive))
	root.AddCommand(newPlanningCommand(state, commandCheck, deps.collectLive))
	root.AddCommand(newPolicyCheckCommand(state))
	root.InitDefaultHelpFlag()
	return root
}

// refuseEmptyFormat refuses --format "", which the declared values accept
// because it is the flag's default. A script that passes an unset variable meant
// to choose a format.
func refuseEmptyFormat(cmd *cobra.Command) error {
	flag := cmd.Flags().Lookup("format")
	if flag != nil && flag.Changed && flag.Value.String() == "" {
		return invalidInput(errors.New("--format must be human or json"))
	}
	return nil
}

// validateRequiredFlags repeats Cobra's own flag checks, which run after every
// hook, so their failures reach the error boundary. Cobra's then pass.
func validateRequiredFlags(cmd *cobra.Command) error {
	if err := cmd.ValidateRequiredFlags(); err != nil {
		return invalidInput(err)
	}
	return invalidInput(cmd.ValidateFlagGroups())
}
