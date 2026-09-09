# Add next-free allocation

## Why

Operators can reserve subnet pools, but cannot ask `tail-ipam` for the first
safe child CIDR after accounting for both recorded reservations and routes
currently advertised by the tailnet. Planning therefore requires manual,
error-prone comparison before a reservation is made.

## What Changes

- Add the read-only `tail-ipam calc next-free` command for IPv4 pools.
- Read subnet-pool reservations and advertised device routes as exclusions.
- Emit suggested CIDRs directly, including a bare single CIDR suitable for
  shell composition; support multiple suggestions with `--all` or `-n`.
- Report exhaustion as a normal, descriptive result and reject IPv6 pending
  the tracked follow-up.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `calc`: Add state-aware IPv4 free-block suggestions to the calc command.

## Impact

Adds a Cobra subcommand and a JSON-envelope result field in `cmd/tail-ipam`,
uses the existing loopback registry client and device collector, and depends
on the shared `policycore/ipcalc` gap-search API specified by the linked
change of the same name.
