# Reserve your first address space

This tutorial builds a small `tsipam` registry from nothing, shows it
refusing a conflicting reservation, and ends with an offline policy check that
passes. Everything runs locally; no Tailscale credentials are needed.

## Before you begin

Download the `tsipam` archive for your platform from the
[release page](https://github.com/rshade/tsipam/releases) (tag
`vX.Y.Z`) and put the binary on your `PATH`, or build it from
source:

```bash
go build -o tsipam ./cmd/tsipam
```

Work in an empty directory. The registry is a file named
`<tailnet>.ipam.hujson` in the current directory; with no `--tailnet` flag
and no `TAILSCALE_TAILNET` in the environment, the tailnet key is `local`.

```bash
mkdir ipam-tutorial && cd ipam-tutorial
unset TAILSCALE_TAILNET
```

## 1. Reserve a site ID

4via6 site IDs are the first thing two sites fight over. Reserve one:

```bash
tsipam site-id reserve 1 hq-vpc
# reserved site ID 1 for "hq-vpc"
```

That command created `local.ipam.hujson`. Every later command reads and
rewrites the same file.

## 2. Carve a pool and reserve a piece of it

`calc split` is stateless arithmetic. Use it to see the `/24` children of a
`/16` you intend to hand out:

```bash
tsipam calc split 10.20.0.0/16 --prefix 24 | head -3
# 10.20.0.0/24
# 10.20.1.0/24
# 10.20.2.0/24
```

Reserve the first one for the same site and record who owns it:

```bash
tsipam subnet reserve 10.20.0.0/24 hq-vpc --owner platform-team --ticket OPS-1
# reserved subnet CIDR 10.20.0.0/24 for "hq-vpc"
```

`--owner`, `--ticket`, and `--description` are optional. Setting any of them
stamps `reserved_at` with the current UTC time.

## 3. Watch a conflict get refused

Try to give a different site a subnet inside the one you just reserved:

```bash
tsipam subnet reserve 10.20.0.0/25 lab
# tsipam: subnet CIDR 10.20.0.0/25 overlaps 10.20.0.0/24 reserved for "hq-vpc", not "lab"
echo $?
# 1
```

Exit code `1` means a well-formed request was refused. The registry was not
changed. Compare that with exit code `2`, which means the request itself was
invalid or the registry could not be read; see
[Exit codes](../reference/exit-codes.md).

## 4. List what you have

Human output is one reservation per line, tab-separated:

```bash
tsipam subnet list
# 10.20.0.0/24    hq-vpc    owner=platform-team ticket=OPS-1 reserved=2026-09-10T21:23:48Z
```

Add `--format json` when another program will read the result:

```bash
tsipam subnet list --format json
```

```json
{
  "schema_version": "1",
  "tool": "tsipam",
  "command": "subnet list",
  "status": "ok",
  "message": "listed 1 subnet-pool reservation(s)",
  "tailnet": "local",
  "subnet_pools": [
    {
      "owner": "platform-team",
      "ticket": "OPS-1",
      "reserved_at": "2026-09-10T21:23:48Z",
      "cidr": "10.20.0.0/24",
      "site": "hq-vpc"
    }
  ]
}
```

The first six fields appear on every JSON result. The command-specific
payload (`subnet_pools` here) is documented in
[JSON envelope](../reference/json-envelope.md).

## 5. Look at the registry file

The registry is HuJSON, so it can carry comments, and it is meant to live in
version control next to your policy file:

```bash
cat local.ipam.hujson
```

```hujson
{"siteIDs": {
  "1": "hq-vpc"
}, "subnetPools": {
  "10.20.0.0/24": {
    "site":       "hq-vpc",
    "owner":      "platform-team",
    "ticket":     "OPS-1",
    "reservedAt": "2026-09-10T21:23:48Z"
  }
}}
```

The tool writes tabs; they are shown as spaces here. Add a comment above an
entry and run any `reserve` or `release`; the comment survives.
[Registry format](../reference/registry-format.md)
describes every key.

## 6. Check a policy against the registry

`check-policy` compares the ipsets a Tailscale policy file authors with the
subnet pools you reserved. Save this as `policy.hujson`:

```hujson
{
  "ipsets": {
    "ipset:hq":  ["10.20.0.0/25"],
    "ipset:lab": ["172.16.0.0/12"],
  },
}
```

```bash
tsipam check-policy policy.hujson
# registry="local" comparison=authored_ipsets
# authored    matched                    "10.20.0.0/25"   ipset="ipset:hq"   pools=["10.20.0.0/24"]
# authored    authored_but_unreserved    "172.16.0.0/12"  ipset="ipset:lab"  pools=[]
# reserved    matched                    "10.20.0.0/24"   site="hq-vpc"      ipsets=["ipset:hq"]
# drift_count=1
# tsipam: authored ipsets differ from reserved subnet pools
echo $?
# 1
```

`ipset:hq` matched because `10.20.0.0/25` sits inside the reserved
`10.20.0.0/24`. `ipset:lab` authored a range nobody reserved. Fix the drift by
reserving it, then run the check again:

```bash
tsipam subnet reserve 172.16.0.0/12 lab
tsipam check-policy policy.hujson
# ...
# drift_count=0
echo $?
# 0
```

Exit code `0` is the signal a CI job waits for.

## What you have

A registry that records intent, refuses overlaps, round-trips comments, and
can be checked against a policy file without network access.

## Next steps

- Compare the registry with what the tailnet is actually advertising:
  [Adopt tsipam for an existing tailnet](../how-to/adopt-existing-tailnet.md)
  needs API credentials and covers `plan` and `check`.
- Make the two checks block merges:
  [Gate address space in CI](../how-to/gate-address-space-in-ci.md).
- Commit `local.ipam.hujson` (or the file for your real tailnet key) with
  your policy so changes to both are reviewed together.
