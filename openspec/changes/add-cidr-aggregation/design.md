# CIDR Aggregation Design

## Context

The calc command group already renders stateless CIDR operations directly to
human or versioned JSON output, while `policycore/ipcalc` provides the
arithmetic. See proposal.md and the calc delta for required behavior.

## Goals / Non-Goals

**Goals:**

- Add aggregate using the shared arithmetic primitive and existing calc output
  conventions.
- Preserve parse-clean human and JSON output with no registry or API setup.

**Non-Goals:**

- Registry-sourced CIDRs, allocation changes, or IPv6 aggregation.
- A new output format or sanitizer exception unique to aggregate.

## Decisions

- Parse and normalize each argument at the command boundary, including the
  existing 4-in-6 conversion, then pass canonical IPv4 prefixes to policycore.
  This keeps the shared API independent of CLI-specific diagnostics.
- Add a pointer slice to the common output envelope, matching existing calc
  arrays so an applicable empty result remains distinct from an omitted field.
  Human rendering will write each supernet as one bare line, supporting shell
  composition.
- Register the subcommand in the existing calc group so help and schema output
  discover it automatically. A separate command group was rejected because
  aggregation is CIDR arithmetic alongside split and info.

## Risks / Trade-offs

- [JSON consumers need a stable field] → Add a dedicated `supernets` field and
  contract test rather than overloading `subnets`.
- [Different calc commands could normalize mapped input differently] → Reuse
  the established 4-in-6 handling and cover it with a CLI test.

## Migration Plan

The command and JSON field are additive. No registry schema, configuration, or
existing command behavior changes.
