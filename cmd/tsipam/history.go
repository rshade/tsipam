package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/rshade/tsacl/pkg/policycore/sanitize"

	ipamclient "github.com/rshade/tsipam/gen/http/ipam/client"
	ipam "github.com/rshade/tsipam/gen/ipam"
	"github.com/rshade/tsipam/internal/registry"
)

const (
	commandRegistry        = "registry"
	commandRegistryHistory = commandRegistry + " history"
	// absentColumn fills a human-output column that has no value.
	absentColumn = "-"
)

// eventOutput is one audit event. Before, After, and Actor are null rather
// than omitted when absent, so a reader can tell "no value" from "not given".
type eventOutput struct {
	At     string            `json:"at"`
	Action string            `json:"action"`
	Kind   string            `json:"kind"`
	Key    string            `json:"key"`
	Site   string            `json:"site,omitempty"`
	Before *eventValueOutput `json:"before"`
	After  *eventValueOutput `json:"after"`
	Actor  *string           `json:"actor"`
}

type eventValueOutput struct {
	reservationMetadataOutput

	Target string `json:"target"`
}

type historyFlags struct {
	kind, key, since string
	limit            int
}

func newRegistryCommand(state *commandState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   commandRegistry,
		Short: "Inspect the registry itself.",
	}
	cmd.AddCommand(newRegistryHistoryCommand(state))
	return cmd
}

func newRegistryHistoryCommand(state *commandState) *cobra.Command {
	var (
		filter      historyFlags
		tailnetFlag string
	)
	const short = "List the registry's audit events, newest first (requires the sqlite backend)."
	cmd := &cobra.Command{
		Use:   "history",
		Short: short,
		Long: short + "\n\n" +
			"Every reservation a command creates, changes, or removes in a sqlite:// registry is\n" +
			"recorded with its value before and after. Events are never edited or deleted.\n" +
			"A file registry keeps no history here; its history lives in version control.\n" +
			"Human output is one tab-separated line per event: time, action, kind, key, site,\n" +
			"before, after, actor. A column with no value is \"-\"; a local command has no actor.",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return executeHistory(cmd, state, filter, tailnetFlag)
		},
	}
	bindInputs(cmd)
	flags := cmd.Flags()
	flags.StringVar(&filter.kind, "kind", "",
		"keep only one kind: site_id, subnet_pool, service_address, or udp_port")
	flags.StringVar(&filter.key, "key", "", "keep only events of this canonical key")
	flags.StringVar(&filter.since, "since", "", "keep only events at or after this RFC 3339 time")
	flags.IntVar(&filter.limit, "limit", registry.DefaultHistoryLimit, "most events to list, at least 1")
	addRegistryFlags(cmd, &tailnetFlag)
	return cmd
}

func executeHistory(cmd *cobra.Command, state *commandState, filter historyFlags, tailnetFlag string) error {
	tailnet := resolveTailnetWithEnv(tailnetFlag, state.getenv)
	outputSanitizer, err := state.outputSanitizer(tailnet)
	if err != nil {
		return err
	}
	if networkKey(filter.key) {
		registerSweepValue(outputSanitizer, networkKind(filter.key), filter.key)
	}
	payload, err := filter.payload(tailnet)
	if err != nil {
		return err
	}
	return state.runRegistryCall(cmd, outputSanitizer,
		func(ctx context.Context, client *ipamclient.Client) (*outputEnvelope, error) {
			res, callErr := client.ListHistory()(ctx, payload)
			if callErr != nil {
				return nil, callErr
			}
			events, ok := res.([]*ipam.RegistryEvent)
			if !ok {
				return nil, fmt.Errorf("unexpected list_history result type %T", res)
			}
			return historyEnvelope(tailnet, events), nil
		})
}

func (f historyFlags) payload(tailnet string) (*ipam.ListHistoryPayload, error) {
	payload := &ipam.ListHistoryPayload{Tailnet: tailnet, Limit: f.limit}
	if f.limit < 1 {
		return nil, invalidInput(errors.New("--limit must be at least 1"))
	}
	if f.kind != "" {
		if _, err := registry.ParseMapKind(f.kind); err != nil {
			return nil, invalidInput(fmt.Errorf("--kind: %w", err))
		}
		payload.Kind = &f.kind
	}
	if f.since != "" {
		if _, err := time.Parse(time.RFC3339, f.since); err != nil {
			return nil, invalidInput(fmt.Errorf("--since %q is not an RFC 3339 time", f.since))
		}
		payload.Since = &f.since
	}
	if f.key != "" {
		payload.Key = &f.key
	}
	return payload, nil
}

func historyEnvelope(tailnet string, events []*ipam.RegistryEvent) *outputEnvelope {
	entries := make([]eventOutput, len(events))
	for i, event := range events {
		entries[i] = eventOutput{
			At: event.At, Action: event.Action, Kind: event.Kind, Key: event.Key,
			Site: stringValue(event.Site), Actor: event.Actor,
			Before: eventValueFrom(event.Before), After: eventValueFrom(event.After),
		}
	}
	output := newOutput(
		commandRegistryHistory, statusOK, fmt.Sprintf("listed %d registry event(s)", len(entries)), tailnet,
	)
	output.Events = &entries
	return output
}

func eventValueFrom(value *ipam.RegistryEventValue) *eventValueOutput {
	if value == nil {
		return nil
	}
	return &eventValueOutput{
		Target: value.Target,
		reservationMetadataOutput: metadataOutputFrom(
			value.Owner, value.Description, value.Ticket, value.ReservedAt,
		),
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func renderEvents(writer io.Writer, events []eventOutput) error {
	for _, event := range events {
		columns := []string{
			event.At, event.Action, event.Kind, event.Key, event.Site,
			event.Before.human(), event.After.human(), stringValue(event.Actor),
		}
		for i, column := range columns {
			if column == "" {
				columns[i] = absentColumn
			}
		}
		if _, err := fmt.Fprintln(writer, strings.Join(columns, "\t")); err != nil {
			return err
		}
	}
	return nil
}

func (v *eventValueOutput) human() string {
	if v == nil {
		return ""
	}
	if !v.hasMetadata() {
		return v.Target
	}
	return v.Target + " " + v.formatMetadata()
}

// eventKinds returns the sanitizer kinds of an event's key and target. An
// empty key kind leaves the key visible: site IDs and port numbers are not
// identity in any list output either.
func eventKinds(kind string) (string, string) {
	switch kind {
	case registry.MapSubnetPools.String():
		return kindCIDR, kindSite
	case registry.MapServiceAddresses.String():
		return kindIP, kindService
	case registry.MapUDPPorts.String():
		return "", kindHostname
	default:
		return "", kindSite
	}
}

func registerEvents(events *[]eventOutput, output *sanitize.Sanitizer) {
	if events == nil {
		return
	}
	for _, event := range *events {
		sanitizeEvent(event, output)
	}
}

func sanitizeEvents(events *[]eventOutput, output *sanitize.Sanitizer) *[]eventOutput {
	if events == nil {
		return nil
	}
	result := make([]eventOutput, len(*events))
	for i, event := range *events {
		result[i] = sanitizeEvent(event, output)
	}
	return &result
}

func sanitizeEvent(event eventOutput, output *sanitize.Sanitizer) eventOutput {
	keyKind, targetKind := eventKinds(event.Kind)
	if keyKind != "" {
		event.Key = output.Value(keyKind, event.Key)
	}
	event.Site = output.Value(kindSite, event.Site)
	event.Before = sanitizeEventValue(event.Before, targetKind, output)
	event.After = sanitizeEventValue(event.After, targetKind, output)
	if event.Actor != nil {
		actor := output.Value(kindOwner, *event.Actor)
		event.Actor = &actor
	}
	return event
}

func sanitizeEventValue(value *eventValueOutput, targetKind string, output *sanitize.Sanitizer) *eventValueOutput {
	if value == nil {
		return nil
	}
	return &eventValueOutput{
		Target:                    output.Value(targetKind, value.Target),
		reservationMetadataOutput: sanitizeMetadata(value.reservationMetadataOutput, output),
	}
}

// networkKey reports whether key is an address or a prefix.
func networkKey(key string) bool {
	if _, err := netip.ParsePrefix(key); err == nil {
		return true
	}
	_, err := netip.ParseAddr(key)
	return err == nil
}
