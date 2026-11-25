# Design: Live sources beyond the tailnet

## Context

See `proposal.md` for motivation. This builds on `add-address-spaces`: named
spaces, `Store` methods that take a space, and `planner` comparing one space with
a list of prefixes.

- `internal/devices` collects the tailnet. `Service.CollectLive` is injected in
  `cmd/tsipam/target.go`, the only file where commands may reach server packages.
- `planner.Compare` fails on malformed live data instead of skipping it, and any
  collection failure discards the whole report.
- For `subnet allocate` the CLI reads devices and sends the routes. For
  `calc next-free` it composes a list and a device read itself.
- `internal/devices/read.go` is the pattern for a bounded, traced read whose errors
  carry no response body.

## Goals / Non-Goals

**Goals:**

- A seam small enough that each roadmap source is one package.
- The same fail-closed rules the tailnet comparison has.

**Non-Goals:**

- Out-of-process plugins, and writes to any network. Plugins come first, then a
  decision on writes.
- Azure, GCP, DNS, DHCP, router, and IPAM sources. D2 only checks that they fit.
- AWS VPC IPAM pools, Transit Gateway routes, and several accounts through one
  source. A second source with another `profile` covers a second account.
- Finding overlaps between live networks. As with `tailnet-audit`, live-only
  collisions are not `tsipam`'s job.
- Importing live prefixes as reservations.

## Decisions

### D1. The tailnet stays outside the source interface

```go
type Source interface {
    Type() string
    Collect(ctx context.Context, settings map[string]string) ([]Observation, error)
}

type Observation struct {
    Prefix   netip.Prefix
    Resource string
}
```

The tailnet produces routes, site IDs, and Service identities, feeds three kinds of
row, and attributes via routes to spaces. Forcing it through `Observation` would
widen the interface for one implementation. It keeps `devices.Snapshot`. Sources
produce prefixes and nothing else.

Settings are validated by data, not by the source: a table in the shared
`internal/registry` package lists each type's settings, which are required, and the
values or pattern each allows. `Ledger` and every store use it, so `source add`,
loading, and listing agree, and an invalid declaration makes the space map invalid
everywhere. That also keeps the AWS SDK out of the shared package. Implementations
live in a compile-time table in `internal/sources`, and a test requires the two
tables to name the same types. Nothing is loaded at run time.

An older binary therefore rejects a type that it does not know, and commands that
read the space map fail closed until it is upgraded.

### D2. The seam fits the roadmap

| Source | Prefix | Resource |
| --- | --- | --- |
| Cloud VPCs | VPC or subnet block | VPC or subnet ID |
| DNS and DHCP | Scope block, or a /32 or /128 lease or record | Scope or record name |
| Routers and firewalls | Interface subnet or route | Device and interface |
| IPAM systems | Prefix | Prefix object ID |

Host-level evidence works, because a pool matches any contained prefix. A DHCP
source with many leases may want aggregation first. That is its own concern.

### D3. Declarations live on the space

```hujson
"prod": {
  "sources": {
    "use1": {"type": "aws-vpc", "settings": {"region": "us-east-1"}},
  },
},
```

*Alternative:* select sources by flag on each run. CI would have to repeat them, and
a forgotten flag would look like a clean check. Declaring them makes omission
visible: `--only` discloses in `excluded`.

`--only` selects a space, not one source. With part of a space's sources read, a
missing prefix proves nothing, so `reserved_but_unadvertised` would be wrong.

### D4. Rows are per origin, and sources within a space are a union

Pools in one space are usually split among its sources (a region each), so
comparing pool by source would always drift. Within a space the sources form one
origin. The tailnet is a separate origin, because "in use in AWS" and "routed on the
tailnet" are separate facts: a union would hide a subnet that nobody routes.

The state names are unchanged. `reserved_but_unadvertised` on a `sources` row means
no source reports it. A new name would be more exact, but consumers already switch on
these four, and `origin` says which kind of evidence is missing.

### D5. The service collects, outside the lock

`Service.CollectSources` is injected next to `CollectLive`. For allocation the
service reads the declarations in a `View`, collects with no lock held, then opens
the `Update` and refuses if the declarations or the mapping differ from what it
read. `calc next-free` uses a new read-only method, `list_space_observations`.

Putting collection in the service, not the CLI, means `add-serve-tsnet` needs no
second path. It also means a shared server reads with its own credentials, so that
change must gate `add_source` and `remove_source` behind `admin`.

Sources are collected concurrently, at most four at a time. A failure does not cancel
the others, because the reported failure is the first by space and name, which is
known only when all have finished. Each is bounded by its own timeout.

### D6. `aws-vpc` uses the AWS SDK

`aws-sdk-go-v2` `config` and `service/ec2`, called through a two-method interface
holding `DescribeVpcs` and `DescribeSubnets`. That makes the read-only rule a
compile-time fact for this source, and gives tests a fake.

*Alternative:* sign and call the query API directly. It avoids about 4 MB of binary
but reimplements credential resolution (SSO, IMDS, web identity), which is where
the risk is.

Failures become a typed `sources.CollectionError` with a reason (`credentials`,
`denied`, `throttled`, `timeout`, `failed`) and the operation name. Only the AWS
error code is read from the response; its message can hold ARNs and account IDs.

VPC blocks are the default evidence because pools are usually planned per VPC. A VPC
block makes the whole range taken, so allocating inside one needs
`resource=subnet`.

### D7. API

| Method | Path | Errors |
| --- | --- | --- |
| `list_sources` | `GET .../ipam/sources` | `invalid_input` |
| `add_source` | `POST .../ipam/spaces/{space}/sources` | `invalid_input`, `collision` |
| `remove_source` | `DELETE .../ipam/spaces/{space}/sources/{name}` | `invalid_input` |
| `list_space_observations` | `GET .../ipam/spaces/{space}/observations` | `invalid_input`, `source_credentials`, `source_failed` |

Go types do not cross the loopback, so each failure the CLI must tell apart is a goa
error. `plan` and `allocate_subnet_pool` also gain `source_credentials`, and
`allocate_subnet_pool` gains `source_failed`; for `plan` any other source failure is
the existing `comparison_failed`. The CLI maps `source_credentials` to
`credentials_missing` and `source_failed` to `collection_failed`, so no new
`error_code` is needed.

`plan` gains an optional `only` array. `ComparisonEntry` gains optional `origin`.

## Risks / Trade-offs

- [The same block in two VPCs of one space is grouped as one piece of evidence] →
  Document that overlapping VPCs belong in different spaces.
- [Default VPCs appear as `unreserved_but_live` on first use] → They are in use.
  The guide shows reserving the block, or narrowing with `vpc=`.
- [`check` now needs AWS credentials wherever a source is declared] → Intended and
  fail-closed. `--only tailnet` keeps a Tailscale-only job working, with disclosure.
- [IMDS lookup delays a machine with no credentials] → The 30-second bound covers
  credential resolution as well as the request.
- [SDK churn raises Renovate volume] → Group the AWS modules in `renovate.json`.
- [Trace spans could carry identities] → Reuse the attribute-free span pattern, and
  add no SDK instrumentation.

## Migration Plan

1. Merge after `add-address-spaces`, preferably before `add-serve-tsnet`.
2. No registry migration. Declaring a source is opt-in per space.
3. Rollback: an older binary that knows spaces but not sources keeps the `sources`
   map, because `add-address-spaces` patches single fields of a space. It reports
   those pools as `external` again, so remove the CI gate first.
