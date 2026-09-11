# Design: add-ipv6-calc-info

## Context

See proposal.md. `calc info` already normalizes 4-in-6 input to IPv4, then
translates the library's former IPv6 sentinel into a usage error. The output
model already omits an invalid broadcast address, and its usable-host value is
string-encoded expressly to support a large IPv6 count.

## Goals / Non-Goals

**Goals:**

- Allow native IPv6 through the existing command and output pathways.
- Preserve byte-compatible IPv4 output and the mapped-IPv4 normalization.
- Make the documented exact-decimal count rule obvious to human and JSON users.

**Non-Goals:**

- New flags, registry interaction, network calls, or a schema-version bump.
- Changing `calc split` or IPv4 `calc info` semantics.

## Decisions

### Let the library select family semantics

After the existing mapped-IPv4 normalization, pass the parsed prefix directly
to `ipcalc.Info`. Removing the special-case error translation keeps the CLI a
thin adapter and gives downstream callers one authoritative calculation.

### Reuse invalid broadcast as the absence marker

`blockInfoOutput.Broadcast` is already `omitempty`, populated only for valid
addresses, so IPv6 automatically omits the human row and JSON key without a
new nullable field or a wire-format change.

### Exact decimal is the sole host-count rendering

The output field remains a string and renders `big.Int.String()` for every
family. A `/0` IPv6 count is 39 digits, so a `2^k` cutoff adds a second format
without a readability or overflow benefit.

## Risks / Trade-offs

- [Consumers infer broadcast from a top address] → specs, tests, and README
  explicitly document that IPv6 omits it.
- [A large JSON count is parsed as a number] → preserve the documented string
  field and assert exact raw JSON in tests.
