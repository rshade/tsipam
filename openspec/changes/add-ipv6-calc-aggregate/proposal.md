# Proposal: add-ipv6-calc-aggregate

## Why

`tail-ipam calc aggregate` refuses native IPv6 input even though the merge
behind it is already family-agnostic, so operators summarizing IPv6
allocations (ULA site blocks, delegated `/48`s) must reach for another tool.
The IPv4 aggregation change landed the merge; only the guard and its coverage
remain.

## What Changes

- Make `calc aggregate <cidr...>` accept native IPv6 CIDRs alongside IPv4
  and IPv4-mapped input.
- Accept a mixed IPv4 and IPv6 argument list: each family aggregates
  independently, and IPv4 supernets precede IPv6 supernets in output.
- Treat an IPv4-mapped prefix shorter than `/96` as native IPv6 instead of
  rejecting it, matching `calc split` and `calc info`.
- Update command help and README examples to document IPv6 behavior.

## Capabilities

### New Capabilities

<!-- None. -->

### Modified Capabilities

- `calc`: extend `calc aggregate` from IPv4-only to IPv4 and IPv6, and
  replace the native-IPv6 refusal requirement with accepted-input behavior.

## Impact

- `cmd/tail-ipam/calc.go` drops the IPv6 guard and the IPv4-only help text.
- Command tests, the command-schema golden, and README gain IPv6 and
  mixed-family coverage and documentation.
- The JSON envelope is unchanged: `supernets` remains an ordered string
  array, and no schema-version bump is needed.
- `policycore/ipcalc.Aggregate` needs no algorithm change; its matching
  change documents the two-family contract.
