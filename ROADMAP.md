# tsipam Strategic Roadmap

## Vision

`tsipam` is where Tailscale address space gets planned before it is used:
site IDs, subnet pools, Service VIPs, and listen ports are reserved in a
registry, and that intent is checked against what the tailnet advertises.
[`CONTEXT.md`](./CONTEXT.md) sets the boundaries. The short version: it never
writes to the tailnet, never runs as a daemon, and never lets live state
rewrite declared intent on its own.

Planning for this module lived in the `tailscale-utils` monorepo until the
2026-11-16 extraction. Open work moved here; the history stays there.

## Immediate Focus

No open issue holds this slot. PR #2, the fixes from the post-release review
of the extraction, is in review.

## Near-Term Vision

Nothing is filed here yet. Two items are known but not yet issues in this
repo:

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

Repo-wide, with no work needed here: rshade/tailscale-utils#320 asks every CLI
to specify its `--schema` output, and `tsipam` already does.

### Parked

Ideas with their promotion conditions. None is filed; file one when its
condition is met.

- **On-prem CIDR planning** `[L]` — planning address space that has not yet
  been advertised as a live route. *Promote when:* an operator needs to
  reserve on-prem ranges alongside Tailscale routes in one registry.
- **`registry diff`** `[S]`/`[M]` — a structural diff between two registry
  snapshots, mirroring `tailnet-audit`'s diff mode. *Promote when:*
  registries are confirmed to live in git and pull-request review rather than
  being edited ad hoc on one machine.
- **Adopt live state into an empty registry** `[M]` — `plan --adopt` writes
  every `unreserved_but_live` entry as a reservation. It lets the wire
  dictate declared intent, the inversion `plan` and `check` exist to catch,
  so it needs a decision issue before any code. *Promote when:* an operator
  adopting `tsipam` on a populated tailnet reports hand-typing as the
  onboarding cost.
- **Shared or remote registry** `[L]` — a persistent listener over the
  existing loopback HTTP path, so a team shares one registry. This
  contradicts the no-daemon hard no, so the bar is high. *Promote when:*
  real multi-host concurrent-writer demand shows up.

## Completed Milestones

### 2026-Q4

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
- No daemon; nothing outlives the command.
- Live state never writes declared intent without an explicit decision.
- `gen/` is generated from `design/`, never edited by hand.
- Policy and address logic come from `tsacl`, not reimplemented here.
- Commands, flags, exit codes, the JSON envelope, and the registry format
  change only through OpenSpec.
- No new personal-repo dependencies beyond `tscli`.
