# Proposal: add-tail-ipam-calc-info

GitHub issue: rshade/tailscale-utils#250 (epic parent #224).

## Why

Operators sizing a candidate subnet still leave `tail-ipam` for an external
ipcalc/sipcalc to answer the most basic question about a block: its network
address, broadcast address, usable host range, and host count. `calc split`
(#259) opened the stateless-arithmetic surface; block facts are the single
most common ipcalc operation and the natural next occupant.

## What Changes

- New `tail-ipam calc info <cidr>` subcommand under the existing `calc`
  group: pure math over a user-supplied IPv4 CIDR, no flags, no registry
  access, no network access.
- New `policycore/ipcalc.Info` function returning a `BlockInfo` (network,
  broadcast, first/last usable host, usable-host count as `*big.Int` — the
  big-int type is deliberate so the IPv6 follow-up #251 extends the same
  result type instead of forking it).
- RFC 3021-aware edge handling: `/31` reports both addresses usable (no
  broadcast), `/32` reports its single address usable (host route); neither
  errors or shows a nonsensical range.
- IPv6 input is explicitly rejected with a message pointing at follow-up
  issue #251. IPv4-mapped (`::ffff:a.b.c.d/n`) input is normalized to plain
  IPv4 first, matching `calc split`'s existing behavior.
- `outputEnvelope` gains a `BlockInfo *blockInfoOutput` payload field
  (versioned machine contract), with a human tab-separated rendering.

Out of scope: IPv6 semantics (#251), CIDR aggregation (#252), and any
registry awareness — this command never reads or writes the registry.

## Capabilities

### New Capabilities

- `calc-info`: stateless IPv4 block-fact reporting — what `calc info`
  accepts, what facts it reports in each output format, and how the
  RFC 3021 / host-route edge cases and IPv6 rejection behave.

### Modified Capabilities

<!-- none: openspec/specs/ holds no existing capabilities; calc split
     predates this OpenSpec root and its behavior does not change. -->

## Impact

- `policycore/ipcalc/info.go`, `info_test.go` — new (extends the existing
  package added by #259; no new dependencies).
- `tail-ipam/cmd/tail-ipam/calc.go` — register the `info` subcommand on the
  existing `calc` group.
- `tail-ipam/cmd/tail-ipam/output.go` — new envelope field + human render
  branch; the sanitizer copies it through unredacted (same rationale as
  `Subnets`: computed from CLI input, no tailnet-derived identity).
- `tail-ipam/cmd/tail-ipam/contract_test.go` — the `UnhandledFields`
  reflection guard must learn the new field (fails loudly until it does).
- `tail-ipam/cmd/tail-ipam/schema.go` — the command catalog picks the
  subcommand up automatically from the cobra tree, but the hand-written
  sanitization prose says "calc split output … is not sanitized" and must
  generalize to cover `calc info`.
- `tail-ipam/README.md` — `#### calc info` section beside the existing
  `#### calc split`.
- CHANGELOG is release-please-managed; the conventional commit carries it.
