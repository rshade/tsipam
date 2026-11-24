# tsipam Strategic Roadmap

## Vision

`tsipam` is where Tailscale address space gets planned before it is used:
site IDs, subnet pools, Service VIPs, and listen ports are reserved in a
registry, and that intent is checked against what the tailnet advertises.
[`CONTEXT.md`](./CONTEXT.md) sets the boundaries. The short version: it never
writes to the tailnet, runs no daemon except the opt-in `tsipam serve`, and
never lets live state rewrite declared intent on its own.

Planning for this module lived in the `tailscale-utils` monorepo until the
2026-11-16 extraction. Open work moved here; the history stays there.

## Immediate Focus

No open issue holds this slot. `refactor-registry-store` is archived, which
opens the server track below; its first step is #22.

## Near-Term Vision

File an issue for an item below before picking it up.

### Shared registry server

Promoted from Parked on 2026-11-20. `CONTEXT.md` now allows `tsipam serve` as
the one long-running process, under the conditions it lists. Each step is an
OpenSpec change under `openspec/changes/`, listed in dependency order.

- [ ] #22 `add-registry-export-import`: `registry export` and
      `registry import` (`--merge`, `--plan`), to move reservations between
      stores losslessly. Its prerequisite, `refactor-registry-store`, is
      done. [L]

Not filed yet, in order after #22:

- **`add-sqlite-store`** `[L]` — the `sqlite://` backend, `--registry` and
  `TSIPAM_REGISTRY`, append-only audit events, and `registry history`. Its
  migration guide uses `registry import`.
- **`add-serve-tsnet`** `[L]` — `tsipam serve` runs the full goa `ipam`
  API as `svc:tsipam` on tsnet, with per-request `WhoIsForService`
  identity, grant-capability authorization, `whoami`, and `--server`
  remote mode. Needs the SQLite backend.
- **`add-server-observability`** `[L]` — scheduled drift, `tsipam drift`,
  `/metrics`, and the read-only web UI, all on the same `svc:tsipam` mux
  and behind the `read` capability. Needs `serve`.

### Allocation and drift parity

Added 2026-11-20 from a survey of enterprise DDI, NetBox, AWS VPC IPAM, and
the overlay vendors: features they have validated that fit the tailnet niche.
None needs `tsipam serve`; each runs per invocation against either store.
Each changes a command, the registry format, or the JSON envelope, so each is
an OpenSpec change. Not filed yet, highest value first:

- **Device-IP and `ipPool` checks** `[L]` (`cross-repo`) — `check-policy`
  verifies that every `nodeAttrs` `ipPool` range is reserved and disjoint
  from other pools, and `plan` and `check` flag device addresses that fall
  outside them. `devices list` already reads device addresses. `tsacl`
  parses `ipPool` only in its `internal/aclpolicy` package, so it must be
  exported from `pkg/policycore` first. Read-only: `tsipam` never sets a
  device IP.
- **Pool allocation rules** `[M]` — per-pool minimum, maximum, and default
  prefix length, plus optional required `owner` or `ticket`, stored in the
  registry and enforced by `subnet allocate`. Precedent: Infoblox
  prefix-length policies and AWS IPAM pool netmask rules.
- **Next-free for site IDs and Service VIPs** `[M]` — allocate commands for
  `site-id` and `service` with the same lock and `--ticket` idempotency as
  `subnet allocate`. The Tailscale Terraform provider exposes a Service's
  `addrs` as read-only, so a chosen VIP fills an IaC gap.
- **Reviewed ignore list for drift** `[M]` — registry entries that mark live
  routes or Services as known but unmanaged, reported as an `ignored` row
  instead of `unreserved_but_live` (AWS IPAM's Managed, Unmanaged, and
  Ignored states). A populated tailnet can pass `check` without the parked
  `plan --adopt`, and because the list is declared intent, live state still
  never writes the registry.
- **Pool utilization in output** `[S]` — used and free address counts per
  pool in `subnet list` and its JSON envelope, ahead of the `/metrics`
  endpoint in `add-server-observability`.

### Other near-term work

- **`autogroup:internet` as an ipset member.** Tracked as
  rshade/tailscale-utils#372 (`cross-repo`). The parsing half lands in
  `tsacl`; `tsipam` then consumes the `tsacl` release.
- **Name `tsacl` where the docs still say `tail-acl`.** `README.md` and
  `docs/explanation/registry-and-live-state.md` describe the sibling tool by
  its pre-rename name. File it as a `docs:` issue before picking it up.

## Future Vision (Long-Term)

- [ ] #4 Adopt the ax-go v0.7.0 CLI runtime: `__schema`, MCP, dry-run,
      confirmation, and tracing through `ax.Execute` [L]
      Gated on the repo-wide decision in rshade/tailscale-utils#394, because
      `ax-go` is a personal-repo dependency (a `CONTEXT.md` hard no without
      an explicit exception).
      A read-only MCP server is the first slice worth shipping:
      Infoblox, BlueCat, and NetBox all ship MCP access in 2026.

Repo-wide, with no work needed here: rshade/tailscale-utils#320 asks every CLI
to specify its `--schema` output, and `tsipam` already does.

### Parked

Ideas with their promotion conditions. None is filed; file one when its
condition is met.

- **On-prem CIDR planning** `[L]` — planning address space that has not yet
  been advertised as a live route. *Promote when:* an operator needs to
  reserve on-prem ranges alongside Tailscale routes in one registry.
  A cheap first slice is an `external` or `migration` reservation that marks
  space no route may advertise, after GCP's internal-range usage types.
- **`registry diff`** `[S]`/`[M]` — a structural diff between two registry
  snapshots, mirroring `tailnet-audit`'s diff mode. *Promote when:*
  registries are confirmed to live in git and pull-request review rather than
  being edited ad hoc on one machine. A scheduled `registry export` from a
  `tsipam serve` SQLite store, committed to git, also meets this condition.
- **Adopt live state into an empty registry** `[M]` — `plan --adopt` writes
  every `unreserved_but_live` entry as a reservation. It lets the wire
  dictate declared intent, the inversion `plan` and `check` exist to catch,
  so it needs a decision issue before any code. *Promote when:* an operator
  adopting `tsipam` on a populated tailnet reports hand-typing as the
  onboarding cost. Try the reviewed ignore list under Near-Term first.

## Completed Milestones

### 2026-Q4

- Registry store split from the allocation rules: a `Store` seam, the
  `hujsonstore` backend, and a conformance suite, with no user-visible
  change (`refactor-registry-store`, PR #13, merged 2026-11-17; archived
  2026-11-20). [L]
- `go install` and release archives print the same version (PR #2, merged
  2026-11-16; it ships in the pending 0.1.1 release). [S]
- `tsipam` v0.1.0 released 2026-11-16: the module extracted from
  `tailscale-utils` with its history, renamed from `tail-ipam`, and moved
  onto `tsacl` v0.1.0. [L]
- `devices list` exposes tags, enabled routes, key flags, user, OS, and
  hostname: rshade/tailscale-utils#377 and #375, closed in the monorepo
  before the move. [S]

### 2026-Q3

- `tail-ipam/v0.1.0` tagged 2026-09-11 in the monorepo. Every command the
  2026-08-20 brainstorm designed had shipped: site IDs, subnet pools and
  `allocate`, Service addresses, UDP ports, `calc`, `plan`/`check`, and
  `check-policy`.

## Boundary Safeguards

From [`CONTEXT.md`](./CONTEXT.md):

- Never writes to the tailnet; only device and Service listings are read.
  `tsipam serve` joins as a node and advertises a Service the operator
  defined, and edits nothing.
- No daemon except the opt-in `tsipam serve`; every other command leaves
  nothing running.
- Live state never writes declared intent without an explicit decision.
- `gen/` is generated from `design/`, never edited by hand.
- Policy and address logic come from `tsacl`, not reimplemented here.
- Commands, flags, exit codes, the JSON envelope, and the registry format
  change only through OpenSpec.
- No new personal-repo dependencies beyond `tscli`.
