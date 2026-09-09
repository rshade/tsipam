# Add next-free allocation design

## Context

See proposal.md for motivation and the calc delta specification for the
observable contract. The existing registry commands use a loopback client,
while `devices list` already injects a collector that exposes advertised
routes for tests and production.

## Goals / Non-Goals

**Goals:**

- Compose the existing registry read and device collector with the linked
  policycore free-block primitive.
- Preserve the CLI's stdout-only machine payload and existing format envelope.
- Make a single successful result safe for shell substitution.

**Non-Goals:**

- Reserving a suggested block, changing registry schemas, or adding a Tailscale
  client abstraction.
- Implementing IPv6 suggestions, which belongs to issue #257.

## Decisions

### Keep command orchestration in the CLI

The command parses the pool and size, reads local subnet reservations through
the existing loopback client, and asks the injected device collector for live
routes. It converts parseable same-family CIDRs into exclusions before calling
the policycore primitive. This keeps registry and tailnet concepts outside the
shared arithmetic package. A new service endpoint would add generated API
surface for a read-only local composition and is not needed.

### Treat multiple output as repeated gap searches

The first result comes from the shared primitive. Each selected result becomes
an additional exclusion before finding the next one, retaining sorted output
without candidate materialization. `--count` bounds ordinary multi-result
requests; `--all` intentionally continues until exhaustion because its caller
explicitly requests the complete output.

### Keep exhaustion successful and structured

The command emits an empty suggestion list with an informative message when
the pool is exhausted. Human rendering uses the message for that empty result;
non-empty human results render only CIDRs. This preserves a useful JSON
envelope without putting prose beside a composable CIDR.

## Risks / Trade-offs

- [An `--all` request can itself be very large] → It is explicit output work;
  default output remains one block and the search never materializes candidates
  merely to find a result.
- [A device can advertise malformed CIDR text] → Ignore malformed routes
  because they cannot identify a valid overlapping IP prefix; valid registry
  values continue to fail through the established registry read path.
- [Live data changes after the read] → The command is advisory and read-only;
  reservation remains the separate explicit mutation.

## Migration Plan

This is additive. Rollback removes the subcommand and its envelope field; no
stored data is created or migrated.
