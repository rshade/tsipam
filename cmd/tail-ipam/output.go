package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/netip"

	"github.com/rshade/tailscale-utils/policycore/sanitize"
)

const (
	commandDevices       = "devices"
	commandSiteID        = "site-id"
	commandCalc          = "calc"
	commandList          = "list"
	commandDevicesList   = commandDevices + " " + commandList
	commandSiteIDList    = commandSiteID + " " + commandList
	commandSiteIDReserve = commandSiteID + " reserve"
	commandSiteIDRelease = commandSiteID + " release"
	commandSubnet        = "subnet"
	commandSubnetList    = commandSubnet + " " + commandList
	commandSubnetReserve = commandSubnet + " reserve"
	commandSubnetRelease = commandSubnet + " release"
	commandCalcSplit     = commandCalc + " split"
	statusOK             = "ok"
	statusNoChanges      = "no_changes"
)

func changedStatus(changed bool) string {
	if changed {
		return statusOK
	}
	return statusNoChanges
}

type siteIDOutput struct {
	ID   string `json:"id"`
	Site string `json:"site"`
}

type subnetPoolOutput struct {
	CIDR string `json:"cidr"`
	Site string `json:"site"`
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
	SchemaVersion string              `json:"schema_version"`
	Tool          string              `json:"tool"`
	Sanitization  *sanitize.Metadata  `json:"sanitization,omitempty"`
	Command       string              `json:"command"`
	Status        string              `json:"status"`
	Message       string              `json:"message"`
	Tailnet       string              `json:"tailnet,omitempty"`
	Changed       *bool               `json:"changed,omitempty"`
	ID            string              `json:"id,omitempty"`
	CIDR          string              `json:"cidr,omitempty"`
	Site          string              `json:"site,omitempty"`
	SiteIDs       *[]siteIDOutput     `json:"site_ids,omitempty"`
	SubnetPools   *[]subnetPoolOutput `json:"subnet_pools,omitempty"`
	Devices       *[]deviceRecord     `json:"devices,omitempty"`
	Subnets       *[]string           `json:"subnets,omitempty"`
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
	if result.Devices != nil {
		return renderDevices(writer, *result.Devices)
	}
	if result.Subnets != nil {
		return renderSubnets(writer, *result.Subnets)
	}
	_, err := fmt.Fprintln(writer, result.Message)
	return err
}

func renderSiteIDs(writer io.Writer, entries []siteIDOutput) error {
	for _, entry := range entries {
		if _, err := fmt.Fprintf(writer, "%s\t%s\n", entry.ID, entry.Site); err != nil {
			return err
		}
	}
	return nil
}

func renderSubnetPools(writer io.Writer, entries []subnetPoolOutput) error {
	for _, entry := range entries {
		if _, err := fmt.Fprintf(writer, "%s\t%s\n", entry.CIDR, entry.Site); err != nil {
			return err
		}
	}
	return nil
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
		SchemaVersion: input.SchemaVersion,
		Tool:          input.Tool,
		Sanitization:  output.Metadata(),
		Command:       input.Command,
		Status:        input.Status,
		Message:       output.Text(input.Message),
		Tailnet:       output.Value("tailnet", input.Tailnet),
		Changed:       input.Changed,
		ID:            input.ID,
		CIDR:          output.Value("cidr", input.CIDR),
		Site:          output.Value("site", input.Site),
	}
	if input.SiteIDs != nil {
		entries := make([]siteIDOutput, len(*input.SiteIDs))
		for index, entry := range *input.SiteIDs {
			entries[index] = siteIDOutput{ID: entry.ID, Site: output.Value("site", entry.Site)}
		}
		result.SiteIDs = &entries
	}
	if input.SubnetPools != nil {
		entries := make([]subnetPoolOutput, len(*input.SubnetPools))
		for index, entry := range *input.SubnetPools {
			entries[index] = subnetPoolOutput{
				CIDR: output.Value("cidr", entry.CIDR),
				Site: output.Value("site", entry.Site),
			}
		}
		result.SubnetPools = &entries
	}
	if input.Devices != nil {
		devices := make([]deviceRecord, len(*input.Devices))
		for index, device := range *input.Devices {
			devices[index] = deviceRecord{
				Name:             output.Value("hostname", device.Name),
				Addresses:        sanitizeNetworkValues(device.Addresses, output),
				AdvertisedRoutes: sanitizeNetworkValues(device.AdvertisedRoutes, output),
			}
		}
		result.Devices = &devices
	}
	// Subnets is computed from a CLI-supplied CIDR, not fetched from the
	// tailnet, so unlike every other field above there is no identity to
	// sweep here — copy it through unredacted.
	result.Subnets = input.Subnets
	return result
}

func registerOutputIdentities(input *outputEnvelope, output *sanitize.Sanitizer) {
	output.Value("tailnet", input.Tailnet)
	output.Value("cidr", input.CIDR)
	output.Value("site", input.Site)
	if input.SiteIDs != nil {
		for _, entry := range *input.SiteIDs {
			output.Value("site", entry.Site)
		}
	}
	if input.SubnetPools != nil {
		for _, entry := range *input.SubnetPools {
			output.Value("cidr", entry.CIDR)
			output.Value("site", entry.Site)
		}
	}
	if input.Devices == nil {
		return
	}
	for _, device := range *input.Devices {
		output.Value("hostname", device.Name)
		for _, value := range device.Addresses {
			output.Value(networkKind(value), value)
		}
		for _, value := range device.AdvertisedRoutes {
			output.Value(networkKind(value), value)
		}
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
	return renderOutput(writer, format, result)
}
