package main

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/rshade/ax-go"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	goahttp "goa.design/goa/v3/http"
	goa "goa.design/goa/v3/pkg"

	"github.com/rshade/tsacl/pkg/ipcalc"
	"github.com/rshade/tsacl/pkg/policycore/sanitize"

	"github.com/rshade/tsipam/internal/registry"
)

// Error codes, documented in ../../docs/reference/exit-codes.md. The first four
// are verdicts and exit 1; every other code is an unknown answer and exits 2.
const (
	codeRegistryCollision   = "registry_collision"
	codeSplitTooLarge       = "split_too_large"
	codeDriftDetected       = "drift_detected"
	codePolicyDriftDetected = "policy_drift_detected"

	codeValidationError    = "validation_error"
	codePoolExhausted      = "pool_exhausted"
	codeRegistryFailed     = "registry_failed"
	codeCredentialsMissing = "credentials_missing"
	codeCollectionFailed   = "collection_failed"
	codeComparisonFailed   = "comparison_failed"
	codePolicyCheckFailed  = "policy_check_failed"
	codeOutputFailed       = "output_failed"
	codeCanceled           = "canceled"
	codeCommandFailed      = "command_failed"
	contextDriftCount      = "drift_count"
	kindArgument           = "argument"
	//nolint:gosec // advice naming variables, not a credential
	fixMissingCredentials = "set TAILSCALE_TAILNET and either TAILSCALE_API_KEY or an OAuth client ID and secret"
	fixHistoryUnsupported = "select a sqlite registry: --registry sqlite://<path>, or TSIPAM_REGISTRY"
	fixExhaustedPool      = "release a reservation inside the pool, or choose a larger pool or a longer prefix"
	withheldFailurePrefix = "the failure text is withheld because sanitization could not start: "
)

// Goa error names the design declares (see ../../design). They are how a
// *goa.ServiceError decoded off the wire is told apart from every other error
// the generated HTTP client can return.
const (
	serviceErrorCollision         = "collision"
	serviceErrorExhausted         = "exhausted"
	serviceErrorComparisonFailed  = "comparison_failed"
	serviceErrorPolicyCheckFailed = "policy_check_failed"
	serviceErrorInvalidInput      = "invalid_input"
	serviceErrorUnsupported       = "unsupported"
)

// inputError is a value the caller supplied that the command cannot use.
type inputError struct{ err error }

func (e *inputError) Error() string { return e.err.Error() }
func (e *inputError) Unwrap() error { return e.err }

func invalidInput(err error) error {
	if err == nil {
		return nil
	}
	return &inputError{err: err}
}

// exhaustedError reports that a pool has no free block of the requested size.
type exhaustedError struct{ message string }

func (e *exhaustedError) Error() string { return e.message }

// credentialsError reports that the environment lacks what a live read needs.
type credentialsError struct{ err error }

func (e *credentialsError) Error() string { return e.err.Error() }
func (e *credentialsError) Unwrap() error { return e.err }

// collectionError is a failed Tailscale API read made by this process.
type collectionError struct {
	err       error
	retryable bool
}

func (e *collectionError) Error() string { return e.err.Error() }
func (e *collectionError) Unwrap() error { return e.err }

// policyReadError reports that a policy could not be read for comparison.
type policyReadError struct{ err error }

func (e *policyReadError) Error() string { return e.err.Error() }
func (e *policyReadError) Unwrap() error { return e.err }

// outputError is a failed write to stdout.
type outputError struct{ err error }

func (e *outputError) Error() string { return e.err.Error() }
func (e *outputError) Unwrap() error { return e.err }

// driftError is a completed comparison that found drift. It wraps one of the
// two drift sentinels.
type driftError struct {
	sentinel error
	count    int
}

func (e *driftError) Error() string { return e.sentinel.Error() }
func (e *driftError) Unwrap() error { return e.sentinel }

func isDrift(err error) bool {
	return errors.Is(err, errRegistryDrift) || errors.Is(err, errPolicyDrift)
}

// failure is what a command error means to its caller.
type failure struct {
	code      string
	fix       string
	retryable bool
	context   map[string]any
}

func (f failure) exitCode() int {
	switch f.code {
	case codeRegistryCollision, codeSplitTooLarge, codeDriftDetected, codePolicyDriftDetected:
		return exitFailure
	default:
		return exitUsage
	}
}

// classify decides what err means, from types and goa error names only. Error
// text is for people and never decides anything.
func classify(err error) failure {
	// A cancellation can surface through any of the wrappers below.
	if errors.Is(err, context.Canceled) {
		return failure{code: codeCanceled}
	}
	if drift, ok := errors.AsType[*driftError](err); ok {
		code := codeDriftDetected
		if errors.Is(err, errPolicyDrift) {
			code = codePolicyDriftDetected
		}
		return failure{code: code, context: map[string]any{contextDriftCount: drift.count}}
	}
	if serviceErr, ok := errors.AsType[*goa.ServiceError](err); ok {
		return classifyServiceError(serviceErr)
	}
	if isRegistryCollision(err) {
		return failure{code: codeRegistryCollision}
	}
	if errors.Is(err, ipcalc.ErrTooManyChildren) {
		return failure{code: codeSplitTooLarge}
	}
	return classifyCommandError(err)
}

func classifyCommandError(err error) failure {
	if isInvalidInput(err) {
		return failure{code: codeValidationError}
	}
	if _, ok := errors.AsType[*exhaustedError](err); ok {
		return failure{code: codePoolExhausted, fix: fixExhaustedPool}
	}
	if _, ok := errors.AsType[*credentialsError](err); ok {
		return failure{code: codeCredentialsMissing, fix: fixMissingCredentials}
	}
	if collection, ok := errors.AsType[*collectionError](err); ok {
		return failure{code: codeCollectionFailed, retryable: collection.retryable}
	}
	if _, ok := errors.AsType[*policyReadError](err); ok {
		return failure{code: codePolicyCheckFailed}
	}
	if _, ok := errors.AsType[*outputError](err); ok {
		return failure{code: codeOutputFailed}
	}
	// A registry method answers a fault it does not declare with a status the
	// generated client cannot decode.
	if _, ok := errors.AsType[*goahttp.ClientError](err); ok {
		return failure{code: codeRegistryFailed}
	}
	return failure{code: codeCommandFailed}
}

func classifyServiceError(err *goa.ServiceError) failure {
	switch err.Name {
	case serviceErrorCollision:
		return failure{code: codeRegistryCollision}
	case serviceErrorExhausted:
		return failure{code: codePoolExhausted, fix: fixExhaustedPool}
	case serviceErrorComparisonFailed:
		return failure{code: codeComparisonFailed, retryable: err.Timeout || err.Temporary}
	case serviceErrorPolicyCheckFailed:
		return failure{code: codePolicyCheckFailed}
	case serviceErrorUnsupported:
		return failure{code: codeValidationError, fix: fixHistoryUnsupported}
	case serviceErrorInvalidInput,
		// The rest are the names goa gives a request its own decoder refuses.
		goa.InvalidFieldType, goa.MissingField, goa.InvalidEnumValue, goa.InvalidFormat,
		goa.InvalidPattern, goa.InvalidRange, goa.InvalidLength, goa.DecodePayload, goa.MissingPayload:
		return failure{code: codeValidationError}
	default:
		return failure{code: codeRegistryFailed}
	}
}

func isRegistryCollision(err error) bool {
	if _, ok := errors.AsType[*registry.SiteIDCollisionError](err); ok {
		return true
	}
	if _, ok := errors.AsType[*registry.SubnetPoolCollisionError](err); ok {
		return true
	}
	if _, ok := errors.AsType[*registry.ServiceAddressCollisionError](err); ok {
		return true
	}
	_, ok := errors.AsType[*registry.UDPPortCollisionError](err)
	return ok
}

func isInvalidInput(err error) bool {
	if _, ok := errors.AsType[*inputError](err); ok {
		return true
	}
	_, ok := errors.AsType[*registry.InvalidInputError](err)
	return ok
}

// collisionFix names what the caller of cmd can do about a collision.
func collisionFix(cmd *cobra.Command) string {
	noun := map[string]string{
		commandSiteID: "site ID", commandSubnet: "subnet CIDR",
		commandService: "service address", commandPort: "UDP port",
	}[groupName(cmd)]
	if noun == "" {
		noun = "reservation"
	}
	return "release the " + noun + " first, or choose another " + noun
}

func groupName(cmd *cobra.Command) string {
	if cmd == nil || cmd.Parent() == nil {
		return ""
	}
	return cmd.Parent().Name()
}

// errBoundaryApplied marks an error toAxError already converted and sanitized.
// Sanitizing a second time could rewrite the placeholders.
var errBoundaryApplied = errors.New("error boundary applied")

// commandFailed converts an error raised by a command body or by the checks
// that run just before it.
func (s *commandState) commandFailed(cmd *cobra.Command, args []string, err error) error {
	if err == nil || errors.Is(err, errBoundaryApplied) {
		return err
	}
	sanitizer := s.sanitizer
	if sanitizer == nil {
		return s.parseFailed(cmd, args, classify(err), err)
	}
	// A drift verdict contains no identities and its report is already
	// rendered. Avoid reopening registry data for a completed comparison.
	if !isDrift(err) {
		registerKnownIdentityInventory(cmd, s)
	}
	return toAxError(cmd, classify(err), err, sanitizer)
}

// usageFailed converts an error Cobra raised while reading the command line.
func (s *commandState) usageFailed(cmd *cobra.Command, args []string, err error) error {
	if err == nil || errors.Is(err, errBoundaryApplied) {
		return err
	}
	// The run ends before it could replace what an earlier tool call left.
	s.reset()
	return s.parseFailed(cmd, args, failure{code: codeValidationError}, err)
}

// parseFailed converts an error raised before the command built its sanitizer.
func (s *commandState) parseFailed(cmd *cobra.Command, args []string, reason failure, err error) error {
	sanitizer, sanitizerErr := s.argumentSanitizer(cmd, args, err)
	if sanitizerErr != nil {
		withheld := errors.New(withheldFailurePrefix + sanitizerErr.Error())
		return toAxError(cmd, failure{code: codeValidationError}, withheld, nil)
	}
	return toAxError(cmd, reason, err, sanitizer)
}

// toAxError renders err as the one error object the runtime writes. It is the
// only place a failure's code, exit status, and redaction are decided. An error
// the runtime itself built keeps its code and exit status.
func toAxError(cmd *cobra.Command, reason failure, err error, sanitizer *sanitize.Sanitizer) error {
	ctx := cmd.Context()
	if built, ok := errors.AsType[*ax.Error](err); ok {
		options := []ax.ErrorOption{
			ax.WithErrorExitCode(built.ExitCode()), ax.WithErrorContext(built.Context),
			ax.WithActionableFix(built.ActionableFix), ax.WithSuggestions(built.Suggestions...),
			ax.WithErrorCause(errBoundaryApplied),
		}
		if built.Retryable != nil {
			options = append(options, ax.WithRetryable(*built.Retryable))
		}
		return ax.NewError(ctx, built.ErrorCode, sanitizeErrorIfNeeded(built, sanitizer).Error(), options...)
	}
	fix := reason.fix
	if reason.code == codeRegistryCollision {
		fix = collisionFix(cmd)
	}
	options := []ax.ErrorOption{
		ax.WithErrorExitCode(reason.exitCode()), ax.WithErrorContext(reason.context),
		ax.WithActionableFix(fix), ax.WithErrorCause(errBoundaryApplied),
	}
	if reason.retryable {
		options = append(options, ax.WithRetryable(true))
	}
	text := sanitizeErrorIfNeeded(describeTransportFault(err), sanitizer).Error()
	return ax.NewError(ctx, reason.code, text, options...)
}

// argumentSanitizer builds the sanitizer for a failure raised before the
// command's own exists. The command line has not been understood yet, so every
// word of it that is not a command or flag name may be an identity.
func (s *commandState) argumentSanitizer(
	cmd *cobra.Command, args []string, cause error,
) (*sanitize.Sanitizer, error) {
	level := s.requestedLevel(cmd)
	if level == sanitize.Off {
		return nil, nil //nolint:nilnil // no sanitizer is the documented meaning of "off"
	}
	sanitizer, err := sanitize.New(sanitize.Options{Level: level, Tool: toolName, KeyHex: s.getenv(sanitize.KeyEnv)})
	if err != nil {
		return nil, err
	}
	known := knownNames(cmd.Root())
	register := func(value string) {
		if !slices.Contains(known, value) {
			registerSweepValue(sanitizer, kindArgument, value)
		}
	}
	for _, arg := range slices.Concat(s.argv, args) {
		register(arg)
		trimmed := strings.TrimLeft(arg, "-")
		name, value, _ := strings.Cut(trimmed, "=")
		register(trimmed)
		register(name)
		register(value)
	}
	// A tool call passes its values without a command line of its own.
	cmd.Flags().Visit(func(flag *pflag.Flag) { register(flag.Value.String()) })
	if invalid, ok := errors.AsType[*pflag.InvalidValueError](cause); ok {
		register(invalid.GetValue())
	}
	register(resolveTailnetWithEnv("", s.getenv))
	return sanitizer, nil
}

// requestedLevel is the sanitization the caller asked for, as far as the
// command line could be read. A value that cannot be understood still means
// the caller wanted something hidden.
func (s *commandState) requestedLevel(cmd *cobra.Command) sanitize.Level {
	// A tool call's arguments are not on the command line, and parsing may have
	// stopped before its sanitize argument.
	if s.serving && s.sanitizeResolution.Level == "" {
		return sanitize.Identity
	}
	flagValue := s.sanitizeLevel
	if flagValue == string(sanitize.Off) {
		flagValue = sanitizeArgument(s.argv)
	}
	resolution, err := sanitize.Resolve(s.getenv(sanitize.LevelEnv), flagValue,
		cmd.Root().PersistentFlags().Changed("sanitize"))
	if err != nil {
		return sanitize.Identity
	}
	return resolution.Level
}

// sanitizeArgument finds --sanitize in a command line whose parsing may have
// stopped before reaching it.
func sanitizeArgument(argv []string) string {
	for index, arg := range argv {
		if arg == "--sanitize" && index+1 < len(argv) {
			return argv[index+1]
		}
		if value, ok := strings.CutPrefix(arg, "--sanitize="); ok {
			return value
		}
	}
	return string(sanitize.Off)
}

// knownNames lists every command and flag name in the tree.
func knownNames(root *cobra.Command) []string {
	names := []string{root.Name()}
	collect := func(flag *pflag.Flag) { names = append(names, flag.Name) }
	walkCommands(root, func(cmd *cobra.Command) {
		names = append(names, cmd.Name())
		cmd.Flags().VisitAll(collect)
		cmd.PersistentFlags().VisitAll(collect)
	})
	return names
}
