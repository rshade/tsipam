// Command tail-ipam manages a declarative registry of Tailscale address
// space -- 4via6 site IDs and subnet CIDR pools so far, with per-service /32
// allocations as separately scoped future work -- and will plan/check that
// registry against live control-plane state.
//
// "site-id reserve/release/list" and "subnet reserve/release/list" edit and
// read the local registry file (comment-preserving HuJSON, see
// internal/registry); "devices list" is a read-only proof that the
// tscli-backed client construction works. Neither cross-checks the registry
// against live state yet -- that's the "plan"/"check" surface, still future
// work. See ../../README.md and
// ../../../ROADMAP.md ("Future Vision" -> tail-ipam) for the design this will
// grow into.
//
// Output contract (AX conventions, see ../../../AGENTS.md): command results
// are written to stdout as human text by default or a versioned envelope with
// -format json; structured logs and final errors are written to stderr.
package main

import (
	"context"
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
	goa "goa.design/goa/v3/pkg"

	"github.com/rshade/tailscale-utils/policycore/cliflags"
	"github.com/rshade/tailscale-utils/policycore/ipcalc"
	"github.com/rshade/tailscale-utils/policycore/sanitize"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/diagnostics"
	"github.com/rshade/tailscale-utils/tail-ipam/internal/registry"
)

// version is overridden at build time via -ldflags "-X main.version=vX.Y.Z".
var version = "dev"

const (
	toolName            = "tail-ipam"
	outputSchemaVersion = "1"
	toolSchemaVersion   = "1"
	formatHuman         = "human"
	formatJSON          = "json"
	shortRevisionLength = 12
)

// Exit codes, matching the AX scheme documented in ../../../AGENTS.md:
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
	// ever runs. Kind matches registerOutputIdentities's "tailnet" so both
	// paths resolve to the same placeholder for the same value.
	registerSweepValue(sanitizer, "tailnet", tailnet)
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
// free-text sweep, skipping anything shorter than minSweepValueLength.
func registerSweepValue(sanitizer *sanitize.Sanitizer, kind, value string) {
	if len(value) < minSweepValueLength {
		return
	}
	sanitizer.Value(kind, value)
}

// registerKnownIdentityInventory registers the identity values this CLI
// itself already knows from its own inputs -- the resolved tailnet, the
// registry directory, the full computed registry file path, and that path's
// bare filename -- so sanitizeErrorIfNeeded's free-text sweep can redact them
// from an error's text wherever they appear verbatim. This closes registry
// path leaks that originate deep in policycore/hujsonedit's own error
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

	registerSweepValue(state.sanitizer, "tailnet", tailnet)
	registerSweepValue(state.sanitizer, "path", registryDir)
	filename := tailnet + ".ipam.hujson"
	registryPath := filepath.Join(registryDir, filename)
	registerSweepValue(state.sanitizer, "path", registryPath)
	registerSweepValue(state.sanitizer, "path", filename)

	siteIDs, subnetPools := registry.RawEntries(registryPath)
	for _, site := range siteIDs {
		registerSweepValue(state.sanitizer, "site", site)
	}
	for cidr, site := range subnetPools {
		registerSweepValue(state.sanitizer, "cidr", cidr)
		registerSweepValue(state.sanitizer, "site", site)
	}
}

type commandDependencies struct {
	getenv      func(string) string
	listDevices devicesListFunc
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
		registerKnownIdentityInventory(executed, state)
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
// surrounding quotes, 3 = the %q-quoted wanted site, ditto.
var collisionErrorPattern = regexp.MustCompile(
	`^site ID (\S+) is already reserved for ("(?:[^"\\]|\\.)*"), not ("(?:[^"\\]|\\.)*")$`,
)

// subnetPoolCollisionErrorPattern is the matching format-drift guard for
// SubnetPoolCollisionError.Error(). Capture groups: 1 = requested CIDR,
// 2 = overlapping existing CIDR, 3 = existing site, 4 = wanted site.
var subnetPoolCollisionErrorPattern = regexp.MustCompile(
	`^subnet CIDR (\S+) overlaps (\S+) reserved for ("(?:[^"\\]|\\.)*"), not ("(?:[^"\\]|\\.)*")$`,
)

const (
	siteIDCollisionExistingSiteGroup  = 2
	siteIDCollisionWantedSiteGroup    = 3
	subnetCollisionRequestedCIDRGroup = 1
	subnetCollisionExistingCIDRGroup  = 2
	subnetCollisionExistingSiteGroup  = 3
	subnetCollisionWantedSiteGroup    = 4
)

// unprintableSitePlaceholder replaces a matched quoted segment that
// [strconv.Unquote] cannot decode: failing closed with a fixed, non-identity
// placeholder rather than falling through to the raw (still possibly
// identity-bearing) escaped bytes.
const unprintableSitePlaceholder = `"<unprintable>"`

// collisionSegment names one submatch group within a collision error's
// regex match to redact by byte-offset splice: group is the regex capture
// group number (1-based, matching FindStringSubmatchIndex's numbering --
// group N's byte range is match[2N:2N+2]), and quoted reports whether the
// segment is a [strconv.Unquote]-able %q-quoted Go string literal (site
// names) or a bare token printed with %s (CIDRs). kind is the sanitizer.Value
// kind to register it under -- consulted only when quoted is false, since a
// quoted segment is always a site name and sanitizedQuotedSegment hardcodes
// that kind itself.
type collisionSegment struct {
	group  int
	kind   string
	quoted bool
}

const (
	kindSite = "site"
	kindCIDR = "cidr"
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

// sanitizeErrorIfNeeded returns a sanitized rendering of err's text when
// sanitization is active, or err unchanged otherwise.
//
// A genuine site-ID or subnet-pool collision is redacted by splicing its
// known byte ranges out of the *original* text and back in as their
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
	if match := subnetPoolCollisionErrorPattern.FindStringSubmatchIndex(text); match != nil {
		return errors.New(spliceCollisionSegments(text, match, subnetCollisionSegments, sanitizer))
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
			out.WriteString(sanitizedQuotedSegment(text[start:end], sanitizer))
		} else {
			out.WriteString(sanitizer.Value(seg.kind, text[start:end]))
		}
		cursor = end
	}
	out.WriteString(text[cursor:])
	return out.String()
}

// sanitizedQuotedSegment unquotes a %q-quoted segment (surrounding quotes
// included), registers the recovered value with sanitizer under the "site"
// kind, and returns the placeholder re-quoted so the surrounding message
// grammar is preserved (resolved decision D1). A segment [strconv.Unquote]
// cannot decode fails closed to unprintableSitePlaceholder.
func sanitizedQuotedSegment(quoted string, sanitizer *sanitize.Sanitizer) string {
	value, err := strconv.Unquote(quoted)
	if err != nil {
		return unprintableSitePlaceholder
	}
	return `"` + sanitizer.Value("site", value) + `"`
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
	var svcErr *goa.ServiceError
	if errors.As(err, &svcErr) && svcErr.Name == collisionErrorName {
		return exitFailure
	}
	var collision *registry.SiteIDCollisionError
	if errors.As(err, &collision) {
		return exitFailure
	}
	var subnetCollision *registry.SubnetPoolCollisionError
	if errors.As(err, &subnetCollision) {
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
			"     a calc split producing too many subnets to materialize)\n" +
			"  2  invalid invocation, collection failure, or unreadable registry",
		Example: "  tail-ipam site-id reserve 1 hq-vpc\n" +
			"  tail-ipam subnet reserve 10.0.0.0/24 hq-vpc\n" +
			"  tail-ipam site-id list -format json\n" +
			"  tail-ipam devices list -verbose\n" +
			"  tail-ipam -schema",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if state.showSchema {
				return printSchema(cmd.OutOrStdout(), cmd.Root())
			}
			return errors.New("a command is required; run \"tail-ipam --help\" for usage")
		},
	}
	root.SetVersionTemplate("{{.Version}}\n")

	flags := root.PersistentFlags()
	flags.StringVar(&state.format, "format", formatHuman, "output format: human|json")
	flags.StringVar(&state.sanitizeLevel, "sanitize", string(sanitize.Off),
		"output sanitization level: off|identity|strict (a non-off TAILSCALE_SANITIZE value overrides this flag)")
	flags.BoolVar(&state.verbose, "verbose", false, "log operational details to stderr")
	// Registered explicitly so the usage string is stable in -schema rather
	// than Cobra's generated "version for tail-ipam" wording.
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
	root.AddCommand(newSubnetCommand(state))
	root.AddCommand(newCalcCommand(state))
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

// buildVersion combines the ldflags version with the embedded VCS revision so
// --version reports exact provenance for an auditable binary.
func buildVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		return buildVersionWithSettings(version, info.Settings)
	}
	return buildVersionWithSettings(version, nil)
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
