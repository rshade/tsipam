# Design: add-ipv6-calc-aggregate

## Context

See proposal.md. `calc aggregate` parses each argument, normalizes 4-in-6
input, and passes the masked prefixes to `ipcalc.Aggregate`, which already
sorts IPv4 before IPv6 and never merges or covers across families. The only
IPv6-specific code in the command is the guard that refuses it.

## Goals / Non-Goals

**Goals:**

- Let native IPv6 and mixed-family input flow through the existing command
  and output pathways unchanged.
- Preserve byte-compatible IPv4 output and the mapped-IPv4 normalization.

**Non-Goals:**

- New flags, family filtering, registry interaction, or a schema-version
  bump.
- Any change to the merge algorithm.

## Decisions

### Accept mixed families rather than reject them

A dual-stack route list is the common operator input, and the library
already yields a well-defined result: IPv4 supernets, then IPv6 supernets,
with no cross-family merging. Rejecting mixed input would force callers to
pre-sort by family for no correctness gain. The alternative, a per-family
usage error, adds a failure mode the library does not need.

### Delete the guard rather than gate it

The `Is4()` check exists only to defer IPv6; removing it leaves the command
a thin adapter, as `calc info` became when its guard was removed. The
tracking-issue text in the error and README goes with it.

### Short mapped prefixes follow `calc split`

A mapped-looking prefix shorter than `/96` is not exclusively IPv4-mapped, so
it aggregates as native IPv6 instead of being refused, matching the split
and info commands.

## Risks / Trade-offs

- [Consumers assumed `supernets` was IPv4-only] → README and specs state the
  IPv4-then-IPv6 ordering; the JSON shape is unchanged.
- [Help text change alters the command-schema golden] → regenerate with
  `-update` and inspect the single summary-line diff.
