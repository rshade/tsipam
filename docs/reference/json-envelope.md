# JSON envelope

Every command accepts `--format json`. Successful results are one JSON
object on stdout; diagnostics stay on stderr. Field names and meanings are a
compatibility surface, and `schema_version` changes before any breaking
change to them.

## Common fields

```json
{
  "schema_version": "1",
  "tool": "tail-ipam",
  "command": "subnet reserve",
  "status": "ok",
  "message": "reserved subnet CIDR 10.20.0.0/24 for \"hq-vpc\"",
  "tailnet": "production"
}
```

| Field | Values |
| --- | --- |
| `schema_version` | `"1"` |
| `tool` | `"tail-ipam"` |
| `command` | The subcommand path, for example `"site-id list"` or `"check-policy"` |
| `status` | `"ok"`; `"no_changes"` for a reserve or release that changed nothing; `"drift"` for a check that found drift |
| `message` | Human summary of the result |
| `tailnet` | The registry alias for registry commands, the live tailnet for `devices list` |

Failures write nothing to stdout in either format; see
[Exit codes](exit-codes.md).

## Payload by command

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
| `devices list` | `devices` | array of `{name, addresses, advertised_routes}` |
| `calc info` | `block_info` | `{prefix, network, broadcast?, first_usable, last_usable, usable_hosts}` |
| `calc split` | `subnets` | array of CIDR strings |
| `calc aggregate` | `supernets` | array of CIDR strings, IPv4 before IPv6 |
| `calc next-free` | `free_blocks` | array of CIDR strings; an exhausted pool exits 2 with no document |
| `plan`, `check` | `comparison` | see below |
| `check-policy` | `policy_comparison` | see below |

Empty lists are always `[]`, never `null`. Optional metadata fields are
omitted rather than emitted as empty strings.

`block_info.usable_hosts` is a decimal **string**, never a JSON number, so
IPv6 counts survive parsers that store numbers as doubles. Use
`jq -r '.block_info.usable_hosts | tonumber'` where a number is required.

## `comparison`

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

## `policy_comparison`

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

## Under sanitization

With `--sanitize identity`, identity-bearing values in the payload are
replaced by placeholders and `tailnet` is redacted. Numeric site IDs, ports,
states, counts, and field names are unchanged, so a consumer can still parse
and classify a redacted document. See
[Redact output for sharing](../how-to/redact-output-for-sharing.md).
