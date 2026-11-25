# JSON envelope

A successful command in JSON mode writes one minified JSON line to stdout;
diagnostics stay on stderr. Field names and meanings are a compatibility
surface, and `schema_version` changes before any breaking change to them.

## Choosing the format

The first of these that is set decides:

1. `--format human` or `--format json`
2. `AGENT_MODE`: `1`, `true`, `yes`, `on`, `json`, `agent`, or `machine` for
   JSON; `0`, `false`, `no`, `off`, `human`, or `text` for human text
3. Where stdout goes: human text on a terminal, JSON anywhere else

A script that reads human output from a pipe or `$(...)` therefore needs
`--format human`, or `AGENT_MODE=human` for the whole script.

## The document

The examples on this page are spread out for reading.

```json
{
  "data": {
    "schema_version": "2",
    "tool": "tsipam",
    "command": "subnet reserve",
    "status": "ok",
    "message": "reserved subnet CIDR 10.20.0.0/24 for \"hq-vpc\"",
    "tailnet": "production",
    "changed": true,
    "cidr": "10.20.0.0/24",
    "site": "hq-vpc"
  },
  "meta": {
    "trace_id": "0af7651916cd43dd8448eb211c80319c",
    "span_id": "b7ad6b7169203331",
    "idempotency_key": "00000000-0000-4000-8000-000000000000"
  }
}
```

`data` holds the command's result. `meta` holds the run:

| `meta` field | Contents |
| --- | --- |
| `trace_id`, `span_id` | The run's W3C trace and span IDs. `TRACEPARENT`, when valid, supplies the trace ID. |
| `idempotency_key` | The `--idempotency-key` value, or a fresh key. It is echoed, never stored. |
| `dry_run` | `true` for a `--dry-run` rehearsal; absent otherwise. |

The three `meta` IDs differ on every run. `data.reserved_at` also does on the
commands that can stamp it: `subnet allocate`, and `site-id`, `subnet`, and
`service` `reserve`. `tsipam __schema` lists these per command as
`non_deterministic_fields`; mask them to compare two runs.

## Common fields of `data`

| Field | Values |
| --- | --- |
| `schema_version` | `"2"` |
| `tool` | `"tsipam"` |
| `command` | The subcommand path, for example `"site-id list"` or `"check-policy"` |
| `status` | `"ok"`; `"no_changes"` for a reserve or release that changed nothing; `"drift"` for a check that found drift |
| `message` | Human summary of the result |
| `tailnet` | The registry alias for registry commands, the live tailnet for `devices list` |

Failures write one error object to stderr and nothing to stdout, except that
`check` and `check-policy` write their report before failing on drift; see
[Exit codes](exit-codes.md).

`-schema`, `__schema`, `--version`, `--help`, and `completion` keep their own
output forms and are never wrapped.

## Payload by command

Every field below is inside `data`.

| Command | Field | Contents |
| --- | --- | --- |
| `site-id reserve`, `site-id release` | `changed`, `id`, `site` | `changed` is a boolean; reserve also echoes `owner`, `description`, `ticket`, `reserved_at` when present |
| `subnet reserve`, `subnet release` | `changed`, `cidr`, `site` | plus metadata fields on reserve |
| `subnet allocate` | `changed`, `cidr`, `site` | plus metadata fields; `changed` is `false` on a `--ticket` replay; an exhausted pool exits 2 with no document |
| `service reserve`, `service release` | `changed`, `address`, `service` | plus metadata fields on reserve |
| `port reserve`, `port release` | `changed`, `port`, `site`, `device` | `port` is a decimal string; release omits `device` |
| `site-id list` | `site_ids` | array of `{id, site, owner?, description?, ticket?, reserved_at?}` |
| `subnet list` | `subnet_pools` | array of `{cidr, site, …metadata}` |
| `service list` | `service_addresses` | array of `{address, service, …metadata}` |
| `port list` | `udp_ports` | array of `{site, port, device}` |
| `devices list` | `devices` | array of `{name, addresses, advertised_routes, enabled_routes, tags, user, os, hostname, authorized, ephemeral}` |
| `calc info` | `block_info` | `{prefix, network, broadcast?, first_usable, last_usable, usable_hosts}` |
| `calc split` | `subnets` | array of CIDR strings |
| `calc aggregate` | `supernets` | array of CIDR strings, IPv4 before IPv6 |
| `calc next-free` | `free_blocks` | array of CIDR strings; an exhausted pool exits 2 with no document |
| `plan`, `check` | `comparison` | see below |
| `check-policy` | `policy_comparison` | see below |
| `registry history` | `events` | see below |

Empty lists are always `[]`, never `null`. Optional metadata fields are
omitted rather than emitted as empty strings.

`block_info.usable_hosts` is a decimal **string**, never a JSON number, so
IPv6 counts survive parsers that store numbers as doubles. Use
`jq -r '.data.block_info.usable_hosts | tonumber'` where a number is required.

## `data.comparison`

```json
{
  "live_tailnet": "example.ts.net",
  "drift_count": 1,
  "entries": [
    {
      "kind": "subnet_pool",
      "state": "reserved_but_unadvertised",
      "key": "10.20.0.0/16",
      "expected": "branch-office",
      "observed": [],
      "routes": []
    }
  ],
  "excluded": ["udp_port"]
}
```

- `kind`: `site_id`, `subnet_pool`, or `service_address`.
- `state`: `matched`, `reserved_but_unadvertised`, `unreserved_but_live`, or
  `conflict`.
- `key`: the site ID, CIDR, or address.
- `expected`: the registry's label; omitted for `unreserved_but_live`.
- `observed`: device or service identities providing evidence, sorted and
  deduplicated.
- `routes`: the advertised routes behind the evidence.
- `drift_count`: rows whose state is not `matched`.
- `excluded`: allocation kinds the API cannot observe; always `["udp_port"]`.

Entries sort by kind, then numeric ID or network address and prefix length.

## `data.policy_comparison`

```json
{
  "drift_count": 1,
  "authored": [
    { "ipset": "ipset:hq", "cidr": "10.20.0.0/25", "state": "matched", "pools": ["10.20.0.0/24"] },
    { "ipset": "ipset:lab", "cidr": "172.16.0.0/12", "state": "authored_but_unreserved", "pools": [] }
  ],
  "reserved": [
    { "cidr": "10.20.0.0/24", "site": "hq-vpc", "state": "matched", "ipsets": ["ipset:hq"] }
  ]
}
```

- `authored` rows carry `ipset`, `cidr`, `state` (`matched` or
  `authored_but_unreserved`), and the containing `pools`.
- `reserved` rows carry `cidr`, `site`, `state` (`matched` or
  `reserved_but_unreferenced`), and the referencing `ipsets`.
- `drift_count` counts unmatched rows across both arrays.

Authored rows sort by ipset name, then network address and prefix length;
reserved rows by network address and prefix length.

## `events`

```json
[
  {
    "at": "2026-03-01T12:00:00Z",
    "action": "reserve",
    "kind": "subnet_pool",
    "key": "10.20.0.0/24",
    "before": { "target": "hq-vpc", "ticket": "OPS-1", "reserved_at": "2026-02-01T09:00:00Z" },
    "after": { "target": "hq-vpc", "ticket": "OPS-2", "reserved_at": "2026-02-01T09:00:00Z" },
    "actor": null
  }
]
```

- `at`: when the change was made, RFC 3339 in UTC.
- `action`: `reserve`, `allocate`, `release`, or `import`.
- `kind`: `site_id`, `subnet_pool`, `service_address`, or `udp_port`.
- `key`: the canonical key. `site` is present for `udp_port` only, whose keys are
  scoped to a site.
- `before`, `after`: the stored value, or `null` when the key was not reserved.
  `target` is the site, service, or device; metadata fields are as in the lists.
- `actor`: the authenticated caller, or `null` for a local command.

Events are newest first.

## Under sanitization

With `--sanitize identity`, identity-bearing values in the payload are
replaced by placeholders and `tailnet` is redacted. Numeric site IDs, ports,
states, counts, and field names are unchanged, so a consumer can still parse
and classify a redacted document. See
[Redact output for sharing](../how-to/redact-output-for-sharing.md).

## Migrating from version 1

| Version 1 | Version 2 |
| --- | --- |
| `.cidr`, `.site_ids`, `.comparison`, … | `.data.cidr`, `.data.site_ids`, `.data.comparison`, … |
| Indented over several lines | One minified line |
| Human text unless `--format json` | Human text on a terminal, JSON otherwise |
| `tsipam: <message>` on stderr | One [error object](exit-codes.md) on stderr |
