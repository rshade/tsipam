# Exit codes and error objects

`tsipam` uses three exit codes. `tsipam -schema` carries the same
catalog in machine-readable form.

| Code | Meaning | stdout |
| --- | --- | --- |
| `0` | The command completed. Includes no-op reserves and releases. | Result |
| `1` | A well-formed request was refused, or a check found drift. | Report for `check` and `check-policy`; empty for refusals |
| `2` | The request was invalid, or the command could not complete. | Empty |

## The error object

A command that exits non-zero writes one error object to stderr, as one JSON
line, in both output modes. Here it is spread out for reading:

```json
{
  "error_code": "registry_collision",
  "message": "site ID 1 is already reserved for \"hq\", not \"branch\"",
  "trace_id": "0af7651916cd43dd8448eb211c80319c",
  "tool": "tsipam",
  "version": "1.2.3",
  "schema_version": "1.0.0",
  "actionable_fix": "release the site ID first, or choose another site ID"
}
```

Other stderr lines, such as `--verbose` logs and the sanitization notice, may
appear around it. Pick the line that has `error_code`.

| Field | Present | Meaning |
| --- | --- | --- |
| `error_code` | Always | One of the codes below. Branch on this, not on `message`. |
| `message` | Always | Text for people. Its wording can change. |
| `trace_id` | Always | The run's W3C trace ID, continued from `TRACEPARENT`. |
| `tool`, `version` | Always | `tsipam` and the version `--version` prints. |
| `schema_version` | Always | Version of this object. |
| `actionable_fix` | Some | What to change before trying again. |
| `retryable` | `true` only | A live read timed out or got an HTTP 5xx answer; the same command may succeed. |
| `context` | Some | Fields for the code, such as `drift_count`. |
| `suggestions` | Some | Flag values that would have been accepted. |

With sanitization active no field holds a site, service, device, owner, CIDR,
address, path, or tailnet value. The CIDRs you pass to `calc split`,
`calc info`, and `calc aggregate` without `--from-registry` are the exception:
those commands are arithmetic on your own input. See
[redact output for sharing](../how-to/redact-output-for-sharing.md).

## Codes that exit `1`

These four are verdicts: the question was answered, and the answer is no.

| `error_code` | When |
| --- | --- |
| `registry_collision` | A reserve names a key another target holds: another site for a site ID or subnet pool, another service for an address, another device for a port within the same site. Overlap counts for subnet pools. |
| `split_too_large` | `calc split` would produce more than 65,536 children. |
| `drift_detected` | `check` finished and at least one row is not `matched`. `context.drift_count` holds the count. |
| `policy_drift_detected` | `check-policy` finished and an authored CIDR has no containing pool, or nobody references a pool. `context.drift_count` holds the count. |

`plan` never exits `1`: it reports and exits `0`.

## Codes that exit `2`

Do not treat `2` as "no drift" or "not reserved". It means the answer is
unknown.

| `error_code` | When |
| --- | --- |
| `validation_error` | An unknown flag or command, a wrong argument count, a missing required flag, an invalid `--format` or `--sanitize` value, an invalid registry URL, `--registry` together with `--registry-dir`, an invalid `registry history` filter, `registry history` on a file registry, or malformed input: a CIDR with host bits set, a site ID outside `0`–`65535`, a service address that is not a bare IPv4 host or `/32`, a UDP port outside `1024`–`65535`, a control character in a label. |
| `confirmation_required` | A `release` ran without `--yes` and nobody approved it. |
| `pool_exhausted` | `calc next-free` or `subnet allocate` found no free block of the requested size. Nothing is written. |
| `registry_failed` | The registry is missing, unreadable, or fails validation for the map the command touches. Reserve and allocate create a missing file; list, release, aggregate, and next-free do not. Also a SQLite database that is missing, holds no registry for the routing key, was written by a newer `tsipam`, or was created by another program, and a write that waits more than 5 seconds for another command's lock. |
| `credentials_missing` | A live command ran without `TAILSCALE_TAILNET` or credentials. |
| `collection_failed` | `devices list`, `calc next-free`, or `subnet allocate` could not read the Tailscale API. |
| `comparison_failed` | `plan` or `check` could not compare everything: an unreadable registry, a failed API read, or a live value it cannot parse. |
| `policy_check_failed` | `check-policy` could not compare everything: a missing or malformed policy, an unreadable registry, an ipset reference cycle, a `host:` alias that is not a literal address, `autogroup:internet` or another dynamic selector, or expansion beyond 128 levels or 65,536 prefixes. |
| `output_failed` | The result could not be written to stdout, even after a comparison found drift. |
| `canceled` | The run was interrupted. |
| `command_failed` | Anything else. |

## Reading the codes in scripts

```bash
tsipam check --registry-dir . --tailnet production --format json \
  > check.json 2> check.err
case $? in
  0) echo "registry matches" ;;
  1) echo "drift; see check.json" ;;
  *) jq -R 'fromjson? | select(.error_code)' check.err >&2; exit 1 ;;
esac
```

A pool with no free block leaves stdout empty, so
`subnet reserve "$(tsipam calc next-free … --format human)"` fails at
`reserve` with `subnet CIDR must not be empty` rather than passing text as a
CIDR.
