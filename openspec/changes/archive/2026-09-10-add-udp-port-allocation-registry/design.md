# Design: Per-Device UDP Port Allocation Registry

## Context

See `proposal.md` for motivation and
`specs/udp-port-reservations/spec.md` for the behavioral contract. The current
registry has three independent top-level maps (`siteIDs`, `subnetPools`, and
`serviceAddresses`). Each map has a scoped loader error, a canonical Go view,
raw on-file key spellings, and an object-presence bit so a HuJSON patch can
preserve comments and siblings. `reservePatchOp` handles that state for a
one-level map; UDP ports add a second object level, where the same distinctions
must also be made for each site.

Several repository changes since issue #240 was drafted shape the integration:

- Every registry primitive now traverses the generated goa HTTP client and
  ephemeral loopback server.
- The root command now owns a versioned human/JSON output envelope and
  sanitizer reflection guards. Port commands must integrate with that existing
  contract even though creating output-mode infrastructure remains out of
  scope.
- Reservation metadata exists for the three current primitive value types.
  Issue #240 deliberately specifies the new on-disk leaf as a device string and
  no metadata flags; v1 keeps that smaller contract rather than silently
  extending the requested schema.
- Site names are free-form after trim/control-character validation. They can
  contain characters such as `/`, so a site must not be made a URL path
  segment unless its grammar is narrowed, which this change must not do.

## Goals / Non-Goals

**Goals:**

- Add one independently validated UDP-port map without coupling its health or
  lifecycle to the other registry maps.
- Make every nested HuJSON edit target the exact raw spelling already present
  on disk and preserve both levels of comments and siblings.
- Keep collision and idempotency decisions inside one locked
  read-decide-write transaction.
- Extend the generated API, CLI, output, sanitization, and schema surfaces in
  the same shape as current registry commands.

**Non-Goals:**

- Generalizing all registry maps behind a recursive generic abstraction; the
  nested UDP shape has materially different state and patch decisions.
- Adding reservation metadata to UDP-port leaves in v1.
- Validating site membership, device existence, or the device's live
  `tailscaled` listen port.
- Port-range allocation or next-free search.

## Decisions

### 1. Use a canonical nested view plus raw-key state at both levels

Add `udpPortsKey = "udpPorts"` and model the resolved data as
`map[string]map[string]string`, keyed first by canonical site and then by
canonical port, with the device label as the leaf. Expose list results through
a flat `UDPPortEntry{Site, Port, Device}` because it is the natural API and
sorting shape.

`Registry` gains UDP-specific state alongside the existing map state:

- the canonical nested view;
- raw site spelling by canonical site;
- raw port spelling by canonical site and canonical port;
- top-level object presence;
- child-object presence by canonical site; and
- one scoped UDP-map validation error.

`loadUDPPorts` resolves the top-level member through the existing exact-key
logic, then walks its HuJSON object rather than relying only on
`encoding/json`. Walking the syntax tree is required to detect duplicate site
and port members before a last-wins map decode hides them. It canonicalizes
each raw site with `NormalizeSiteName`, each raw port with `NormalizePort`, and
each device leaf with a new `NormalizeDeviceName` that trims, rejects empty
values and control characters, but applies no hostname or live-state grammar.
Canonical duplicates such as `"branch"`/`" branch "` or
`"41642"`/`"041642"` are rejected as ambiguous.

Top-level null and a site's null value are accepted as replaceable absence,
matching current top-level registry behavior. A site's non-null, non-object
value is malformed. That distinction lets reserve safely replace a null leaf
without treating arbitrary stored data as an empty map.

Alternative: decode directly into `map[string]map[string]string`. Rejected
because it loses duplicate members and cannot retain the raw spellings or
presence states needed for exact comment-preserving patches.

### 2. Normalize ports as strings at the registry boundary

`NormalizePort(string) (string, error)` first verifies that every input rune is
an ASCII decimal digit, then parses and range-checks 1024-65535 and formats the
result in base 10. This rejects signs and format variants other than leading
zeroes while keeping canonical port keys consistent with `NormalizeSiteID`.
Goa payloads also carry ports as strings so every API and CLI caller reaches
the same normalization and error classification rather than splitting the
rules between generated integer decoding and the registry.

`NormalizeDeviceName` follows `NormalizeSiteName`'s trim/non-empty/control
rules but remains a distinct function so errors identify the device field and
future device-specific policy does not alter site behavior.

Alternative: use `uint16` throughout. Rejected because privileged ports still
need a separate minimum check, input aliases and error messages would differ
between transports, and canonical raw-key tracking is string based.

### 3. Give the nested map a dedicated reserve patch decision

`reservePatchOp` remains unchanged for one-level maps. A UDP-specific helper
selects one HuJSON add operation from the loaded state:

| Stored state | Add target | Added value |
| --- | --- | --- |
| `udpPorts` absent or null | `udpPorts` | `{site: {port: device}}` |
| `udpPorts` object; site absent | `udpPorts/site` | `{port: device}` |
| site present as null | `udpPorts/raw-site` | `{port: device}` |
| site present as an object | `udpPorts/raw-site/raw-port` | `device` |

Here `raw-site` and `raw-port` are the exact spellings recorded at load time
when an existing canonical member is targeted. The helper does not infer
presence from a nil or empty Go map. In particular, reserving into a commented
empty object adds a child instead of replacing that object node.

`ReserveUDPPortWithLogger` normalizes all inputs before taking the lock, then
uses `withRegistryLock`, `loadOrCreate`, same-site collision comparison, the
nested patch helper, and `hujsonedit.WritePatch`. An identical device leaf
returns `changed=false` without writing. Different sites consult different
child maps, so cross-site reuse needs no special-case exception.

`ReleaseUDPPortWithLogger` performs its own locked load and removes exactly
`udpPorts/raw-site/raw-port`. An absent site or port returns unchanged without
writing; a missing file remains an error. It deliberately leaves an empty site
object behind, preserving comments attached to that node and avoiding a second
mutation with unclear comment ownership.

Alternative: rebuild and marshal the complete nested map. Rejected because it
would reorder members and destroy HuJSON comments and formatting. Alternative:
remove the site after its last port. Rejected because deleting the parent can
discard site-level comments and is unnecessary for correctness.

### 4. Keep UDP-map validation isolated

Extend `loadBytes`, `mapAccessorFor`, `mapError`, `loadOrCreate` diagnostics,
and package documentation with the fourth map. A malformed UDP map populates
only `udpPortsErr`; all other canonical maps remain available. Conversely,
UDP operations consult only the UDP error and therefore proceed when another
map is malformed.

The existing generic `releaseRegistryEntry` is not stretched across the
nested path. Its membership and repair model assumes a single canonical key
under one top-level object. A dedicated UDP release is shorter and safer than
making every existing caller provide a synthetic compound key.

For error sanitization inventory, add a best-effort nested raw extractor (for
example `RawUDPPorts`) rather than making validated `Load` the source. It must
return raw site keys and any string device leaves even when the UDP map itself
is invalid, so stored identities can be registered before rendering a load or
collision error. Keeping it separate avoids changing the existing
three-result `RawEntries` API and its tests.

### 5. Use one collection endpoint with site as data, not a path segment

Add a goa `UDPPortReservation` result type with required string `site`, `port`,
and `device` attributes, then add:

- `GET /tailnet/{tailnet}/ipam/udp-ports` for list, with optional `site` query
  parameter;
- `POST /tailnet/{tailnet}/ipam/udp-ports` for reserve, with required
  `site`, `port`, and `device` payload fields; and
- `DELETE /tailnet/{tailnet}/ipam/udp-ports` for release, with required `site`
  and `port` query parameters.

Reserve returns canonical site/port/device plus `changed`; release returns
canonical site/port plus `changed`. Reserve declares `collision` and
`invalid_input`; release and filtered list declare `invalid_input`. The
service translates `UDPPortCollisionError` to the shared generated collision
error and `InvalidInputError` to bad request exactly as existing methods do.

Alternative: `/tailnet/{tailnet}/ipam/sites/{site}/udp-ports`. Rejected because
`NormalizeSiteName` intentionally permits `/` and other free-form label
characters. Encoding that value in a path either narrows a compatibility
contract or risks router-dependent slash handling. Query/body data preserves
the requested free-form site semantics.

### 6. Add a dedicated `port` command group and leaf runner

Add `cmd/tail-ipam/udpport.go` with `reserve`, `release`, and `list` leaves.
Reserve takes exactly two positional arguments; release takes one; list takes
none. `--site` is required by Cobra for reserve/release and optional for list.
All leaves also receive the existing registry/tailnet flags and call the
generated client through `loopback.RunWithLogger`.

A small port leaf runner owns the shared setup and registers the site flag and
device positional argument with the output sanitizer before the generated
client can return a validation error. Device identities use the existing
`hostname` sanitizer domain so a registry device and the same live device name
receive a consistent placeholder within one tailnet scope. Port values remain
unsanitized.

Register the group in the root command. The existing schema walker discovers
it automatically, but the schema golden and command tests must be regenerated
or updated.

Alternative: call registry functions directly to avoid generated changes.
Rejected because it would make this the only registry primitive that bypasses
the service boundary and transport-level error behavior.

### 7. Integrate with the existing output and sanitization contract

Add `port` command constants, scalar `Port` and `Device` envelope fields, and a
`UDPPorts *[]udpPortOutput` list payload using JSON key `udp_ports`. Reserve and
release use post-sanitization message builders; list human rendering emits
`site<TAB>port<TAB>device`. A non-nil empty slice preserves the distinction
between an empty result and an inapplicable payload in JSON.

`sanitizeOutput` and `registerOutputIdentities` redact sites and devices while
copying port strings. Update every `sanitize.UnhandledFields` reflection guard
for the new fields and type. Add a format-anchored collision pattern for a
message shaped as “UDP port PORT in site SITE is already reserved for DEVICE,
not DEVICE”; its splice plan redacts the quoted site and device segments but
keeps the port visible. `exitCodeFor` recognizes the domain collision for
in-process callers, while the normal loopback path continues to recognize the
generated `collision` service error.

Although issue #240 originally deferred JSON work to #232, JSON is now a
root-level CLI contract. Adding the minimal `port`, `device`, and `udp_ports`
fields is therefore compatibility integration, not a second output-mode
project.

### 8. Regenerate goa output from the pinned tool

After editing `design/design.go`, run the pinned Goa 3.30.0 generator in the
module and retain all resulting `gen/**` changes, including OpenAPI documents.
Do not hand-edit generated files. Compile/service tests immediately after
generation catch interface drift before CLI work builds on it.

## Risks / Trade-offs

- [A whole-object add at either nesting level can erase siblings or comments]
  → Store explicit top-level and per-site presence state, centralize the patch
  matrix, and test absent/null/empty/populated combinations with comments.
- [Map decoding can hide duplicate members] → Walk HuJSON object members and
  reject raw and canonical duplicates at both nesting levels before mutation.
- [Free-form site labels are unsafe as URL path segments] → Carry site in a
  query parameter or request body and test labels requiring URL escaping.
- [A new identity value can leak through errors or JSON] → Extend the raw
  inventory, collision splice, output sanitizer, and reflection guards in the
  same change, with malicious quoted/control-input regression tests.
- [Two concurrent reservations could both decide a port is free] → Hold the
  existing cross-process lock across load, collision decision, and write, and
  add same-site concurrency coverage.
- [Leaving empty site objects accumulates harmless empty blocks] → Prefer
  comment preservation and deterministic no-op behavior; explicit cleanup can
  be a separately specified operation if needed.
- [Older binaries do not understand `udpPorts`] → Existing operations preserve
  unknown top-level members, so the new data remains dormant rather than being
  rewritten or removed.

## Migration Plan

This is an additive file-format and API change. Deploy the regenerated binary;
the first reservation creates `udpPorts` lazily, and existing registries need
no migration. Rollback consists of returning to the prior binary: it ignores
and preserves the unknown `udpPorts` member, while operators must stop using
the unavailable `port` commands. No destructive downgrade step is required.
