package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"strings"

	"github.com/rshade/tailscale-utils/policycore/sanitize"
)

const (
	commandDevices        = "devices"
	commandSiteID         = "site-id"
	commandCalc           = "calc"
	commandList           = "list"
	commandDevicesList    = commandDevices + " " + commandList
	commandSiteIDList     = commandSiteID + " " + commandList
	commandSiteIDReserve  = commandSiteID + " reserve"
	commandSiteIDRelease  = commandSiteID + " release"
	commandSubnet         = "subnet"
	commandSubnetList     = commandSubnet + " " + commandList
	commandSubnetReserve  = commandSubnet + " reserve"
	commandSubnetRelease  = commandSubnet + " release"
	commandService        = "service"
	commandServiceList    = commandService + " " + commandList
	commandServiceReserve = commandService + " reserve"
	commandServiceRelease = commandService + " release"
	commandPort           = "port"
	commandPortList       = commandPort + " " + commandList
	commandPortReserve    = commandPort + " reserve"
	commandPortRelease    = commandPort + " release"
	commandCalcSplit      = commandCalc + " split"
	commandCalcInfo       = commandCalc + " info"
	commandCalcAggregate  = commandCalc + " aggregate"
	commandCalcNextFree   = commandCalc + " next-free"
	statusOK              = "ok"
	statusNoChanges       = "no_changes"
)

func changedStatus(changed bool) string {
	if changed {
		return statusOK
	}
	return statusNoChanges
}

type siteIDOutput struct {
	reservationMetadataOutput

	ID   string `json:"id"`
	Site string `json:"site"`
}

type subnetPoolOutput struct {
	reservationMetadataOutput

	CIDR string `json:"cidr"`
	Site string `json:"site"`
}

type serviceAddressOutput struct {
	reservationMetadataOutput

	Address string `json:"address"`
	Service string `json:"service"`
}

type udpPortOutput struct {
	Site   string `json:"site"`
	Port   string `json:"port"`
	Device string `json:"device"`
}

// reservationMetadataOutput is the optional ownership/tracking metadata
// shared by every reservation-shaped output field. Embedding it keeps the
// four JSON tags (and the omitempty that lets them disappear cleanly for a
// metadata-free reservation) declared once instead of per output type.
type reservationMetadataOutput struct {
	Owner       string `json:"owner,omitempty"`
	Description string `json:"description,omitempty"`
	Ticket      string `json:"ticket,omitempty"`
	ReservedAt  string `json:"reserved_at,omitempty"`
}

// hasMetadata reports whether o carries any ownership/tracking metadata,
// for callers deciding whether to render it in human output.
func (o reservationMetadataOutput) hasMetadata() bool {
	return o != reservationMetadataOutput{}
}

// formatMetadata renders o as space-separated key=value pairs, in a fixed
// field order, for human (tab-separated) output. Empty for a metadata-free
// reservation.
func (o reservationMetadataOutput) formatMetadata() string {
	var parts []string
	if o.Owner != "" {
		parts = append(parts, "owner="+o.Owner)
	}
	if o.Ticket != "" {
		parts = append(parts, "ticket="+o.Ticket)
	}
	if o.Description != "" {
		parts = append(parts, "description="+o.Description)
	}
	if o.ReservedAt != "" {
		parts = append(parts, "reserved="+o.ReservedAt)
	}
	return strings.Join(parts, " ")
}

// blockInfoOutput mirrors ipcalc.BlockInfo in the machine contract.
// Prefix is the masked CIDR (identity), followed by network, broadcast,
// first_usable, last_usable, and usable_hosts facts. Broadcast is omitted,
// not empty, for IPv6 and for IPv4 /31 or /32 blocks (RFC 3021 point-to-point
// and host routes have no broadcast address). UsableHosts is a decimal string
// rather than a JSON number so every IPv6 count remains exact.
type blockInfoOutput struct {
	Prefix      string `json:"prefix"`
	Network     string `json:"network"`
	Broadcast   string `json:"broadcast,omitempty"`
	FirstUsable string `json:"first_usable"`
	LastUsable  string `json:"last_usable"`
	UsableHosts string `json:"usable_hosts"`
}

type deviceRecord struct {
	Name             string   `json:"name"`
	Addresses        []string `json:"addresses"`
	AdvertisedRoutes []string `json:"advertised_routes"`
}

// outputEnvelope is the owned, versioned machine contract shared by every
// tail-ipam command. Pointer slices distinguish "this command returned an
// empty collection" ([]) from "this payload does not apply to the command"
// (field omitted).
type outputEnvelope struct {
	reservationMetadataOutput

	SchemaVersion    string                  `json:"schema_version"`
	Tool             string                  `json:"tool"`
	Sanitization     *sanitize.Metadata      `json:"sanitization,omitempty"`
	Command          string                  `json:"command"`
	Status           string                  `json:"status"`
	Message          string                  `json:"message"`
	Tailnet          string                  `json:"tailnet,omitempty"`
	Changed          *bool                   `json:"changed,omitempty"`
	ID               string                  `json:"id,omitempty"`
	CIDR             string                  `json:"cidr,omitempty"`
	Address          string                  `json:"address,omitempty"`
	Site             string                  `json:"site,omitempty"`
	Service          string                  `json:"service,omitempty"`
	Port             string                  `json:"port,omitempty"`
	Device           string                  `json:"device,omitempty"`
	SiteIDs          *[]siteIDOutput         `json:"site_ids,omitempty"`
	SubnetPools      *[]subnetPoolOutput     `json:"subnet_pools,omitempty"`
	ServiceAddresses *[]serviceAddressOutput `json:"service_addresses,omitempty"`
	UDPPorts         *[]udpPortOutput        `json:"udp_ports,omitempty"`
	Devices          *[]deviceRecord         `json:"devices,omitempty"`
	Subnets          *[]string               `json:"subnets,omitempty"`
	Supernets        *[]string               `json:"supernets,omitempty"`
	FreeBlocks       *[]string               `json:"free_blocks,omitempty"`
	BlockInfo        *blockInfoOutput        `json:"block_info,omitempty"`

	// messageBuilder, when set, renders Message from this envelope's own
	// exported fields at emit time instead of Message being pre-formatted
	// with raw values. emitOutput calls it after any sanitization has run,
	// so a builder that reads Site/Service/CIDR/Address/ID sees the already-redacted
	// placeholder rather than the original identity -- avoiding the desync
	// a value-based text sweep risks against a %q-escaped value (see
	// sanitizeErrorIfNeeded's doc comment in main.go for the same failure
	// mode on the error path). Unexported: excluded from JSON and from the
	// UnhandledFields reflection guard, since it carries no data of its own.
	messageBuilder func(*outputEnvelope) string
}

func newOutput(command, status, message, tailnet string) *outputEnvelope {
	return &outputEnvelope{
		SchemaVersion: outputSchemaVersion,
		Tool:          toolName,
		Command:       command,
		Status:        status,
		Message:       message,
		Tailnet:       tailnet,
	}
}

func renderOutput(writer io.Writer, format string, result *outputEnvelope) error {
	if format == formatJSON {
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}
	if result.SiteIDs != nil {
		return renderSiteIDs(writer, *result.SiteIDs)
	}
	if result.SubnetPools != nil {
		return renderSubnetPools(writer, *result.SubnetPools)
	}
	if result.ServiceAddresses != nil {
		return renderServiceAddresses(writer, *result.ServiceAddresses)
	}
	if result.UDPPorts != nil {
		return renderUDPPorts(writer, *result.UDPPorts)
	}
	if result.Devices != nil {
		return renderDevices(writer, *result.Devices)
	}
	if result.Subnets != nil {
		return renderSubnets(writer, *result.Subnets)
	}
	if result.Supernets != nil {
		return renderSubnets(writer, *result.Supernets)
	}
	if result.FreeBlocks != nil && len(*result.FreeBlocks) > 0 {
		return renderSubnets(writer, *result.FreeBlocks)
	}
	if result.BlockInfo != nil {
		return renderBlockInfo(writer, *result.BlockInfo)
	}
	_, err := fmt.Fprintln(writer, result.Message)
	return err
}

func renderSiteIDs(writer io.Writer, entries []siteIDOutput) error {
	for _, entry := range entries {
		if err := renderReservationLine(writer, entry.ID, entry.Site, entry.reservationMetadataOutput); err != nil {
			return err
		}
	}
	return nil
}

func renderSubnetPools(writer io.Writer, entries []subnetPoolOutput) error {
	for _, entry := range entries {
		if err := renderReservationLine(writer, entry.CIDR, entry.Site, entry.reservationMetadataOutput); err != nil {
			return err
		}
	}
	return nil
}

func renderServiceAddresses(writer io.Writer, entries []serviceAddressOutput) error {
	for _, entry := range entries {
		if err := renderReservationLine(
			writer, entry.Address, entry.Service, entry.reservationMetadataOutput,
		); err != nil {
			return err
		}
	}
	return nil
}

func renderUDPPorts(writer io.Writer, entries []udpPortOutput) error {
	for _, entry := range entries {
		if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\n", entry.Site, entry.Port, entry.Device); err != nil {
			return err
		}
	}
	return nil
}

// renderReservationLine writes one tab-separated human-output line: key,
// reservation target, and -- only when present -- a third tab-separated field carrying the
// reservation's metadata as space-separated key=value pairs. Omitting the
// third field entirely for a metadata-free reservation, rather than writing
// an empty one, is what lets existing scripts parsing two tab-separated
// columns keep working unchanged.
func renderReservationLine(writer io.Writer, key, target string, metadata reservationMetadataOutput) error {
	line := key + "\t" + target
	if metadata.hasMetadata() {
		line += "\t" + metadata.formatMetadata()
	}
	_, err := fmt.Fprintln(writer, line)
	return err
}

func renderDevices(writer io.Writer, devices []deviceRecord) error {
	for _, device := range devices {
		if _, err := fmt.Fprintf(writer, "%s\t%v\troutes=%v\n",
			device.Name, device.Addresses, device.AdvertisedRoutes); err != nil {
			return err
		}
	}
	return nil
}

// renderBlockInfo writes one tab-separated fact<TAB>value line per block
// fact, keys matching the JSON field names so the human and machine
// vocabularies stay one set. The prefix (masked CIDR) is emitted first,
// then network, broadcast, first_usable, last_usable, and usable_hosts.
// The broadcast line is omitted when the block has none, mirroring the
// JSON encoding.
func renderBlockInfo(writer io.Writer, info blockInfoOutput) error {
	facts := []struct{ key, value string }{
		{"prefix", info.Prefix},
		{"network", info.Network},
		{"broadcast", info.Broadcast},
		{"first_usable", info.FirstUsable},
		{"last_usable", info.LastUsable},
		{"usable_hosts", info.UsableHosts},
	}
	for _, fact := range facts {
		if fact.value == "" {
			continue
		}
		if _, err := fmt.Fprintf(writer, "%s\t%s\n", fact.key, fact.value); err != nil {
			return err
		}
	}
	return nil
}

func renderSubnets(writer io.Writer, subnets []string) error {
	for _, subnet := range subnets {
		if _, err := fmt.Fprintln(writer, subnet); err != nil {
			return err
		}
	}
	return nil
}

// sanitizeOutput rebuilds the known envelope from zero at the output boundary.
// It first registers the complete identity inventory so Message can be swept
// with the same placeholders used by the typed fields.
func sanitizeOutput(input *outputEnvelope, output *sanitize.Sanitizer) *outputEnvelope {
	registerOutputIdentities(input, output)
	result := &outputEnvelope{
		SchemaVersion:             input.SchemaVersion,
		Tool:                      input.Tool,
		Sanitization:              output.Metadata(),
		Command:                   input.Command,
		Status:                    input.Status,
		Message:                   output.Text(input.Message),
		messageBuilder:            input.messageBuilder,
		Tailnet:                   output.Value(kindTailnet, input.Tailnet),
		Changed:                   input.Changed,
		ID:                        input.ID,
		CIDR:                      output.Value(kindCIDR, input.CIDR),
		Address:                   output.Value(kindIP, input.Address),
		Site:                      output.Value(kindSite, input.Site),
		Service:                   output.Value(kindService, input.Service),
		Port:                      input.Port,
		Device:                    output.Value(kindHostname, input.Device),
		reservationMetadataOutput: sanitizeMetadata(input.reservationMetadataOutput, output),
	}
	if input.SiteIDs != nil {
		entries := make([]siteIDOutput, len(*input.SiteIDs))
		for index, entry := range *input.SiteIDs {
			entries[index] = siteIDOutput{
				ID: entry.ID, Site: output.Value(kindSite, entry.Site),
				reservationMetadataOutput: sanitizeMetadata(entry.reservationMetadataOutput, output),
			}
		}
		result.SiteIDs = &entries
	}
	if input.SubnetPools != nil {
		entries := make([]subnetPoolOutput, len(*input.SubnetPools))
		for index, entry := range *input.SubnetPools {
			entries[index] = subnetPoolOutput{
				CIDR:                      output.Value(kindCIDR, entry.CIDR),
				Site:                      output.Value(kindSite, entry.Site),
				reservationMetadataOutput: sanitizeMetadata(entry.reservationMetadataOutput, output),
			}
		}
		result.SubnetPools = &entries
	}
	if input.ServiceAddresses != nil {
		entries := make([]serviceAddressOutput, len(*input.ServiceAddresses))
		for index, entry := range *input.ServiceAddresses {
			entries[index] = serviceAddressOutput{
				Address:                   output.Value(kindIP, entry.Address),
				Service:                   output.Value(kindService, entry.Service),
				reservationMetadataOutput: sanitizeMetadata(entry.reservationMetadataOutput, output),
			}
		}
		result.ServiceAddresses = &entries
	}
	if input.UDPPorts != nil {
		entries := make([]udpPortOutput, len(*input.UDPPorts))
		for index, entry := range *input.UDPPorts {
			entries[index] = udpPortOutput{
				Site: output.Value(kindSite, entry.Site), Port: entry.Port,
				Device: output.Value(kindHostname, entry.Device),
			}
		}
		result.UDPPorts = &entries
	}
	if input.Devices != nil {
		devices := make([]deviceRecord, len(*input.Devices))
		for index, device := range *input.Devices {
			devices[index] = deviceRecord{
				Name:             output.Value(kindHostname, device.Name),
				Addresses:        sanitizeNetworkValues(device.Addresses, output),
				AdvertisedRoutes: sanitizeNetworkValues(device.AdvertisedRoutes, output),
			}
		}
		result.Devices = &devices
	}
	// Subnets, Supernets, and BlockInfo are computed only from a CLI-supplied CIDR, so they
	// have no tailnet-derived identity to sweep.
	result.Subnets = input.Subnets
	result.Supernets = input.Supernets
	result.BlockInfo = input.BlockInfo
	if input.FreeBlocks != nil {
		blocks := make([]string, len(*input.FreeBlocks))
		for index, block := range *input.FreeBlocks {
			blocks[index] = output.Value(kindCIDR, block)
		}
		result.FreeBlocks = &blocks
	}
	return result
}

// sanitizeMetadata sweeps Owner the same way a site or hostname is swept --
// it is an operator-chosen identity, not free text -- and passes
// Description, Ticket, and ReservedAt through unredacted: a description or
// ticket reference is free-form prose the sanitizer's exact-token
// replacement cannot meaningfully redact, and a timestamp carries no tailnet
// identity to sweep.
func sanitizeMetadata(metadata reservationMetadataOutput, output *sanitize.Sanitizer) reservationMetadataOutput {
	metadata.Owner = output.Value(kindOwner, metadata.Owner)
	return metadata
}

func registerOutputIdentities(input *outputEnvelope, output *sanitize.Sanitizer) {
	output.Value(kindTailnet, input.Tailnet)
	output.Value(kindCIDR, input.CIDR)
	output.Value(kindIP, input.Address)
	output.Value(kindSite, input.Site)
	output.Value(kindService, input.Service)
	output.Value(kindHostname, input.Device)
	output.Value(kindOwner, input.Owner)
	if input.SiteIDs != nil {
		for _, entry := range *input.SiteIDs {
			output.Value(kindSite, entry.Site)
			output.Value(kindOwner, entry.Owner)
		}
	}
	if input.SubnetPools != nil {
		for _, entry := range *input.SubnetPools {
			output.Value(kindCIDR, entry.CIDR)
			output.Value(kindSite, entry.Site)
			output.Value(kindOwner, entry.Owner)
		}
	}
	if input.ServiceAddresses != nil {
		for _, entry := range *input.ServiceAddresses {
			output.Value(kindIP, entry.Address)
			output.Value(kindService, entry.Service)
			output.Value(kindOwner, entry.Owner)
		}
	}
	if input.UDPPorts != nil {
		for _, entry := range *input.UDPPorts {
			output.Value(kindSite, entry.Site)
			output.Value(kindHostname, entry.Device)
		}
	}
	registerFreeBlocks(input.FreeBlocks, output)
	if input.Devices == nil {
		return
	}
	for _, device := range *input.Devices {
		output.Value(kindHostname, device.Name)
		for _, value := range device.Addresses {
			output.Value(networkKind(value), value)
		}
		for _, value := range device.AdvertisedRoutes {
			output.Value(networkKind(value), value)
		}
	}
}

func registerFreeBlocks(blocks *[]string, output *sanitize.Sanitizer) {
	if blocks == nil {
		return
	}
	for _, block := range *blocks {
		output.Value(kindCIDR, block)
	}
}

func sanitizeNetworkValues(values []string, output *sanitize.Sanitizer) []string {
	if values == nil {
		return nil
	}
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = output.Value(networkKind(value), value)
	}
	return result
}

func networkKind(value string) string {
	if _, err := netip.ParsePrefix(value); err == nil {
		return "cidr"
	}
	if _, err := netip.ParseAddr(value); err == nil {
		return "ip"
	}
	return "address"
}

func emitOutput(
	writer io.Writer,
	format string,
	result *outputEnvelope,
	outputSanitizer *sanitize.Sanitizer,
) error {
	if outputSanitizer != nil && outputSanitizer.Level() != sanitize.Off {
		result = sanitizeOutput(result, outputSanitizer)
	}
	if result.messageBuilder != nil {
		result.Message = result.messageBuilder(result)
	}
	return renderOutput(writer, format, result)
}
