# Proposal: add-service-address-reservations

GitHub issue: rshade/tailscale-utils#237 (epic parent #224).

## Why

Operators defining a Tailscale Service have no local source of truth for
reserving its individual IPv4 address before the service exists. `tail-acl`
can later verify that a `svc:` selector resolves, but it cannot prove the
service address was planned or detect two services claiming the same address.

## What Changes

- Add `tail-ipam service reserve <addr> <service-name>`, `service release
  <addr>`, and `service list`, following the existing registry, metadata,
  output, and loopback-transport contracts used by `site-id` and `subnet`.
- Add a comment-preserving `serviceAddresses` registry map from canonical IPv4
  host address to canonical `svc:<name>` identity. A bare IPv4 address and its
  exact `/32` spelling are equivalent inputs; wider prefixes, IPv6, malformed
  addresses, and an empty `svc:` name are rejected rather than truncated.
- Refuse an address already reserved for a different service, make an exact
  repeat idempotent, make release of an absent address a no-op, and list
  reservations in numeric address order.
- Extend the goa API design and regenerate `gen/` so all three commands still
  traverse the generated HTTP client and in-process loopback server.
- Keep parent address-pool declaration, membership checks, next-free
  allocation, and exhaustion detection out of scope. This change records
  explicit `/32` reservations only; it does not invent the bounds model that
  issue #237 deliberately leaves unspecified.

## Capabilities

### New Capabilities

- `service-address-reservations`: Explicit per-service IPv4 `/32` reservation,
  collision, release, listing, persistence, output, and API behavior.

### Modified Capabilities

<!-- none: openspec/specs/ contains no existing capabilities whose
     requirements change. -->

## Impact

- `design/design.go` and generated `gen/ipam` / `gen/http/ipam` contracts gain
  list, reserve, and release methods for service addresses.
- `internal/registry` gains a third independently validated HuJSON map and
  service-address operations; shared map access and reservation-entry helpers
  must accommodate a service identity rather than assuming every reservation
  belongs to a site.
- `internal/ipamsvc/service.go` translates the new domain operations and
  collision into generated goa results/errors.
- `cmd/tail-ipam` gains the `service` command group and versioned JSON/human
  output fields, including sanitization and reflection-contract handling.
- `README.md` and schema/test fixtures gain the new registry and command
  behavior. No new dependency or live Tailscale API permission is required.
