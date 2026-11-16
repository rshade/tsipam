# Exit codes

`tsipam` uses three exit codes. `tsipam -schema` carries the same
catalog in machine-readable form.

| Code | Meaning | stdout |
| --- | --- | --- |
| `0` | The command completed. Includes no-op reserves and releases. | Result |
| `1` | A well-formed request was refused, or a check found drift. | Result for checks; empty for refusals |
| `2` | The request was invalid, or the command could not complete. | Empty |

## What maps to `1`

- A reserve whose key is already held by a different target: another site
  for a site ID or subnet pool, another service for an address, another
  device for a port within the same site. Overlap counts for subnet pools.
- `calc split` asked to materialize more than 65,536 children.
- `check` after a complete comparison with at least one row that is not
  `matched`.
- `check-policy` after a complete comparison with at least one authored CIDR
  lacking a containing pool or one pool nobody references.

`plan` never exits `1`: it reports and exits `0`.

## What maps to `2`

- Unknown flags or subcommands, wrong argument counts, an invalid `--format`
  or `--sanitize` value.
- Malformed input: a CIDR with host bits set, a site ID outside `0`–`65535`,
  a service address that is not a bare IPv4 host or `/32`, a UDP port outside
  `1024`–`65535`, a missing `--site`, a control character in a label.
- A registry that is missing, unreadable, or fails validation for the map
  the command touches. Reserve and allocate create a missing file; list,
  release, plan, check, aggregate, and next-free do not.
- `plan`, `check`, `devices list`, `calc next-free`, or `subnet allocate`
  without `TAILSCALE_TAILNET` and credentials, or when an API request fails.
- `calc next-free` or `subnet allocate` when the pool has no free block of
  the requested size: the message goes to stderr and stdout stays empty, so
  `subnet reserve "$(tsipam calc next-free …)"` fails at `reserve` with
  `subnet CIDR must not be empty` rather than passing text as a CIDR, and
  `allocate` writes nothing to the registry.
- `check-policy` with a missing or malformed policy, an ipset reference
  cycle, a `host:` alias that is not a literal address, `autogroup:internet`
  or another dynamic selector, or expansion beyond 128 levels or 65,536
  prefixes.
- Any failure writing the result to stdout, even after a comparison found
  drift.

## Reading the codes in scripts

```bash
tsipam check --registry-dir . --tailnet production --format json > check.json
case $? in
  0) echo "registry matches" ;;
  1) echo "drift; see check.json" ;;
  *) echo "comparison did not complete; see stderr" >&2; exit 1 ;;
esac
```

Do not treat `2` as "no drift". It means the answer is unknown.
