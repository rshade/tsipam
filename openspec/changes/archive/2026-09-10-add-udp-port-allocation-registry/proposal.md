# Add Per-Device UDP Port Allocation Registry

## Why

Operators running multiple Tailscale devices behind a firewall that cannot
share outbound UDP 41641 currently track alternate `tailscaled --port`/`PORT`
assignments in tribal knowledge or spreadsheets. A comment-preserving,
collision-safe registry makes those assignments deterministic and auditable,
using the same reserve-before-deploy model that `tail-ipam` already provides
for its other allocation primitives.

## What Changes

- Add explicit per-device UDP listen-port reservations, keyed by site/firewall
  boundary and port, with a device as the reservation owner.
- Add `tail-ipam port reserve <port> <device> --site <site>`, `port release
  <port> --site <site>`, and `port list [--site <site>]` commands routed through
  the generated goa client and loopback service.
- Enforce decimal ports in the inclusive range 1024-65535 and non-empty site
  and device labels after trimming.
- Make repeated reservation of the same site/port/device tuple idempotent,
  reject a different device claiming the same port within one site, and allow
  the same port to be reused in different sites.
- Persist reservations in a nested `udpPorts` HuJSON map (`site -> port ->
  device`) under the existing cross-process registry lock while preserving
  comments, formatting, unknown members, and unrelated reservations.
- Add the UDP-port primitive to `tail-ipam` documentation and to the roadmap's
  allocation-driver list.

Out of scope are automatic next-free-port assignment, validation against live
`tailscaled` state, requiring a matching site-ID reservation, and new output
format machinery. The commands integrate with the repository's now-existing
global human/JSON envelope contract; that infrastructure landed after issue
\#240 originally deferred JSON work to #232.

## Capabilities

### New Capabilities

- `udp-port-reservations`: Validation, per-site collision semantics,
  comment-preserving persistence, deterministic reserve/release/list behavior,
  and API-first CLI discoverability for device UDP listen-port reservations.

### Modified Capabilities

<!-- None. The existing service-address-reservations capability is unchanged. -->

## Impact

- Registry model and HuJSON editing under `internal/registry`, including a new
  nested-map loader/editor and collision error.
- Goa design plus regenerated service/client/server/OpenAPI output under
  `design/` and `gen/`.
- Service implementation and tests under `internal/ipamsvc`.
- Cobra commands, result rendering, sanitization/error handling, schema
  catalog, and tests under `cmd/tail-ipam`.
- User and roadmap documentation in `README.md` and `../ROADMAP.md`.
- No new external dependency and no change to existing registry primitives or
  their on-disk entries.
