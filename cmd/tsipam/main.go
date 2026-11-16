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
// Output contract (AX conventions, see ../../docs/reference/json-envelope.md): command results
// are written to stdout as human text by default or a versioned envelope with
// -format json; structured logs and final errors are written to stderr.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	goahttp "goa.design/goa/v3/http"
	goa "goa.design/goa/v3/pkg"

	"github.com/rshade/tsacl/pkg/ipcalc"
	"github.com/rshade/tsacl/pkg/policycore/cliflags"
	"github.com/rshade/tsacl/pkg/policycore/sanitize"

	"github.com/rshade/tsipam/internal/diagnostics"
	"github.com/rshade/tsipam/internal/registry"
)

// devVersion is the release string of a build that injects no version.
const devVersion = "dev"

// version is overridden at build time via -ldflags "-X main.version=X.Y.Z";
// goreleaser injects the tag without its leading v.
var version = devVersion

const (
	toolName            = "tsipam"
	outputSchemaVersion = "1"
	toolSchemaVersion   = "1"
	formatHuman         = "human"
	formatJSON          = "json"
	shortRevisionLength = 12
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
	format             string
	verbose            bool
	showSchema         bool
	sanitizeLevel      string
	sanitizeResolution sanitize.Resolution
	logger             diagnostics.Logger
	getenv             func(string) string
	sanitizer          *sanitize.Sanitizer
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
// executed is the *cobra.Command runCLI actually ran, as returned by
// ExecuteContextC. A command with no "registry-dir" flag (for example
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
	filename := tailnet + ".ipam.hujson"
	registryPath := filepath.Join(registryDir, filename)
	registerSweepValue(state.sanitizer, kindPath, registryPath)
	registerSweepValue(state.sanitizer, kindPath, filename)

	siteIDs, subnetPools, serviceAddresses, owners := registry.RawEntries(registryPath)
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
	for _, entry := range registry.RawUDPPorts(registryPath) {
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

func runCLI(
	ctx context.Context,
	args []string,
	getenv func(string) string,
	stdout, stderr io.Writer,
	listDevices devicesListFunc,
) int {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	if listDevices == nil {
		listDevices = func(ctx context.Context, logger diagnostics.Logger) ([]deviceRecord, error) {
			return collectDevices(ctx, getenv, logger)
		}
	}
	state := &commandState{}
	root := newRootCommandWithDependencies(state, commandDependencies{getenv: getenv, listDevices: listDevices})
	root.SetArgs(cliflags.Normalize(root, args))
	root.SetOut(stdout)
	root.SetErr(stderr)
	executed, err := root.ExecuteContextC(ctx)
	if err != nil {
		// SilenceErrors on every command hands printing to the caller so
		// cobra never writes to stdout on failure; this is that print.
		// The drift sentinel contains no identities and the report is already
		// rendered. Avoid reopening registry data for this completed comparison.
		if !errors.Is(err, errRegistryDrift) && !errors.Is(err, errPolicyDrift) {
			registerKnownIdentityInventory(executed, state)
		}
		err = describeTransportFault(err)
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", toolName, sanitizeErrorIfNeeded(err, state.sanitizer))
		return exitCodeFor(err)
	}
	return exitOK
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

// collisionErrorName must match design.Error("collision", ...) in
// ../../design/design.go: it is how a *goa.ServiceError decoded off the wire
// is told apart from every other error the generated HTTP client can return.
const collisionErrorName = "collision"

// exitCodeFor maps a command error to this tool's exit-code scheme (see
// newRootCommand's Long text): a site-ID collision and a calc-split refusal
// are both decidable refusals of a well-formed request, not usage problems,
// so they get their own code rather than being indistinguishable from a bad
// flag or an unreadable registry file.
//
// Registry commands run through the loopback goa HTTP client (see
// internal/loopback), so a collision normally arrives as a *goa.ServiceError
// decoded from the 409 response, not the original registry collision type --
// that concrete Go type does not cross the wire. Both domain checks stay for
// in-process callers.
func exitCodeFor(err error) int {
	if errors.Is(err, errRegistryDrift) || errors.Is(err, errPolicyDrift) {
		return exitFailure
	}
	var svcErr *goa.ServiceError
	if errors.As(err, &svcErr) && svcErr.Name == collisionErrorName {
		return exitFailure
	}
	if _, ok := errors.AsType[*registry.SiteIDCollisionError](err); ok {
		return exitFailure
	}
	if _, ok := errors.AsType[*registry.SubnetPoolCollisionError](err); ok {
		return exitFailure
	}
	if _, ok := errors.AsType[*registry.ServiceAddressCollisionError](err); ok {
		return exitFailure
	}
	if _, ok := errors.AsType[*registry.UDPPortCollisionError](err); ok {
		return exitFailure
	}
	if errors.Is(err, ipcalc.ErrTooManyChildren) {
		return exitFailure
	}
	return exitUsage
}

func newRootCommandWithDependencies(state *commandState, deps commandDependencies) *cobra.Command {
	if deps.getenv == nil {
		deps.getenv = func(string) string { return "" }
	}
	state.format = formatHuman
	state.sanitizeLevel = string(sanitize.Off)
	state.getenv = deps.getenv
	root := &cobra.Command{
		Use:     toolName,
		Short:   "Allocate and plan Tailscale address space.",
		Version: buildVersion(),
		Long: "Allocate and plan Tailscale address space.\n\n" +
			"Results go to stdout as human text by default or a versioned JSON envelope.\n" +
			"Structured operational diagnostics go to stderr. Registry commands use a\n" +
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
			return errors.New("a command is required; run \"tsipam --help\" for usage")
		},
	}
	root.SetVersionTemplate("{{.Version}}\n")

	flags := root.PersistentFlags()
	flags.StringVar(&state.format, "format", formatHuman, "output format: human|json")
	flags.StringVar(&state.sanitizeLevel, "sanitize", string(sanitize.Off),
		"output sanitization level: off|identity|strict (a non-off TAILSCALE_SANITIZE value overrides this flag)")
	flags.BoolVar(&state.verbose, "verbose", false, "log operational details to stderr")
	// Registered explicitly so the usage string is stable in -schema rather
	// than Cobra's generated "version for tsipam" wording.
	root.Flags().Bool("version", false, "print version and exit")
	root.Flags().BoolVar(&state.showSchema, "schema", false,
		"print the machine-readable tool schema (JSON) and exit")

	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		state.logger = newLogger(cmd.ErrOrStderr(), state.verbose)
		if state.format != formatHuman && state.format != formatJSON {
			return fmt.Errorf("unknown -format %q; want human or json", state.format)
		}
		resolution, err := sanitize.Resolve(
			deps.getenv(sanitize.LevelEnv), state.sanitizeLevel,
			root.PersistentFlags().Changed("sanitize"),
		)
		if err != nil {
			return err
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
		return nil
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

func newLogger(writer io.Writer, verbose bool) *slog.Logger {
	level := slog.LevelWarn
	if verbose {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(writer, &slog.HandlerOptions{Level: level}))
}

// buildVersion combines the release string (injected by ldflags, or the module
// version for `go install module@version`) with the embedded VCS revision so
// --version reports exact provenance for an auditable binary.
func buildVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		return buildVersionWithSettings(moduleVersion(version, info.Main.Version), info.Settings)
	}
	return buildVersionWithSettings(version, nil)
}

// moduleVersion prefers the module version Go recorded for `go install
// module@version` over the dev placeholder, without its leading v so both
// install paths print the same release string. A working-tree build records
// "(devel)" and keeps dev. An injected release string always wins.
func moduleVersion(injected, module string) string {
	if injected != devVersion || module == "" || module == "(devel)" {
		return injected
	}
	return strings.TrimPrefix(module, "v")
}

func buildVersionWithSettings(value string, settings []debug.BuildSetting) string {
	var revision, dirty string
	for _, setting := range settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			if setting.Value == "true" {
				dirty = "+dirty"
			}
		}
	}
	if len(revision) > shortRevisionLength {
		revision = revision[:shortRevisionLength]
	}
	if revision != "" {
		value = fmt.Sprintf("%s (%s%s)", value, revision, dirty)
	}
	return toolName + " " + value
}
