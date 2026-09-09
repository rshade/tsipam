# Proposal: add-ipv6-calc-info

## Why

Operators can already use `tail-ipam calc info` for IPv4 but must use another
tool for IPv6. The existing rejection is now unnecessary because
`policycore/ipcalc` can supply explicit IPv6 block semantics.

## What Changes

- Make `calc info <cidr>` accept native IPv6 CIDRs as well as IPv4 and mapped
  IPv4 input.
- Omit `broadcast` from IPv6 human and JSON results because it is not an IPv6
  concept.
- Render the exact decimal IPv6 usable-host count in the existing string
  field; no notation cutover is needed because the largest IPv6 count is only
  39 decimal digits and remains precise and parseable.
- Update command help and README examples to document IPv6 behavior.

## Capabilities

### New Capabilities

<!-- None. -->

### Modified Capabilities

- `calc`: extend `calc info` block-fact reporting from IPv4-only to IPv4 and
  IPv6.

## Impact

- `cmd/tail-ipam/calc.go` drops the IPv6 rejection translation.
- `cmd/tail-ipam/output.go`, command tests, schema golden, and README gain
  IPv6 block-fact coverage and documentation.
- The JSON envelope remains compatible: `usable_hosts` stays a decimal string
  and `broadcast` remains omitted when inapplicable.
