# Plan Check Specification Delta

## MODIFIED Requirements

### Requirement: Compare a complete registry and live inventory read-only

`plan` and `check` SHALL accept no positional arguments and SHALL share
`--registry-dir` (default `.`) and `--tailnet` registry selection. Registry
selection SHALL resolve from the flag, then `TAILSCALE_TAILNET`, then `local`.
The live tailnet SHALL always come from required `TAILSCALE_TAILNET`, with
the same API-key/OAuth authentication as `devices list`. Both commands SHALL
read site IDs, subnet pools, and service-address reservations from one
validated registry snapshot and collect all device advertised routes and
Services before reporting results. They SHALL NOT modify the registry or
control plane. UDP-port reservations SHALL be excluded because their live
listen-port setting is unavailable, and the output SHALL disclose this.
In remote mode (`--server` or `TSIPAM_SERVER`), the live tailnet and its
credentials SHALL come from the server's environment instead of the
client's, and `comparison.live_tailnet` SHALL report the server's tailnet.

#### Scenario: A storage alias selects the registry independently

- **WHEN** `plan --tailnet production` runs with `TAILSCALE_TAILNET=example.com`
- **THEN** it compares `production.ipam.hujson` with live `example.com`
- **AND** the result identifies both sources and leaves registry bytes unchanged

#### Scenario: Missing data cannot produce a partial clean report

- **WHEN** the registry is missing, one compared map is invalid, either live
  collection fails, or a live route/address is malformed
- **THEN** both commands exit 2 with empty stdout and a visible stderr error
- **AND** an empty Services list is valid, but a missing list is not

#### Scenario: A remote comparison uses the server's live read

- **WHEN** a client with no `TAILSCALE_TAILNET` or API credentials runs
  `check --server https://tsipam.example.ts.net --tailnet production`
  against a server configured with `TAILSCALE_TAILNET=example.com`
- **THEN** the server compares its `production` registry with live
  `example.com`, `comparison.live_tailnet` is `example.com`, and the client
  exits 0 or 1 by the usual drift rule
