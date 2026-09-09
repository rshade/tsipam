# CIDR Aggregation Proposal

## Why

Operators documenting summary routes need to reduce a list of IPv4 CIDRs to
its minimal cover without leaving `tail-ipam` or manually calculating sibling
merges.

## What Changes

- Add the read-only `tail-ipam calc aggregate <cidr...>` command.
- Render the deterministic minimal IPv4 CIDR cover one CIDR per human-output
  line and in the versioned JSON envelope.
- Reject native IPv6 input until the dedicated IPv6 follow-up is available.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `calc`: aggregate supplied IPv4 CIDRs and render the resulting summary set.

## Impact

Changes the tail-ipam CLI, its JSON contract and schema discovery output,
documentation, and tests. The command is stateless and does not access a
registry, tailnet, or external API.
