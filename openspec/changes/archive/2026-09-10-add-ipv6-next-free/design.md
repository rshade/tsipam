# IPv6 next-free design

## Context

See `proposal.md` for motivation. `nextFreePool` alone rejects native IPv6;
target validation uses the address bit length, and `ipcalc.NextFree` already
uses arbitrary-width interval arithmetic. Registry entries support IPv6.

## Goals / Non-Goals

Enable IPv6 through the current collection and rendering path. Preserve the
search algorithm, registry semantics, credentials, and IPv4 behavior.

## Decisions

- Remove the family guard and update the Cobra summary, which also supplies
  schema help. Reuse the current validation and rendering path rather than
  duplicate it for IPv6.
- Keep mapped prefixes of `/96` or longer normalized to IPv4; target sizes
  then use IPv4 widths. Shorter mapped-looking prefixes remain native IPv6,
  matching the other calculator commands.
- Keep `--all` and `--count` semantics. Gap search skips excluded ranges;
  explicitly asking for all free children still produces every result.
  Document bounded counts for large pools rather than introduce a new limit.
- Verify real registry and mocked live-route collection together, including
  both search families. Validate JSON and sanitization alongside bare output.

## Risks / Trade-offs

- IPv6 pools can contain enormous result sets → document `--count` for large
  pools and keep `--all` examples small.
- Arithmetic at `/128` and the maximum IPv6 address can overflow fixed-width
  implementations → cover these boundaries in the linked policycore tests.
- Help changes affect the schema golden → deliberately regenerate it and
  inspect the exact diff.

## Migration Plan

No data migration is required. Existing invocations retain their formats.
Archive the change only after the implementation PR merges.
