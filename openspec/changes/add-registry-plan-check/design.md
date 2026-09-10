# Design: Registry Plan and Check

## Context

See proposal.md for motivation. Registry calls already use a generated goa
client over an ephemeral local server; devices use tscli and the official
v2 client. The pinned client exposes `Services().List()` and `Service.Addrs`.
`policycore/addrspace.SiteID` already validates the 4via6 encoding. The
registry isolates validation failures by allocation map.

## Goals / Non-Goals

- Keep collection, pure comparison, HTTP transport, and rendering separate.
- Read one immutable local document before collecting live inventory.
- Reuse authentication and generated transport without changing existing calls.
- Do not add remediation, a daemon, new dependencies, live-only collision
  checks, or inferred mappings from operator site labels to devices.

## Decisions

### One report API shared by both commands

Add a read-only `plan` service method at
`GET /tailnet/{tailnet}/ipam/plan?live_tailnet=...`. The service receives an
injected live collector; credentials remain in the process, never in the
HTTP payload. The loopback runner accepts a configured service in addition
to its existing registry-only convenience entry point. Check consumes the
same report and applies the CLI verdict locally, preventing comparator drift.
Separate plan/check service methods would duplicate identical computation.

### Validate one local snapshot

Add a planning load helper to registry that calls Load once and validates
the three compared maps. UDP-port map validation is deliberately independent:
an unavailable comparison domain cannot obstruct the supported domains.
Separate List calls were rejected because concurrent writes could mix local
generations and each call rereads the entire file.

### Collect with the existing authentication boundary

Extend internal/devices with a combined devices/Services snapshot collector.
It constructs one tscli client under the existing viper mutex, makes bounded
GET requests, and returns no partial snapshot if either fails. New comparison
collection errors identify the operation and HTTP status without echoing
untrusted API bodies or credentials. Existing devices-list behavior is preserved.
Tests reuse the command package's one immutable local API server to avoid
introducing concurrent writers to viper's base-url key.

### Compare allocation presence, not operational health

The pure planner uses typed registry maps and normalized device/Service
records. Site IDs compare numeric presence; subnets use containment; services
compare address/identity pairs. Pool containment is the working choice because
a reservation is an allocation pool that can legitimately contain smaller
advertisements. Exact equality would report those valid child allocations as
drift. This choice was surfaced to the user before authoring.

4via6 prefixes contribute both site-ID evidence and their actual IPv6 route
to the pool comparison. No implicit IPv4/site association is inferred. All
advertised routes, including default routes, participate; enabled routes and
device online status are not proof of allocation ownership. Service presence
means an assigned TailVIP, independent of backend health.

### Use structured evidence and stable output

Each row has a kind, state, key, expected registry label, observed identities,
and routes. The planner sorts rows and deduplicates evidence before rendering.
Output carries both tailnet sources, drift count, and explicit UDP-port
exclusion. The CLI sanitizer rebuilds comparison fields with kind-appropriate
placeholders; a reflection guard records every field decision. Check returns
a sentinel only after successful rendering so output failures still exit 2.

## Risks / Trade-offs

- Live reads are not an atomic Tailscale snapshot. The report describes the
  two successful observations; it never claims transaction-level consistency.
- Site labels cannot prove physical placement. Documentation limits matches
  to allocation presence and leaves live collision detection to tailnet-audit.
- Services permissions are required even if no service addresses are locally
  reserved, since unreserved live addresses must be detected. Failures exit 2.
- Pool/route comparison is proportional to pools times distinct routes. It
  does not enumerate addresses, including for IPv6 or default-route pools.

## Migration Plan

The API, CLI commands, and envelope field are additive. Existing registry
files need no migration; rollback removes only the new commands and endpoint.

## References

- [Official v2 Services client](https://github.com/tailscale/tailscale-client-go-v2/blob/v2.10.1/services.go)
- [Tailscale 4via6 documentation](https://tailscale.com/docs/features/subnet-routers/4via6-subnets)
- ROADMAP.md: UDP ports have no live counterpart; plan/check covers the other
  three allocation primitives.
