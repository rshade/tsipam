# tail-ipam

Allocate and plan Tailscale address space: 4via6 site IDs, subnet CIDR pools
for sites, and per-service `/32` reservations for Tailscale Services. Starts
scoped to Tailscale-route/site-ID/service-VIP space; on-prem CIDR planning
(address space that has not yet been advertised as a live route) is an
explicit future-growth direction, not initial scope. See
[`../ROADMAP.md`](../ROADMAP.md) ("Future Vision" → `tail-ipam`) for the full
design rationale.

> **Module** `github.com/rshade/tailscale-utils/tail-ipam`. Conforms to the AX
> conventions in [`../AGENTS.md`](../AGENTS.md#ax-conventions-the-shared-standard):
> the command result will be written to **stdout** (human text; JSON output is
> planned future work); logs and errors go to **stderr**.

`tail-ipam` complements, and does not duplicate, two existing tools: `tail-acl`'s
`ipset`/`autoApprovers` are authoring-only (they label or edit ACL policy text,
with no allocation or conflict detection), and `tailnet-audit`'s
`subnet-route-overlap` ([#221](https://github.com/rshade/tailscale-utils/issues/221))
and `4via6-site-id-collision`
([#222](https://github.com/rshade/tailscale-utils/issues/222)) checks detect
collisions in what is already live. `tail-ipam` is the planning half: reserving
address space before it is ever advertised.

## Status

### Shipped

Four working commands manage the local address-space registry and inspect the
live tailnet:

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

Output format: tab-separated (device name, addresses, advertised routes).

#### `site-id reserve <id> <site-name>`

Reserves a 4via6 site ID in the local registry. Refuses with an error if the ID
is already reserved for a different site. Reserves the same ID and site name
again in a no-op (idempotent). Creates the registry file if it does not exist.

```bash
tail-ipam site-id reserve 1 hq-vpc
tail-ipam site-id reserve 2 branch-office
```

Override the registry file path with `--file`:

```bash
tail-ipam site-id reserve 1 hq-vpc --file /etc/tail-ipam.hujson
```

#### `site-id release <id>`

Releases a reserved site ID. Errors if the registry file does not exist.
Releases a never-reserved ID as a no-op (not an error).

```bash
tail-ipam site-id release 1
tail-ipam site-id release 2 --file /etc/tail-ipam.hujson
```

#### `site-id list`

Lists all reserved site IDs in the registry, sorted numerically by ID.
Errors if the registry file does not exist.

```bash
tail-ipam site-id list
tail-ipam site-id list --file /etc/tail-ipam.hujson
```

Output format: tab-separated (site ID, site name).

### Registry format

The registry is a comment-preserving HuJSON file (default path `tail-ipam.hujson`,
override with `--file <path>`). The structure is straightforward:

```hujson
{
  // Site ID allocations
  "siteIDs": {
    "1": "hq-vpc",
    "2": "branch-office",
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

Site IDs are canonicalized as decimal integers from 0 to 65535 (Tailscale's
16-bit site-ID field): `"01"` and `"1"` map to the same key. This is a
deliberate, documented difference from `tailscale debug via`'s hex/octal-aware
site-ID parsing: a leading-zero key such as `"017"` is always read as decimal
17 here, never as octal. The registry file is created on first `reserve` if
absent; `release` and `list` error if the file does not exist.

A comment attached directly to a released entry (i.e. immediately above it)
is removed along with that entry — this is standard `hujson` behavior, not a
tail-ipam bug. Comments elsewhere in the file, including ones attached to
other entries, are preserved.

### Not yet implemented

The following commands and features are planned future work per
[`../ROADMAP.md`](../ROADMAP.md) ("Future Vision" → `tail-ipam`):

- `allocate` and `plan` commands for address-space planning against live state
- `check` command for validating the registry against live tailnet usage
- Subnet CIDR pool allocation (beyond site IDs)
- Per-service `/32` reservations for Tailscale Services
- On-prem CIDR planning (address space not yet advertised as live routes)
- JSON output format (`-format json`)

## Build

```sh
go build -o tail-ipam ./cmd/tail-ipam
```

`make build` at the monorepo root builds all tool binaries into `bin/`.
