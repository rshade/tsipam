# Registry format

This is the format of the default `file://` backend. See
[Registry backends](registry-backends.md) for the SQLite one and how to choose.

The registry is one HuJSON file per tailnet routing key, named
`<tailnet>.ipam.hujson` in `--registry-dir` (default `.`). The key resolves
from `--tailnet`, then `TAILSCALE_TAILNET`, then `local`. It may be an alias
such as `production` rather than the live tailnet name.

## Shape

```hujson
{
  // 4via6 site IDs
  "siteIDs": {
    "1": "hq-vpc",
    "2": {
      "site": "branch-office",
      "owner": "platform-team",
      "ticket": "OPS-1234",
      "reservedAt": "2026-01-15T09:00:00Z",
    },
  },

  // Subnet CIDR pools
  "subnetPools": {
    "10.20.0.0/16": "hq-vpc",
    "2001:db8:20::/48": "branch-office",
  },

  // Per-service IPv4 host reservations
  "serviceAddresses": {
    "100.100.100.8": "svc:dashboard",
  },

  // Per-device UDP listen ports, grouped by site
  "udpPorts": {
    "branch-office": {
      "41642": "branch-fw01",
    },
  },
}
```

All four top-level maps are optional; `{}` is a valid, empty registry. Unknown
top-level keys are preserved untouched so the format can grow. A key that is
a near-miss spelling of a known one (`siteids`) or duplicated is rejected.

## Entry shapes

Site-ID, subnet-pool, and service-address entries are either a bare string
naming the target, or an object carrying the target under `site` (site IDs,
subnet pools) or `service` (service addresses) plus any of `owner`,
`description`, `ticket`, and `reservedAt`. `reserve` writes the bare form
unless a metadata flag is given, and the two forms coexist in one map.

UDP-port leaves are always bare device strings.

`reservedAt` is RFC 3339 in UTC. It is stamped automatically the first time
`owner`, `description`, or `ticket` is set on an entry that has none, and
`--reserved-at` overrides it.

## Keys and validation

| Map | Key | Canonical form | Refused |
| --- | --- | --- | --- |
| `siteIDs` | site ID | decimal `0`–`65535`; `"01"` and `"1"` are the same key | duplicates after canonicalization; empty or `null` targets |
| `subnetPools` | CIDR | network address with prefix length; IPv4 or IPv6 | host bits set; overlap with a pool held by a different site; duplicates |
| `serviceAddresses` | IPv4 host | bare address; `/32` input is accepted and stripped | wider prefixes; IPv6; duplicates |
| `udpPorts` | site, then port | decimal `1024`–`65535` under a site label | duplicates within a site; non-string device |

Leading-zero site IDs are always decimal, never octal: `"017"` is 17.

Service targets are canonicalized with one `svc:` prefix; `dashboard` and
`svc:dashboard` are the same identity.

The same port may appear under different sites: uniqueness is scoped to the
site, which models one firewall boundary.

Validation is scoped per map. A malformed `udpPorts` object blocks port
commands but not site-ID, subnet, or service commands, and vice versa.

## Writes

Every reserve and release rewrites the whole file through
`github.com/rshade/tsacl/pkg/policycore/hujsonedit`: tab indentation, trailing
commas on expanded objects.
Comments are preserved, except that a comment directly above a released entry
is removed with that entry, which is standard HuJSON behavior. A registry made
only of bare-string entries round-trips byte for byte.

Writes take a lock file beside the registry for their duration, so two
concurrent reserves against one file serialize rather than clobber each other.

Reserve creates the file when it is absent. List, release, `plan`, `check`,
`check-policy`, `calc aggregate --from-registry`, and `calc next-free` require
it to exist.

## Version control

Commit the registry next to the policy file it describes. The file is
deterministic for a given set of reservations, so diffs read as a list of
allocation changes, and `check-policy` in CI keeps the two files honest with
each other.
