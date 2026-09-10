# tail-ipam

Allocate and plan Tailscale address space: 4via6 site IDs, subnet CIDR pools
for sites, per-service `/32` reservations for Tailscale Services, and
per-device UDP listen ports. Starts scoped to
Tailscale-route/site-ID/service-VIP/listen-port space; on-prem CIDR planning
(address space that has not yet been advertised as a live route) is an
explicit future-growth direction, not initial scope. See
[`../ROADMAP.md`](../ROADMAP.md) ("Future Vision" → `tail-ipam`) for the full
design rationale.

> **Module** `github.com/rshade/tailscale-utils/tail-ipam`. Conforms to the AX
> conventions in [`../AGENTS.md`](../AGENTS.md#ax-conventions-the-shared-standard):
> command results are written to **stdout** as human text by default or a
> versioned JSON envelope with `-format json`; structured logs and errors go
> to **stderr**.

`tail-ipam` complements, and does not duplicate, two existing tools: `tail-acl`'s
`ipset`/`autoApprovers` are authoring-only (they label or edit ACL policy text,
with no allocation or conflict detection), and `tailnet-audit`'s
`subnet-route-overlap` ([#221](https://github.com/rshade/tailscale-utils/issues/221))
and `4via6-site-id-collision`
([#222](https://github.com/rshade/tailscale-utils/issues/222)) roadmap checks
own collision detection in what is already live. `tail-ipam` reserves address
space before it is advertised and compares those reservations with live use.

## Status

### Shipped

Working commands manage the local address-space registry, perform stateless
CIDR arithmetic, and inspect the live tailnet:

#### `devices list`

Read-only command that lists all devices on the tailnet, including their addresses
and advertised routes. Requires the Tailscale API client to be configured:

```bash
export TAILSCALE_TAILNET=myorg
export TAILSCALE_API_KEY=tskey_...
tail-ipam devices list
```

Or use OAuth credentials:

```bash
export TAILSCALE_TAILNET=myorg
export TAILSCALE_OAUTH_CLIENT_ID=...
export TAILSCALE_OAUTH_CLIENT_SECRET=...
tail-ipam devices list
```

Human output is tab-separated (device name, addresses, advertised routes).
Use `-format json` for the versioned machine contract. Set
`TAILSCALE_SANITIZE=identity` or `strict` when the output must not expose
device names, addresses, routes, or the tailnet name.

#### `plan` and `check`

Compare the local registry with live advertised routes and Tailscale Services.
Both commands are read-only: they read one registry snapshot and collect devices
and Services through the API without changing either source. Use the credentials
shown under `devices list`, with permission to read both devices and Services.
`TAILSCALE_TAILNET` is required and always selects the live source. `--tailnet`
selects the registry alias, defaulting to `TAILSCALE_TAILNET` when omitted.

```bash
export TAILSCALE_TAILNET=myorg
tail-ipam plan --registry-dir /etc/tail-ipam --tailnet production
tail-ipam check --registry-dir /etc/tail-ipam --tailnet production --format json
```

The commands use these matching rules:

| Allocation | A reservation matches when… |
| --- | --- |
| Site ID | Its numeric ID appears in an advertised 4via6 route. |
| Subnet pool | It contains an entire advertised route of the same address family. |
| Service address | The reserved IPv4 address belongs to the reserved `svc:` identity. |

A `/16` pool can therefore match an advertised `/24`; a `/24` reservation does
not match a broader `/16` route. IPv4-mapped prefixes are normalized to IPv4.
4via6 routes contribute their encoded site ID and their actual IPv6 prefix;
they do not implicitly advertise their embedded IPv4 range. Multiple routers
advertising the same route are grouped as evidence. Site labels are registry
annotations, so a matching numeric ID does not prove physical-site ownership.

Each report row has an allocation `kind`, a `key` (ID, CIDR, or address), and
one of four states:

- `matched`: the reservation has matching live evidence.
- `reserved_but_unadvertised`: the reservation has no matching evidence.
- `unreserved_but_live`: live use has no covering reservation.
- `conflict`: a reserved service address is assigned to a different service,
  or to multiple distinct services.

`plan` exits **0** after any complete comparison. `check` emits the same report
and exits **1** when at least one row differs; it exits **0** when all rows match.
Both exit **2**, with diagnostics on stderr and no report on stdout, if the
registry, credentials, either API request, or live allocation data is invalid.
An unavailable Services endpoint is an error even when the registry has no
service reservations. Empty lists from both endpoints are valid observations.
Each API request has a 30-second timeout and honors command cancellation.

Human output is tab-separated with quoted evidence and a final drift count.
JSON adds a `comparison` object to the versioned envelope:

```json
{
  "schema_version": "1",
  "tool": "tail-ipam",
  "command": "check",
  "status": "drift",
  "message": "found 1 allocation drift(s)",
  "tailnet": "production",
  "comparison": {
    "live_tailnet": "myorg",
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
}
```

Entries are ordered by kind (site IDs, subnet pools, service addresses), then
numeric ID or network address and prefix length. Evidence is sorted and
deduplicated. Empty results and evidence remain JSON arrays. `expected` is
omitted for unreserved live entries. `drift_count` counts non-matching rows;
it is not a count of affected devices.

UDP listen ports are explicitly excluded because the API does not expose live
listen-port configuration. Service IPv6 addresses are outside the IPv4 service
registry's scope. Advertised routes, including default routes, participate even
if not approved or currently usable. A match does not establish reachability,
policy approval, or operational health. The two API reads are consecutive
observations, not an atomic control-plane snapshot. Live-only overlap and 4via6
collision analysis remains owned by `tailnet-audit`; these commands compare
registry intent with live evidence.

Use `--sanitize identity` or `strict` to redact registry and live tailnet names,
site labels, device and service identities, addresses, and routes. Numeric site
IDs, states, counts, and exclusions remain visible. A fixed
`TAILSCALE_SANITIZE_KEY` produces stable placeholders scoped to the registry
alias, including for live evidence. API error bodies are omitted from diagnostics;
errors retain the collection operation and HTTP status when available.

#### `check-policy <acl-file>`

Compares every authored ipset definition in a local HuJSON policy with subnet
pools in the selected registry. It runs offline without API credentials and
leaves both files unchanged.

```bash
tail-ipam check-policy policy.hujson --registry-dir /etc/tail-ipam --tailnet production
tail-ipam check-policy policy.hujson --registry-dir /etc/tail-ipam --tailnet production --format json
```

Each effective authored CIDR needs **one containing reservation**. For example,
an authored `192.0.2.0/25` matches a reserved `192.0.2.0/24`. Two adjacent `/25`
reservations do not jointly satisfy an authored `/24`. A reservation counts as
referenced when it overlaps any effective authored CIDR, including a smaller
subnet inside the pool. A broad authored CIDR can therefore be unreserved while
the smaller pools it overlaps are referenced.

The static resolver supports IPv4/IPv6 addresses, CIDRs, address ranges,
`host:` aliases from the policy's `hosts` object, nested `ipset:` references,
and ordered `add`/`remove` operations. When a set removes members, all its
operations must be explicit. These forms follow the
[Tailscale ipset syntax](https://tailscale.com/docs/features/tailnet-policy-file/ip-sets).
Only addresses remaining after the operations participate. Adjacent authored
additions remain separate, and duplicate effective CIDRs within one ipset are
deduplicated. Host aliases must resolve to a literal address or CIDR.

`autogroup:internet`, dynamic selectors, missing references, cycles, malformed
definitions, and ambiguous duplicate keys produce an error. Expansion is
limited to 128 nested levels and 65,536 materialized prefixes across definitions;
large networks such as IPv6 `/0` do not enumerate hosts. There is no partial
report on incomplete input. All ipset definitions participate even if unused
by ACLs or grants. This command does not validate unrelated policy fields,
assess grant reachability, or compare site IDs, service addresses, or UDP ports.

Both output formats include matched and drifting rows. JSON adds
`policy_comparison` to the standard envelope:

- `authored`: rows with `ipset`, `cidr`, `state`, and containing `pools`.
  States are `matched` or `authored_but_unreserved`.
- `reserved`: rows with `cidr`, `site`, `state`, and referencing `ipsets`.
  States are `matched` or `reserved_but_unreferenced`.
- `drift_count`: the number of unmatched rows across both directions.

Authored rows sort by ipset name, then network address and prefix length.
Reserved rows sort by network address and prefix length; evidence sorts
lexically. Empty arrays remain arrays. The command exits **0** for consistency,
**1** after reporting drift, or **2** for invalid invocation, unreadable or
incomplete input, or output failure. `--sanitize identity` and `strict` redact
ipset names, CIDRs, site labels, and registry aliases consistently in both
directions; counts and classifications remain visible.

#### `site-id reserve <id> <site-name>`

Reserves a 4via6 site ID in the local registry. Refuses with an error if the ID
is already reserved for a different site. Reserves the same ID and site name
again in a no-op (idempotent). Creates the registry file if it does not exist.

```bash
tail-ipam site-id reserve 1 hq-vpc
tail-ipam site-id reserve 2 branch-office
```

Select the registry directory and routing/storage key explicitly when needed:

```bash
tail-ipam site-id reserve 1 hq-vpc \
  --registry-dir /etc/tail-ipam \
  --tailnet production
```

`--owner`, `--description`, and `--ticket` attach optional ownership/tracking
metadata to the reservation; `--reserved-at` overrides the RFC 3339 timestamp
that is otherwise auto-stamped (UTC, now) the first time any of the other
three is set on a reservation that does not already carry one:

```bash
tail-ipam site-id reserve 1 hq-vpc --owner platform-team --ticket OPS-1234 \
  --description "HQ gateway, migrated from the old ACL"
```

Reserving the same id and site again amends only the fields explicitly
passed — a field left out keeps its stored value rather than being cleared —
so a later `--ticket OPS-5678` on its own updates just the ticket, leaving
`--owner` and the original `--reserved-at` untouched. A bare reserve (no
metadata flags at all) stays exactly as idempotent as before, including
against an already-owned reservation. Reserving the same id for a
*different* site is always refused as a collision, regardless of metadata.

#### `site-id release <id>`

Releases a reserved site ID. Errors if the registry file does not exist.
Releases a never-reserved ID as a no-op (not an error).

```bash
tail-ipam site-id release 1
tail-ipam site-id release 2 --registry-dir /etc/tail-ipam --tailnet production
```

#### `site-id list`

Lists all reserved site IDs in the registry, sorted numerically by ID.
Errors if the registry file does not exist.

```bash
tail-ipam site-id list
tail-ipam site-id list --registry-dir /etc/tail-ipam --tailnet production
```

Human output is tab-separated (site ID, site name), plus a third
tab-separated field — space-separated `key=value` pairs, e.g.
`owner=platform-team ticket=OPS-1234` — appended only for a reservation that
carries metadata; a metadata-free reservation's line is unchanged. `-format
json` emits the same entries in the versioned envelope, with `owner`,
`description`, `ticket`, and `reserved_at` present only when set.

#### `subnet reserve <cidr> <site-name>`

Reserves a canonical IPv4 or IPv6 subnet CIDR pool for a site. Refuses an
exact duplicate or any containing/contained overlap already reserved for a
different site. Adjacent pools do not conflict, and overlaps owned by the same
site are permitted. Repeating the exact CIDR and site is an idempotent no-op.
This checks retained reservations whether or not they are advertised live; it
does not query the Tailscale control plane.

```bash
tail-ipam subnet reserve 10.20.0.0/16 hq-vpc
tail-ipam subnet reserve 2001:db8:20::/48 branch-office
```

`subnet reserve` accepts the same `--owner`, `--description`, `--ticket`, and
`--reserved-at` metadata flags as `site-id reserve`, with the same amend and
collision-regardless-of-metadata semantics described above.

CIDRs must already be in `net/netip`'s canonical form. For example,
`10.20.0.1/16` is rejected with a suggestion to use `10.20.0.0/16`, rather
than silently reserving a different network from the one written.

#### `subnet release <cidr>`

Releases an exact subnet-pool reservation. Errors if the registry file does
not exist; releasing a valid canonical CIDR that is not reserved is a no-op.

```bash
tail-ipam subnet release 10.20.0.0/16
```

#### `subnet list`

Lists all subnet-pool reservations sorted by network address. Human output is
tab-separated (CIDR, site name), with the same metadata third field as
`site-id list` when a reservation carries any; `-format json` emits
`subnet_pools` in the versioned envelope, `owner`/`description`/`ticket`/
`reserved_at` included only when set.

```bash
tail-ipam subnet list
tail-ipam subnet list --registry-dir /etc/tail-ipam --tailnet production
```

#### `service reserve <address> <service-name>`

Reserves one IPv4 host address for a Tailscale Service. The address may be a
bare IPv4 host or an exact `/32`; both forms are stored and returned as the
canonical bare address. A wider prefix is rejected rather than silently
truncated, and native IPv6 and IPv4-mapped IPv6 inputs are rejected. The
service may be written as either `dashboard` or `svc:dashboard`; both forms
are stored and returned as exactly `svc:dashboard`.

```bash
tail-ipam service reserve 100.100.100.8 dashboard
tail-ipam service reserve 100.100.100.9/32 svc:metrics \
  --owner network-team --ticket NET-237 --format json
```

The same canonical address and service is an idempotent no-op. Reserving an
address already held by a different service is a collision (exit code 1),
while one service may intentionally hold multiple addresses. The command
accepts the same `--owner`, `--description`, `--ticket`, and `--reserved-at`
metadata flags and amend semantics as the other reservation primitives.

This is explicit-address bookkeeping only. There is no parent service pool,
pool-membership validation, next-free allocation, or exhaustion calculation;
declaring and allocating from bounded pools is separate future work. The
command also does not query or create a live Tailscale Service.

#### `service release <address>`

Releases the canonical reservation selected by a bare IPv4 address or exact
`/32`. An absent reservation is a successful no-op; a missing registry file is
an error.

```bash
tail-ipam service release 100.100.100.8
tail-ipam service release 100.100.100.9/32 --format json
```

#### `service list`

Lists service-address reservations in numeric address order. Human output is
tab-separated (bare address, canonical `svc:` identity), with the optional
metadata third field used by the other registry lists. JSON output carries a
`service_addresses` array with `address`, `service`, and any set metadata.

```bash
tail-ipam service list
tail-ipam service list --format json
```

#### `port reserve <port> <device> --site <site>`

Reserves an explicit UDP listen port for one device within a site/firewall
boundary. This is intended for deployments where a firewall cannot let
multiple devices share tailscaled's default UDP 41641. Configure the returned
port with `tailscaled --port` or the `PORT` environment variable.

```bash
tail-ipam port reserve 41642 branch-fw01 --site branch-office
tail-ipam port reserve 41643 branch-fw02 --site branch-office \
  --registry-dir /etc/tail-ipam --tailnet production
```

Ports must contain only ASCII decimal digits and fall within 1024-65535.
Leading zeroes are accepted and canonicalized, so `041642` and `41642` select
the same reservation. Device and site labels are trimmed and must remain
non-empty and free of control characters.

Uniqueness is scoped to the site, not the whole tailnet. Reserving the same
site, port, and device again is an idempotent no-op. Reserving that port for a
different device in the same site is a collision (exit code 1), but a device
behind a different firewall may reuse it:

```bash
# Allowed: the sites represent different firewall boundaries.
tail-ipam port reserve 41642 hq-fw01 --site hq-vpc
```

This command records only explicit reservations. It does not choose the next
free port, inspect tailscaled's live configuration, or require `--site` to
match a site-ID reservation. UDP-port reservations carry no ownership or
tracking metadata.

#### `port release <port> --site <site>`

Releases a reservation from the selected site. Releasing an absent site/port
pair is a successful no-op; a missing registry file is an error.

```bash
tail-ipam port release 41642 --site branch-office
tail-ipam port release 41643 --site branch-office --format json
```

#### `port list [--site <site>]`

Lists every UDP-port reservation, sorted by site and then numerically by port.
`--site` filters the result to one firewall boundary. Human output is
tab-separated (`site`, `port`, `device`); JSON output carries a `udp_ports`
array whose entries have those same three fields. An empty result remains an
empty JSON array.

```bash
tail-ipam port list
tail-ipam port list --site branch-office
tail-ipam port list --site branch-office --format json
```

#### `calc split`

Stateless IPv4 and IPv6 CIDR splitting: divides a parent CIDR into equal-sized
child subnets. No registry, network I/O, or Tailscale API credentials required.

```bash
tail-ipam calc split 10.0.0.0/16 --prefix 24
tail-ipam calc split fd7a:115c:a1e0::/48 --prefix 64
tail-ipam calc split 2001:db8::/32 --prefix 64
```

The first command produces 256 subnets of size `/24`. The second divides
Tailscale's ULA `/48` into 65,536 `/64`s, exactly the in-memory materialization
limit. The third requests 4,294,967,296 `/64`s, which exceeds that limit;
instead of enumerating them, `calc split` refuses the request (exit code 1,
like a site-ID collision) and reports the exact count with a suggested
narrower parent or shorter target on stderr:

```bash
tail-ipam calc split 2001:db8::/32 --prefix 64
# stderr: tail-ipam: splitting 2001:db8::/32 into /64 blocks would produce
# 4294967296 subnets; too many to list (max 65536); use a shorter target or
# narrower parent: ipcalc: split would produce too many children to
# materialize
# exit code: 1
```

This refusal carries no structured envelope, in either format — `-format
json` changes how a successful split is rendered, not how a refusal is
reported. Human output for a successful split is one CIDR per line; `-format
json` emits all subnets in the versioned envelope.

IPv4-mapped (4-in-6) prefixes with a length of `/96` or longer are converted to
their equivalent plain IPv4 form before splitting, so `--prefix` then specifies
IPv4 bits (0–32). Shorter 4-in-6-like prefixes cannot be exclusively IPv4-mapped
and are treated as native IPv6 (so `--prefix` specifies IPv6 bits, 0–128). For
example, `tail-ipam calc split '::ffff:10.0.0.0/104' --prefix 20` normalizes to
`10.0.0.0/8` and splits it into `/20` IPv4 subnets.

#### `calc aggregate`

IPv4 and IPv6 CIDR summarization: merges supplied CIDRs into the
smallest set of supernets that covers exactly the same addresses. With
positional CIDRs it is stateless, requiring no registry, network I/O, or
Tailscale API credentials. Duplicate or covered
CIDRs collapse, and adjacent siblings merge recursively; non-adjacent CIDRs
remain separate. Human output is one bare CIDR per line in network-address
order, making it safe to compose in a shell:

```bash
tail-ipam calc aggregate 10.0.0.0/25 10.0.0.128/25
# 10.0.0.0/24

tail-ipam calc aggregate fd12:3456:789a:1::/64 fd12:3456:789a:2::/63 fd12:3456:789a::/64
# fd12:3456:789a::/62

tail-ipam calc aggregate 10.0.0.0/24 10.1.0.0/24 --format json
```

IPv4 and IPv6 CIDRs may be mixed in one invocation. Each family is aggregated
on its own, so a CIDR never merges with, or is covered by, a CIDR of the other
family, and every IPv4 supernet is listed before any IPv6 supernet:

```bash
tail-ipam calc aggregate 2001:db8::/49 10.0.0.128/25 2001:db8:0:8000::/49 10.0.0.0/25
# 10.0.0.0/24
# 2001:db8::/48
```

JSON output carries the same ordered result in the `supernets` array of the
versioned envelope. IPv4-mapped prefixes of `/96` or longer are normalized to
their plain IPv4 equivalents before aggregation, exactly as `calc split` and
`calc info` do; shorter mapped-looking prefixes cannot be exclusively
IPv4-mapped and are aggregated as native IPv6.

To summarize the subnet pools already reserved in a local registry, use
`--from-registry`:

```bash
tail-ipam calc aggregate --from-registry \
  --registry-dir /etc/tail-ipam --tailnet production
tail-ipam calc aggregate --from-registry \
  --registry-dir /etc/tail-ipam --tailnet production --format json
```

Registry aggregation supports IPv4-only, IPv6-only, and mixed-family subnet
pools. For example, reservations for `10.0.0.0/25` and `10.0.0.128/25` produce
`10.0.0.0/24`; IPv6 reservations for `2001:db8::/49` and
`2001:db8:0:8000::/49` produce `2001:db8::/48`. If the selected registry
contains all four reservations, human output is:

```text
10.0.0.0/24
2001:db8::/48
```

Each family aggregates independently, with IPv4 supernets first and address
order within each family. JSON carries the same order in `supernets`:
`["10.0.0.0/24", "2001:db8::/48"]` for the mixed example above. Registry
aggregation reads subnet pools through the local API;
it does not modify reservations or collect live routes. The registry
directory defaults to `.`, and the tailnet resolves from `--tailnet`, then
`TAILSCALE_TAILNET`, then `local`, matching the other registry commands.

`--from-registry` cannot be combined with positional CIDRs. Explicit
`--registry-dir` and `--tailnet` flags require registry mode. An existing
registry with no subnet reservations succeeds with empty human output or
`"supernets": []` in JSON. A missing or invalid registry is an error (exit 2),
with no result on stdout. Registry JSON includes the selected `tailnet`;
registry-derived supernets and the tailnet are redacted when sanitization
is active. Positional aggregation retains its unredacted output.

#### `calc next-free`

Finds the first available IPv4 or IPv6 child CIDR of a pool after checking both the
local subnet-pool registry and every route advertised by live tailnet devices.
Unlike the other `calc` commands, this read-only planning command needs the
registry and Tailscale API credentials used by `devices list`; it never writes
to the registry. It searches conflicting CIDR intervals directly, so finding a
free child does not enumerate every possible child of a large pool.

```bash
tail-ipam calc next-free 10.20.0.0/16 --size 24 \
  --registry-dir /etc/tail-ipam --tailnet production
tail-ipam subnet reserve "$(tail-ipam calc next-free 10.20.0.0/16 --size 24)" branch-office
tail-ipam calc next-free 2001:db8::/32 --size 48 --count 3 \
  --registry-dir /etc/tail-ipam --tailnet production
tail-ipam subnet reserve "$(tail-ipam calc next-free 2001:db8::/48 --size 64)" branch-office
```

One result is exactly one bare CIDR followed by a newline, which makes the
second command safe to compose through shell substitution. Use `--count` (or
`-n`) for the first several suggestions, or `--all` to print every available
child; multiple results are one CIDR per line in address order. JSON output
carries the same suggestions as the `free_blocks` array in the versioned
envelope.

Only overlapping reservations and routes from the pool's address family
exclude candidates; a mixed IPv4/IPv6 registry supports either search.
Target sizes range from the pool's prefix length through `/32` for IPv4 or
`/128` for IPv6, including `/127` and `/128` pools. For large IPv6 pools, use
the default single result or `--count`: `--all` still produces every free
child and can be impractical even though the search skips occupied ranges.

If the pool is exhausted, the command succeeds with a message that no free
block exists (and an empty `free_blocks` JSON array). IPv4-mapped prefixes of
`/96` or longer are normalized to their plain IPv4 equivalent before searching,
so `--size` uses the IPv4 prefix length. Shorter mapped-looking prefixes remain
native IPv6. Host bits in pool inputs are masked before searching.

#### `calc info`

Stateless IPv4 and IPv6 block facts: the network address, usable host range,
and usable-host count for a CIDR — classic ipcalc output. IPv4 results also
include a broadcast address where that concept applies. Like `calc split`, it
needs no registry, network I/O, or Tailscale API credentials. Host bits in the
argument are masked off first, so an address inside a block reports the facts
for its containing block. Human output is one tab-separated `fact` and `value`
pair per line:

```bash
tail-ipam calc info 192.168.1.0/24
# prefix        192.168.1.0/24
# network       192.168.1.0
# broadcast     192.168.1.255
# first_usable  192.168.1.1
# last_usable   192.168.1.254
# usable_hosts  254
```

`/31` and `/32` blocks report correctly instead of erroring or showing an
inverted range: a `/31` is an RFC 3021 point-to-point link — no broadcast
address, both addresses usable — and a `/32` is a host route whose single
address is the whole range. The broadcast fact is omitted entirely for
those sizes, in both formats, rather than rendered empty:

```bash
tail-ipam calc info 10.0.0.0/31
# prefix        10.0.0.0/31
# network       10.0.0.0
# first_usable  10.0.0.0
# last_usable   10.0.0.1
# usable_hosts  2
```

IPv6 has no broadcast address, so `broadcast` is omitted entirely in both
formats. Every address in an IPv6 block is usable, including `/127` and
`/128`; for example:

```bash
tail-ipam calc info 2001:db8:1::42/64
# prefix        2001:db8:1::/64
# network       2001:db8:1::
# first_usable  2001:db8:1::
# last_usable   2001:db8:1:0:ffff:ffff:ffff:ffff
# usable_hosts  18446744073709551616
```

`-format json` carries the facts as a `block_info` object in the versioned
envelope. `usable_hosts` is an exact decimal **string**, not a JSON number.
No scientific or `2^k` notation cutoff is used: even the full IPv6 address
space (`2^128`) is only 39 decimal digits, so every count is precise and
uniformly parseable. Use `jq -r '.block_info.usable_hosts | tonumber'` where
a number is required.

IPv4-mapped (4-in-6) prefixes of `/96` or longer are normalized to plain
IPv4 first, exactly as `calc split` does, so
`tail-ipam calc info '::ffff:10.0.0.0/120'` reports the facts for
`10.0.0.0/24`. Shorter 4-in-6-like prefixes cannot be exclusively IPv4-mapped
and are treated as native IPv6.

### Registry format

The registry is a comment-preserving HuJSON file named
`<tailnet>.ipam.hujson` under `--registry-dir` (default `.`). The routing/storage
key resolves from `--tailnet`, then `TAILSCALE_TAILNET`, then `local`; it may be
an operator-defined alias rather than the live tailnet name. The structure is
straightforward:

```hujson
{
  // Site ID allocations
  "siteIDs": {
    "1": "hq-vpc",
    "2": {
      "site": "branch-office",
      "owner": "platform-team",
      "ticket": "OPS-1234",
      "reservedAt": "2026-01-15T09:00:00Z",
    },
  },

  // Subnet CIDR pool reservations
  "subnetPools": {
    "10.20.0.0/16": "hq-vpc",
    "2001:db8:20::/48": "branch-office",
  },

  // Per-service IPv4 host reservations
  "serviceAddresses": {
    "100.100.100.8": "svc:dashboard",
    "100.100.100.9": {
      "service": "svc:metrics",
      "owner": "network-team",
      "ticket": "NET-237",
      "reservedAt": "2026-08-31T12:00:00Z",
    },
  },

  // Per-device UDP listen ports, grouped by firewall boundary
  "udpPorts": {
    "branch-office": {
      "41642": "branch-fw01",
      "41643": "branch-fw02",
    },
    "hq-vpc": {
      // Cross-site reuse is valid.
      "41642": "hq-fw01",
    },
  },
}
```

Every write goes through `policycore/hujsonedit`, which always reformats the
whole file (tab indentation, trailing commas on an expanded object) rather
than preserving the original file's own style. The example above matches that
shape (comment preserved, trailing comma on an expanded block) except for the
indentation character: it is shown here with spaces because this repository's
markdown lint config forbids hard tabs in a fenced code block, but the tool
itself always writes tabs.

An entry in `siteIDs`, `subnetPools`, or `serviceAddresses` is either a bare
string (the reservation target, and the only shape `reserve` writes when no
metadata flags are given) or an object carrying that target plus whatever
optional `owner`, `description`, `ticket`, and `reservedAt` fields were
attached. Site-ID and subnet-pool objects use `site`; service-address objects
use `service`. A registry with only bare-string entries loads and re-saves
byte-for-byte unchanged; the two shapes freely coexist in one map, as `"1"`
and `"2"` do above. UDP-port device leaves are always strings and do not carry
metadata.

Site IDs are canonicalized as decimal integers from 0 to 65535 (Tailscale's
16-bit site-ID field): `"01"` and `"1"` map to the same key. This is a
deliberate, documented difference from `tailscale debug via`'s hex/octal-aware
site-ID parsing: a leading-zero key such as `"017"` is always read as decimal
17 here, never as octal. The registry file is created on first `reserve` if
absent; `release` and `list` error if the file does not exist.

Subnet-pool keys must be canonical CIDR prefixes. Registry load validates the
whole `subnetPools` map and refuses malformed or non-canonical keys, duplicate
members, empty site names, and overlaps owned by different sites. CIDR overlap
semantics come from the shared `policycore/addrspace` primitive used by the
repository's policy and audit tools rather than from a registry-specific
comparison.

Service-address keys are canonicalized from bare IPv4 hosts or exact `/32`
prefixes to bare IPv4 addresses. Registry load rejects wider prefixes, native
or mapped IPv6, duplicate aliases that resolve to the same address, and
invalid service identities. A bare service value is canonicalized with one
`svc:` prefix. Collision checks are exact-address checks only; they do not
cross-reference subnet pools or live service state.

UDP-port keys are canonical decimal ports from 1024 through 65535. The nested
site object scopes uniqueness to one firewall boundary, so identical port
keys may appear under different sites. Registry load rejects duplicate
canonical site or port aliases, invalid labels, and non-string device leaves.
Releasing a site's final port retains the empty site object so comments on the
site itself are not discarded.

Validation failures are isolated to their top-level allocation map: malformed
UDP-port data blocks UDP-port operations without preventing valid site-ID,
subnet-pool, or service-address operations, and a malformed existing map does
not prevent valid UDP-port operations. Every operation still refuses malformed
data in the map it is about to read or change.

A comment attached directly to a released entry (i.e. immediately above it)
is removed along with that entry — this is standard `hujson` behavior, not a
tail-ipam bug. Comments elsewhere in the file, including ones attached to
other entries, are preserved.

### Machine-readable and diagnostic output

Every successful command supports `-format human|json`. JSON uses the same
top-level contract as the repository's other action-oriented CLI output:

```json
{
  "schema_version": "1",
  "tool": "tail-ipam",
  "command": "site-id reserve",
  "status": "ok",
  "message": "reserved site ID 1 for \"hq-vpc\"",
  "tailnet": "local",
  "changed": true,
  "id": "1",
  "site": "hq-vpc"
}
```

`site-id list` carries `site_ids`, `subnet list` carries `subnet_pools`,
`service list` carries `service_addresses`, `port list` carries `udp_ports`,
`devices list` carries `devices`, `plan`/`check` carry `comparison`, and
`check-policy` carries `policy_comparison`.
Reserve/release results carry the
applicable `changed`, `id`, `cidr`, `address`, `site`, `service`, `port`, and
`device` fields, plus `owner`, `description`, `ticket`, and `reserved_at` when
the reservation shape supports and carries that metadata (a reserve echoes
back what was actually written, including any auto-stamped `reserved_at`).
Empty list results remain JSON arrays. Treat field names and meanings as a
compatibility surface; `schema_version` changes before a breaking contract
change.

Run `tail-ipam -schema` for the authoritative machine-readable command, flag,
and exit-code catalog. Run `tail-ipam --help` for examples and the human-facing
exit-code description. `tail-ipam --version` includes the embedded VCS revision
and marks a modified working-tree build as dirty.

`-verbose` enables structured `slog` diagnostics for collection, loopback
transport, registry loading/creation, and lock acquisition. Diagnostics stay
on stderr, so stdout remains parse-clean under `-format json`.

`TAILSCALE_SANITIZE=identity` or `strict` replaces device names, addresses,
routes, tailnet routing keys, site names, service identities, reservation
owners, reserved subnet CIDRs, reserved service addresses, and UDP-port site
and device labels after the real operation has completed. UDP port numbers
remain visible. Collision errors receive the same treatment on stderr,
including identity labels whose quoted representation contains escapes.
`description`, `ticket`, and `reserved_at` are left unredacted: they are
free-form operator text and a timestamp, not tailnet-derived identities the
sanitizer's exact-token replacement can meaningfully sweep. A non-`off`
environment value overrides `--sanitize` and is announced on stderr. `calc
split`, positional `calc aggregate`, and `calc info` output is not sanitized
because those modes perform stateless arithmetic over CLI-supplied CIDRs.
`calc aggregate --from-registry` and `calc next-free` reflect registry or
live-route data, so their derived CIDRs are sanitized when sanitization is
active. Placeholders for registry and inventory output are
scoped to the artifact's routing/storage key (the `--tailnet` alias for
registry commands, or the live tailnet from `TAILSCALE_TAILNET` for `devices`
commands), so passing an explicit `--tailnet` alias to a registry command
intentionally creates a separate pseudonym domain from `devices list` output
for the same real tailnet. Set `TAILSCALE_SANITIZE_KEY` to exactly 64
hexadecimal characters for stable tailnet-scoped placeholders; without it
placeholders are artifact-scoped.

### Not yet implemented

The following commands and features are planned future work per
[`../ROADMAP.md`](../ROADMAP.md) ("Future Vision" → `tail-ipam`):

- `allocate` command for address-space planning against live state
- On-prem CIDR planning (address space not yet advertised as live routes)

## Build

```sh
go build -o tail-ipam ./cmd/tail-ipam
```

`make build` at the monorepo root builds all tool binaries into `bin/`.

Releases are not discovery-based: this module is registered in
`release-please-config.json`, `.release-please-manifest.json`, and the release
workflow's tag outputs, and ships its own [`.goreleaser.yaml`](./.goreleaser.yaml).
Merging its release-please PR cuts the `tail-ipam/vX.Y.Z` tag and attaches the
archives; registration alone does not cut the initial release.

## License

Apache-2.0; see this module's [LICENSE](./LICENSE) and [`NOTICE`](./NOTICE) — the
latter carries the full third-party dependency inventory for the release binaries.
