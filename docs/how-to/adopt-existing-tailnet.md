# Adopt tail-ipam for an existing tailnet

Use this guide when subnet routes, 4via6 site IDs, or Tailscale Services are
already live and you want a registry that records them, so that from now on
`check` catches anything that drifts. It uses `plan` to discover live
allocations and `check` to confirm the registry matches.

## Before you begin

Both commands read devices and Services through the Tailscale API. Provide an
API key or an OAuth client with read access to both:

```bash
export TAILSCALE_TAILNET=example.ts.net
export TAILSCALE_API_KEY=tskey-api-...
# or
export TAILSCALE_OAUTH_CLIENT_ID=...
export TAILSCALE_OAUTH_CLIENT_SECRET=...
```

`TAILSCALE_TAILNET` always names the live source. The registry file uses the
same key unless you pass `--tailnet` to choose an alias such as `production`.

Decide where the registry lives. This guide uses `/etc/tail-ipam`; any
directory you can commit to version control works.

## 1. Start from an empty registry

`plan` and `check` read an existing file rather than creating one:

```bash
mkdir -p /etc/tail-ipam
echo '{}' > /etc/tail-ipam/production.ipam.hujson
```

## 2. See what is live

```bash
tail-ipam plan --registry-dir /etc/tail-ipam --tailnet production
```

`plan` always exits `0` after a complete comparison. Each row names an
allocation kind, its state, the key, and the evidence:

```text
registry="production" live_tailnet="example.ts.net"
site_id      unreserved_but_live    7               observed=["router-hq"]   routes=["fd7a:115c:a1e0:b1a:0:7:a1e:0/120"]
subnet_pool  unreserved_but_live    10.30.0.0/24    observed=["router-hq"]   routes=["10.30.0.0/24"]
drift_count=2 excluded=udp_port (no live counterpart)
```

The states are:

| State | Meaning |
| --- | --- |
| `matched` | The reservation has matching live evidence. |
| `reserved_but_unadvertised` | Reserved, but nothing live matches. |
| `unreserved_but_live` | Live, but no reservation covers it. |
| `conflict` | A reserved service address is assigned to a different service, or to several. |

UDP listen ports are always excluded: the API does not expose them.

## 3. Reserve every live allocation

Reserve each `unreserved_but_live` row by hand, choosing the site label that
means something to your team, or script it from the JSON report with a
placeholder label. The key is what `reserve` takes; for services the observed
identity is the label:

```bash
tail-ipam plan --registry-dir /etc/tail-ipam --tailnet production --format json |
  jq -r '.comparison.entries[] | select(.state == "unreserved_but_live") |
         [.kind, .key, (.observed[0] // "")] | @tsv' |
  while IFS=$'\t' read -r kind key first; do
    case "$kind" in
      site_id)         tail-ipam site-id reserve "$key" unlabeled --registry-dir /etc/tail-ipam --tailnet production ;;
      subnet_pool)     tail-ipam subnet reserve "$key" unlabeled --registry-dir /etc/tail-ipam --tailnet production ;;
      service_address) tail-ipam service reserve "$key" "$first" --registry-dir /etc/tail-ipam --tailnet production ;;
    esac
  done
```

Replace `unlabeled` afterwards: reserving the same key for a different site
is refused as a collision, so `release` the key and `reserve` it again with
the real label.

A `/16` pool covers any live `/24` inside it. If you would rather record one
broad pool than many routes, reserve the pool and skip the routes it contains.

## 4. Confirm the registry matches

```bash
tail-ipam check --registry-dir /etc/tail-ipam --tailnet production
echo $?
# 0
```

`check` prints the same report as `plan` and exits `1` while any row is not
`matched`. Exit `2` means the comparison could not be completed: bad
credentials, an API failure, or an unreadable registry. There is no partial
report in that case.

## 5. Commit and keep it honest

Commit `production.ipam.hujson` beside your policy file. Once a label is
right, reserving the same key and site again amends only the metadata flags
you pass, so ownership can be filled in as you learn it:

```bash
tail-ipam subnet reserve 10.10.20.0/24 hq-vpc --owner network-team --ticket NET-42 \
  --registry-dir /etc/tail-ipam --tailnet production
```

Then run `check` on a schedule or in CI so the next unregistered route is
caught: [Gate address space in CI](gate-address-space-in-ci.md).

## What a match does not tell you

A `matched` row says the registry and the API agree on the allocation. It does
not say the route is approved, reachable, or healthy, and the two API reads
are consecutive observations, not one atomic snapshot. Overlap and 4via6
collision analysis across live routes stays with `tailnet-audit`; see
[Registry and live state](../explanation/registry-and-live-state.md).

## Sharing the report

Add `--sanitize identity` (or `strict`) to redact the registry alias, tailnet,
site labels, devices, addresses, and routes. Counts and states stay visible.
See [Redact output for sharing](redact-output-for-sharing.md).
