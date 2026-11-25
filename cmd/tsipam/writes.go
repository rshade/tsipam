package main

import (
	"bufio"
	"context"
	"fmt"
	"strings"

	"github.com/rshade/ax-go"
	"github.com/spf13/cobra"

	"github.com/rshade/tsacl/pkg/policycore/sanitize"

	ipamclient "github.com/rshade/tsipam/gen/http/ipam/client"
)

// registryCall is one service call and the result it renders. It takes whether
// to rehearse from its context.
type registryCall func(context.Context, *ipamclient.Client) (*outputEnvelope, error)

// runRegistryCall makes call and renders its result. A command whose contract
// says it writes runs as an audited effect that --dry-run rehearses.
func (s *commandState) runRegistryCall(
	cmd *cobra.Command, registryDir string, outputSanitizer *sanitize.Sanitizer, call registryCall,
) error {
	var output *outputEnvelope
	run := func(ctx context.Context) error {
		return s.registryTarget(registryDir).run(ctx, func(ctx context.Context, client *ipamclient.Client) error {
			result, err := call(ctx, client)
			output = result
			return err
		})
	}
	err := s.perform(cmd, run)
	if err != nil {
		return err
	}
	return emitOutput(cmd, output, outputSanitizer)
}

// perform runs a service call. The rehearsal and the write are the same call:
// the service discards a rehearsal's changes. Auditing adds two lines to every
// write, so only --verbose asks for it, and the audit line repeats the error it
// is given, so it is given a sanitized one.
func (s *commandState) perform(cmd *cobra.Command, run func(context.Context) error) error {
	if !contractFor(cmd).writes() {
		return run(cmd.Context())
	}
	effect := func(ctx context.Context) error {
		return s.commandFailed(cmd, nil, run(ctx))
	}
	return ax.PerformWithAudit(ax.WithAudit(cmd.Context(), s.verbose), commandName(cmd), effect, effect)
}

// approve returns nil when cmd may remove a reservation: the caller passed
// --yes, the run is a rehearsal, or a person at a terminal agrees. normalize
// validates the key in args first, so a typo is not hidden behind the question.
func (s *commandState) approve(
	cmd *cobra.Command, args []string, normalize func(string) (string, error),
) error {
	ctx := cmd.Context()
	if !contractFor(cmd).removes() || ax.DryRunFromContext(ctx) {
		return nil
	}
	if _, err := normalize(args[0]); err != nil {
		return err
	}
	subject := commandName(cmd) + " removes a reservation"
	outcome, err := ax.Confirm(ctx, subject)
	if outcome != ax.ConfirmationPromptRequired {
		return err
	}
	if s.stdinIsTTY {
		if promptApproval(cmd, subject) {
			return nil
		}
		// The error object must be a line of its own, whatever was typed.
		if _, err = fmt.Fprintln(cmd.ErrOrStderr()); err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
	}
	// Outside human mode the runtime refuses without a prompt; reuse its error.
	_, err = ax.Confirm(ax.WithMode(ctx, ax.ModeJSON), subject)
	return err
}

// promptApproval asks on stderr and reads one line from stdin. Only a complete
// answer of y or yes approves. An interrupt ends the wait: the read itself cannot
// be canceled, so it is left behind for the process to take with it.
func promptApproval(cmd *cobra.Command, subject string) bool {
	if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "%s. Continue? [y/N] ", subject); err != nil {
		return false
	}
	answered := make(chan bool, 1)
	go func() {
		answer, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
		answered <- err == nil && isYes(answer)
	}()
	select {
	case approved := <-answered:
		return approved
	case <-cmd.Context().Done():
		return false
	}
}

func isYes(answer string) bool {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

// outcomeMessage words what a write did, or in a rehearsal would do. verbs are
// the past and plain forms; unchanged says why nothing changed.
func outcomeMessage(o *outputEnvelope, past, plain, object, unchanged string) string {
	changed := o.Changed != nil && *o.Changed
	switch {
	case o.rehearsed && changed:
		return "dry run: would " + plain + " " + object
	case o.rehearsed:
		return "dry run: would change nothing: " + unchanged
	case changed:
		return past + " " + object
	default:
		return unchanged
	}
}
