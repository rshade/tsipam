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
> command results are written to **stdout** as human text by default or a
> versioned JSON envelope with `-format json`; structured logs and errors go
> to **stderr**.

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

Human output is tab-separated (device name, addresses, advertised routes).
Use `-format json` for the versioned machine contract. Set
`TAILSCALE_SANITIZE=identity` or `strict` when the output must not expose
device names, addresses, routes, or the tailnet name.

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

Human output is tab-separated (site ID, site name). `-format json` emits the
same entries in the versioned envelope.

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

`site-id list` carries `site_ids`, `devices list` carries `devices`, and
reserve/release results carry the applicable `changed`, `id`, and `site`
fields. Empty list results remain JSON arrays. Treat field names and meanings
as a compatibility surface; `schema_version` changes before a breaking
contract change.

Run `tail-ipam -schema` for the authoritative machine-readable command, flag,
and exit-code catalog. Run `tail-ipam --help` for examples and the human-facing
exit-code description. `tail-ipam --version` includes the embedded VCS revision
and marks a modified working-tree build as dirty.

`-verbose` enables structured `slog` diagnostics for collection, loopback
transport, registry loading/creation, and lock acquisition. Diagnostics stay
on stderr, so stdout remains parse-clean under `-format json`.

`TAILSCALE_SANITIZE=identity` or `strict` replaces device names, addresses,
routes, tailnet routing keys, and site names after the real operation has
completed. A non-`off` environment value overrides `--sanitize` and is
announced on stderr. Placeholders are scoped to the artifact's routing/storage
key (the `--tailnet` alias for `site-id` commands, or the live tailnet from
`TAILSCALE_TAILNET` for `devices` commands), so passing an explicit `--tailnet`
alias to a `site-id` command intentionally creates a separate pseudonym domain
from `devices list` output for the same real tailnet. Set `TAILSCALE_SANITIZE_KEY`
to exactly 64 hexadecimal characters for stable tailnet-scoped placeholders;
without it placeholders are artifact-scoped.

### Not yet implemented

The following commands and features are planned future work per
[`../ROADMAP.md`](../ROADMAP.md) ("Future Vision" → `tail-ipam`):

- `allocate` and `plan` commands for address-space planning against live state
- `check` command for validating the registry against live tailnet usage
- Subnet CIDR pool allocation (beyond site IDs)
- Per-service `/32` reservations for Tailscale Services
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
