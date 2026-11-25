package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/rshade/ax-go"
	"github.com/spf13/cobra"
	goahttp "goa.design/goa/v3/http"
	goa "goa.design/goa/v3/pkg"

	"github.com/rshade/tsacl/pkg/ipcalc"
	"github.com/rshade/tsacl/pkg/policycore/sanitize"

	ipam "github.com/rshade/tsipam/gen/ipam"
	"github.com/rshade/tsipam/internal/devices"
	"github.com/rshade/tsipam/internal/registry"
)

const secretSite = "hq-secret"

func registryFault(message string) error {
	return goahttp.ErrInvalidResponse("ipam", "list_site_ids", http.StatusInternalServerError,
		`{"name":"fault","message":"`+message+`"}`)
}

func timedOut(err error) error {
	failure := ipam.MakeComparisonFailed(err)
	failure.Timeout = true
	return failure
}

func temporary(err error) error {
	failure := ipam.MakeComparisonFailed(err)
	failure.Temporary = true
	return failure
}

type classifyCase struct {
	name      string
	err       error
	code      string
	exit      int
	fix       string
	retryable bool
}

// classifyCases has a row for every row of the design's classification table.
// Each error's text names secretSite, so one test also proves redaction.
func classifyCases() []classifyCase {
	text := "site " + secretSite + " failed"
	plain := errors.New(text)
	return []classifyCase{
		{name: "input", err: invalidInput(plain), code: codeValidationError, exit: exitUsage},
		{name: "service invalid input", err: ipam.MakeInvalidInput(plain), code: codeValidationError, exit: exitUsage},
		{
			name: "service request decoding", exit: exitUsage, code: codeValidationError,
			err: goa.InvalidFormatError("body.reserved_at", secretSite, goa.FormatDateTime, plain),
		},
		{
			name: "registry invalid input", exit: exitUsage, code: codeValidationError,
			err: func() error { _, err := registry.NormalizeSiteName("hq-secret\x00"); return err }(),
		},
		{
			name: "service collision", err: ipam.MakeCollision(plain),
			code: codeRegistryCollision, exit: exitFailure, fix: "choose another site ID",
		},
		{
			name: "site ID collision", exit: exitFailure, code: codeRegistryCollision, fix: "release the site ID",
			err: &registry.SiteIDCollisionError{ID: "1", Existing: secretSite, Wanted: "wanted"},
		},
		{
			name: "subnet collision", exit: exitFailure, code: codeRegistryCollision, fix: "choose another",
			err: &registry.SubnetPoolCollisionError{
				CIDR: "10.0.0.0/24", ExistingCIDR: "10.0.0.0/24",
				ExistingSite: secretSite, WantedSite: "wanted",
			},
		},
		{
			name: "service address collision", exit: exitFailure, code: codeRegistryCollision, fix: "choose another",
			err: &registry.ServiceAddressCollisionError{
				Address: "100.100.100.7", Existing: "svc:" + secretSite, Wanted: "svc:wanted",
			},
		},
		{
			name: "port collision", exit: exitFailure, code: codeRegistryCollision, fix: "choose another",
			err: &registry.UDPPortCollisionError{
				Site: secretSite, Port: "41642", Existing: "fw01", Wanted: "fw02",
			},
		},
		{
			name: "split too large", exit: exitFailure, code: codeSplitTooLarge,
			err: invalidInput(fmt.Errorf("%s: %w", text, ipcalc.ErrTooManyChildren)),
		},
		{
			name: "service exhausted",
			err:  ipam.MakeExhausted(plain),
			code: codePoolExhausted,
			exit: exitUsage,
			fix:  "larger pool",
		},
		{
			name: "next-free exhausted",
			err:  &exhaustedError{message: text},
			code: codePoolExhausted,
			exit: exitUsage,
			fix:  "larger pool",
		},
		{
			name: "registry drift", exit: exitFailure, code: codeDriftDetected,
			err: &driftError{sentinel: errRegistryDrift, count: 2},
		},
		{
			name: "policy drift", exit: exitFailure, code: codePolicyDriftDetected,
			err: &driftError{sentinel: errPolicyDrift, count: 2},
		},
		{
			name: "credentials", err: &credentialsError{err: plain},
			code: codeCredentialsMissing, exit: exitUsage, fix: "TAILSCALE_API_KEY",
		},
		{name: "collection", err: &collectionError{err: plain}, code: codeCollectionFailed, exit: exitUsage},
		{
			name: "retryable collection", err: &collectionError{err: plain, retryable: true},
			code: codeCollectionFailed, exit: exitUsage, retryable: true,
		},
		{name: "comparison", err: ipam.MakeComparisonFailed(plain), code: codeComparisonFailed, exit: exitUsage},
		{
			name:      "comparison timeout",
			err:       timedOut(plain),
			code:      codeComparisonFailed,
			exit:      exitUsage,
			retryable: true,
		},
		{name: "comparison 5xx", err: temporary(plain), code: codeComparisonFailed, exit: exitUsage, retryable: true},
		{name: "policy check", err: ipam.MakePolicyCheckFailed(plain), code: codePolicyCheckFailed, exit: exitUsage},
		{name: "policy read", err: &policyReadError{err: plain}, code: codePolicyCheckFailed, exit: exitUsage},
		{name: "registry fault", err: registryFault(text), code: codeRegistryFailed, exit: exitUsage},
		{name: "output", err: &outputError{err: plain}, code: codeOutputFailed, exit: exitUsage},
		{name: "canceled", err: fmt.Errorf("%s: %w", text, context.Canceled), code: codeCanceled, exit: exitUsage},
		{
			name: "canceled read", exit: exitUsage, code: codeCanceled,
			err: &collectionError{err: fmt.Errorf("%s: %w", text, context.Canceled)},
		},
		{name: "anything else", err: plain, code: codeCommandFailed, exit: exitUsage},
	}
}

// boundaryCommand is "tsipam site-id reserve", enough tree for toAxError.
func boundaryCommand(ctx context.Context) *cobra.Command {
	root := &cobra.Command{Use: toolName}
	group := &cobra.Command{Use: commandSiteID}
	leaf := &cobra.Command{Use: "reserve"}
	group.AddCommand(leaf)
	root.AddCommand(group)
	leaf.SetContext(ctx)
	return leaf
}

func TestToAxErrorClassification(t *testing.T) {
	t.Parallel()
	for _, test := range classifyCases() {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sanitizer, err := sanitize.New(sanitize.Options{
				Level: sanitize.Identity, Tool: toolName, KeyHex: testSanitizeKey,
			})
			if err != nil {
				t.Fatal(err)
			}
			registerSweepValue(sanitizer, kindSite, secretSite)
			registerSweepValue(sanitizer, kindSite, "hq-secret\x00")

			converted := toAxError(boundaryCommand(t.Context()), classify(test.err), test.err, sanitizer)
			axErr, ok := errors.AsType[*ax.Error](converted)
			if !ok {
				t.Fatalf("toAxError returned %T", converted)
			}
			assertClassified(t, test, axErr)
		})
	}
}

func assertClassified(t *testing.T, test classifyCase, axErr *ax.Error) {
	t.Helper()
	if axErr.ErrorCode != test.code || axErr.ExitCode() != test.exit {
		t.Errorf("code = %s, exit = %d; want %s and %d",
			axErr.ErrorCode, axErr.ExitCode(), test.code, test.exit)
	}
	if !strings.Contains(axErr.ActionableFix, test.fix) || (test.fix == "") != (axErr.ActionableFix == "") {
		t.Errorf("actionable_fix = %q, want %q", axErr.ActionableFix, test.fix)
	}
	if got := axErr.Retryable != nil && *axErr.Retryable; got != test.retryable {
		t.Errorf("retryable = %v, want %t", axErr.Retryable, test.retryable)
	}
	if strings.Contains(axErr.Message, secretSite) || axErr.Message == "" {
		t.Errorf("message = %q, want it sanitized and not empty", axErr.Message)
	}
	if strings.Contains(axErr.Message, "invalid response code") {
		t.Errorf("message = %q, want the service's own text", axErr.Message)
	}
}

func TestDriftErrorCarriesItsCount(t *testing.T) {
	t.Parallel()
	for _, sentinel := range []error{errRegistryDrift, errPolicyDrift} {
		err := &driftError{sentinel: sentinel, count: 2}
		converted := toAxError(boundaryCommand(t.Context()), classify(err), err, nil)
		axErr, ok := errors.AsType[*ax.Error](converted)
		if !ok || axErr.Context[contextDriftCount] != 2 {
			t.Errorf("context of %v = %v, want drift_count 2", sentinel, converted)
		}
	}
}

func TestBoundaryConvertsOnce(t *testing.T) {
	t.Parallel()
	state := &commandState{getenv: emptyEnvironment, sanitizeLevel: string(sanitize.Off)}
	cmd := boundaryCommand(t.Context())
	first := state.commandFailed(cmd, nil, errors.New("boom"))
	if second := state.commandFailed(cmd, nil, first); !errors.Is(second, first) {
		t.Errorf("second conversion = %v, want the first error unchanged", second)
	}
	if err := state.commandFailed(cmd, nil, nil); err != nil {
		t.Errorf("commandFailed(nil) = %v, want nil", err)
	}
}

// TestErrorsTheRuntimeBuildsKeepTheirCode covers an error the runtime made
// itself: its code, guidance, and retry signal stay.
func TestErrorsTheRuntimeBuildsKeepTheirCode(t *testing.T) {
	t.Parallel()
	built := ax.NewError(t.Context(), "confirmation_required", "confirmation required: release",
		ax.WithErrorExitCode(exitUsage), ax.WithActionableFix("pass --yes"), ax.WithSuggestions("--yes"),
		ax.WithRetryable(false))
	converted := toAxError(boundaryCommand(t.Context()), classify(built), built, nil)
	axErr, ok := errors.AsType[*ax.Error](converted)
	if !ok || axErr.ErrorCode != "confirmation_required" || axErr.ExitCode() != exitUsage ||
		axErr.ActionableFix != "pass --yes" || len(axErr.Suggestions) != 1 ||
		axErr.Retryable == nil || *axErr.Retryable {
		t.Errorf("converted = %+v, want the runtime's own error kept", converted)
	}
}

func TestDescribeCollection(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want failure
	}{
		{"timeout", &devices.CollectionError{Err: errors.New("x"), Timeout: true},
			failure{code: codeCollectionFailed, retryable: true}},
		{"5xx", &devices.CollectionError{Err: errors.New("x"), Temporary: true},
			failure{code: codeCollectionFailed, retryable: true}},
		{"4xx", &devices.CollectionError{Err: errors.New("x")}, failure{code: codeCollectionFailed}},
		{"credentials", devices.CheckCredentials(emptyEnvironment),
			failure{code: codeCredentialsMissing, fix: fixMissingCredentials}},
		{"other", errors.New("x"), failure{code: codeCommandFailed}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := classify(describeCollection(test.err))
			if got.code != test.want.code || got.retryable != test.want.retryable || got.fix != test.want.fix {
				t.Errorf("classified as %+v, want %+v", got, test.want)
			}
		})
	}
}
