# Design: add-tail-ipam-calc-info

## Context

See proposal.md for motivation. What matters for the approach is what #250
could not know when it was written: `calc split` has already landed (#259)
and grown IPv6 support (#269), so `policycore/ipcalc`, the `calc` cobra
group, 4-in-6 normalization (`ipcalc.Unmap4In6`), and the sanitize-exempt
precedent for calc output all exist. This change extends that surface
rather than creating it. Constraints that shape the design:

- `outputEnvelope` is a versioned machine contract guarded by a
  `sanitize.UnhandledFields` reflection test (`contract_test.go`); any new
  field fails that test until explicitly handled.
- The `-schema` command catalog is generated from the cobra tree, but its
  `Sanitization` description is hand-written prose that currently names
  `calc split` specifically.
- The repo's AX conventions: result on stdout, parse-clean; stateless
  arithmetic stays in `policycore/ipcalc`, which "knows nothing about
  tail-ipam's registry, CLI, or Tailscale-specific concepts".
- The IPv6 follow-up is issue #251; this change must leave it a clean seam.

## Goals / Non-Goals

**Goals:**

- One new pure function in `policycore/ipcalc` plus a thin CLI wrapper —
  the same collect-free classify-only shape as `Split`.
- A result type (`BlockInfo`) that #251 can extend to IPv6 without forking
  it or breaking the JSON contract.
- Every silent-drift site touched by a new subcommand handled: envelope
  guard, schema prose, README.

**Non-Goals:**

- IPv6 semantics (#251), aggregation (#252), netmask/wildcard-bits output,
  total-address counts — only the four facts the issue names.
- Any registry or control-plane interaction.

## Decisions

### 1. Family rejection lives in `ipcalc.Info`; the CLI translates it

`Info(prefix netip.Prefix) (BlockInfo, error)` returns a sentinel
`ErrIPv6NotSupported` for native IPv6; the CLI maps that sentinel (via
`errors.Is`) to the user-facing message pointing at #251.

- Why: the library stays honest for any caller (a v6 prefix can never
  produce a silently wrong v4-style answer), while the presentation layer
  owns wording — the same split of duties as `ErrTooManyChildren` and its
  CLI-side rewording in `calc.go`.
- Alternative — reject v6 in the CLI before calling `Info`: leaves the
  library able to return garbage for v6, and duplicates the family check
  once #251 moves it anyway.
- Note: `Split`'s doc comment says family restrictions belong to callers,
  but that holds where the arithmetic is family-agnostic. Broadcast and
  usable-range subtraction are v4 concepts; for `Info` the restriction is
  unimplemented semantics, not caller policy.

### 2. `BlockInfo` uses zero `netip.Addr` for "no broadcast"

```go
type BlockInfo struct {
    Network     netip.Addr
    Broadcast   netip.Addr // zero (IsValid() == false) for /31 and /32
    FirstUsable netip.Addr
    LastUsable  netip.Addr
    UsableHosts *big.Int
}
```

- Why: the zero `netip.Addr` is the stdlib's own "absent" idiom
  (`IsValid()`), and it maps directly onto the JSON `omitempty`. `/31` and
  `/32` genuinely have no broadcast (RFC 3021 / host route), so modeling it
  as optional is the honest shape.
- Alternative — always report the block's top address as broadcast
  (sipcalc's behavior): simpler, but for `/31` it labels a usable host
  address "broadcast", contradicting the RFC the issue cites; the spec
  chose omission.
- Alternative — a `HasBroadcast bool`: redundant next to `IsValid()`.

### 3. RFC 3021-aware usable-range arithmetic

`/30` and shorter: first/last usable exclude network and broadcast, count
2^(32−n) − 2. `/31`: both addresses usable, count 2. `/32`: the single
address, count 1. Host bits in the argument are masked off first
(`prefix.Masked()`, matching `Split`).

- Why: the issue's acceptance criterion cites RFC 3021 by number; the
  legacy "0 usable hosts in a /31" answer is exactly the nonsensical output
  it warns against.

### 4. `UsableHosts` is `*big.Int` in Go, a decimal **string** in JSON

- Why `*big.Int`: #250 chose it explicitly so #251 extends the same type
  (v6 counts exceed uint64). Consistent with `Split`'s count return.
- Why a JSON string: `big.Int` marshals as a bare JSON number, and numbers
  above 2^53 silently lose precision in JavaScript consumers. IPv4 alone
  would fit, but switching the field from number to string when #251 lands
  would be a breaking change to the versioned envelope — pick the
  future-proof encoding now, while the field is new.

### 5. Envelope integration follows the `Subnets` precedent exactly

- `BlockInfo *blockInfoOutput` pointer field on `outputEnvelope` (pointer =
  "payload does not apply to this command" when nil, per the envelope's
  documented convention), with `blockInfoOutput` carrying
  `prefix` (the masked CIDR so consumers need not parse the message),
  `network`, `broadcast,omitempty`, `first_usable`, `last_usable`,
  `usable_hosts` as strings.
- `sanitizeOutput` copies it through unredacted with the same rationale
  comment as `Subnets`: computed from a CLI-supplied CIDR, no
  tailnet-derived identity to sweep.
- `contract_test.go`'s `UnhandledFields[outputEnvelope]` handled-list gains
  `"BlockInfo"`, and `blockInfoOutput` gets its own guard entry following
  the sibling structs' pattern.
- Human rendering: a `BlockInfo != nil` branch in `renderOutput` printing
  one `fact\tvalue` line per fact, keys matching the JSON field names so
  human and machine vocabulary stay one set. Broadcast line omitted when
  absent, mirroring the JSON.

### 6. Exit codes: no new mappings

Invalid CIDR and IPv6 rejection both fall through `exitCodeFor`'s default
to `exitUsage` (2) — they are invocation errors, not refused operations.
`calc info` has no exit-1 case, so the schema's exit-code descriptions
stay accurate as written.

### 7. Schema prose generalizes; catalog updates itself

The hand-written `Sanitization` string's "calc split output (subnets and
parent CIDR) is not sanitized…" becomes a statement about `calc` output
generally (stateless arithmetic over CLI-supplied input), so the next calc
subcommand (#252) doesn't need a third edit. The command catalog itself
needs nothing: `commandSchema` walks the cobra tree.

### 8. Command constant and wiring

`commandCalcInfo = commandCalc + " info"` beside `commandCalcSplit` in
`output.go`; `newCalcInfoCommand(state)` registered in `newCalcCommand`.
No flags — `cobra.ExactArgs(1)` and the shared `--format`/`--sanitize`
persistent flags only.

## Risks / Trade-offs

- [Omitted broadcast surprises sipcalc-habituated users on /31] → README
  section states the RFC 3021 behavior explicitly with a /31 example.
- [String-typed `usable_hosts` surprises consumers expecting a number] →
  the field is new (no existing consumer), and the schema description plus
  README document the encoding; jq consumers use `tonumber`.
- [#251 will MODIFY this capability's IPv6-rejection requirement] →
  expected; the spec delta model handles it, and the sentinel-error seam
  (Decision 1) localizes the code change to `Info` plus one CLI message.
- [Silent drift: schema prose and README are unguarded hand-written text]
  → both edits are named tasks, not left to discovery; the prose is
  generalized (Decision 7) to remove the per-subcommand enumeration
  entirely.
- [strict lint (paralleltest, nolintlint) rejects casual test code] →
  follow `calc_test.go` / `split_test.go` conventions, including the
  `runCLI`-based helper for exit-code and stderr assertions.
