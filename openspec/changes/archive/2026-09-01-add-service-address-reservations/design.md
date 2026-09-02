# Design: add-service-address-reservations

## Context

See `proposal.md` for motivation and
`specs/service-address-reservations/spec.md` for the behavior contract. The
current implementation has two independent registry maps: `siteIDs` and
`subnetPools`. `Registry.Load` records each map's canonical entries, raw key
spellings, presence, and scoped validation error so one damaged map does not
disable commands for the other. All writes run under the existing per-file
lock and use `policycore/hujsonedit` patches.

Both current maps use `Entry`, whose required subject is specifically named
`Site` and whose metadata object is serialized with a `site` field. The shared
`mapAccessor` likewise assumes `map[string]Entry`; adding a service map with a
truthful `service` field therefore requires a small internal generalization,
not merely another switch case.

The public path is API-first: goa design → generated service/HTTP contract →
`internal/ipamsvc` → registry, and the Cobra commands use that generated client
through `internal/loopback`. The CLI's optional output fields, sanitization
rebuild, and collision-error redaction are guarded by reflection and
format-drift tests. The third reservation primitive must join every layer
without creating a registry-direct shortcut or an identity leak.

## Goals / Non-Goals

**Goals:**

- Add a third independently validatable, comment-preserving registry map whose
  on-disk vocabulary says `service`, not `site`.
- Keep address and service-name canonicalization in the registry domain layer
  so CLI, loopback API, and any later remote API consumer resolve the same
  input identically.
- Reuse the established lock, metadata merge/timestamp, HuJSON patch, output,
  sanitization, and exit-code contracts.
- Make the generated goa contract the only path used by the new CLI commands.

**Non-Goals:**

- Parent-pool schema, next-free search, pool exhaustion, address-range policy,
  or subnet-pool membership checks.
- Live service lookup, service creation, ACL selector editing, or #238's
  registry-versus-control-plane `plan` / `check` work.
- Cross-map collision rules. A service address inside a site's subnet pool is
  not inherently a conflict, and this change has no model that could decide
  otherwise.
- One-address-per-service enforcement. The service API represents addresses as
  a collection, and the issue only makes an address uniquely owned.
- Changes to the existing `siteIDs` or `subnetPools` on-disk value shapes.

## Decisions

### 1. Normalize to a bare IPv4 address and canonical `svc:` identity

Add `NormalizeServiceAddress` and `NormalizeServiceName` in the registry
package. Address normalization trims input and then:

1. accepts `netip.ParseAddr` only when the parsed value is genuine IPv4 (not
   `Is4In6`), or
2. accepts `netip.ParsePrefix` only when the address is genuine IPv4 and
   `Bits() == 32`.

Both paths return `Addr().String()`. Native IPv6, IPv4-mapped IPv6, any prefix
other than `/32`, zones, and malformed text return a typed validation error.
This orders the checks so `100.100.100.7/24` reports "requires an IPv4 host or
exact /32" instead of ever reaching `Masked()`.

Service normalization trims the argument, accepts either `payments` or
`svc:payments`, rejects `svc:`, doubled leading `svc:` prefixes, and control
characters, and returns `svc:payments`. It deliberately does not copy a
Tailscale name regex: there is no live lookup in this issue, and inventing a
local grammar would let it drift from the control plane. The exact prefix
normalization matches the selector identity `tail-acl` already consumes.

Alternative — require only literal `/32` input: rejected because the accepted
command shape calls the argument `<addr>` and the service API exposes bare
addresses. Alternative — accept only bare addresses: rejected because the
issue explicitly frames these as `/32` allocations and asks to validate an
exact single-host prefix. Accepting both and returning one representation
keeps the contract unambiguous.

### 2. Add `ServiceEntry`; do not rename or overload `Entry.Site`

The logical map is:

```hujson
{
  "serviceAddresses": {
    "100.100.100.7": "svc:payments",
    "100.100.100.8": {
      "service": "svc:search",
      "owner": "platform",
      "ticket": "NET-237",
      "reservedAt": "2026-08-31T12:00:00Z",
    },
  },
}
```

Introduce `ServiceEntry` with `Service` plus the existing embedded
`ReservationMetadata`, and marshal/unmarshal it as a string-or-object exactly
like `Entry` except that the object field is `service`. Extract a shared
metadata resolution helper from `resolveReservationEntry`; site/subnet and
service wrappers then construct their own concrete entry type without
duplicating merge or timestamp behavior.

`Registry` gains `ServiceAddresses`, `serviceAddressRawKeys`,
`serviceAddressesPresent`, and `serviceAddressesErr`, populated by an
independent `loadServiceAddresses`. New writes always use bare address keys.
Load accepts an existing exact `/32` raw key, records it in the raw-key map,
and targets that spelling on update/release, so a hand-authored entry and its
comment are not replaced merely to canonicalize text. Bare and `/32` forms of
the same address in one map are rejected as a canonical duplicate.

Alternative — change `Entry.Site` to a generic `Subject` and dynamically emit
`site` or `service`: rejected because it couples two stable on-disk shapes to a
tag-selection mechanism and turns an additive feature into a migration risk.
Alternative — store service names under an object field named `site`: rejected
because the registry is user-facing and would encode a false domain model.

### 3. Make map selection type-agnostic only where it needs to be

The existing `mapAccessor.entries map[string]Entry` is used for two generic
questions: the target map's scoped error and whether a canonical key exists
during release. Replace the typed entries field with a type-agnostic key
membership function (or equivalent key set), while retaining `rawKeys` and
`err`. `mapAccessorFor` remains the single exhaustive switch for all three
top-level maps; `mapError`, `loadOrCreate`, and the locked release helper keep
using it.

This is narrower than making the whole registry generic. Reserve and list need
their concrete entry fields and remain primitive-specific. The raw identity
inventory used before rendering errors also gains service-address keys and
values, including object-form values, so malformed registry errors can be
sanitized without exposing a service name.

Alternative — copy the release transaction for `ServiceEntry`: rejected
because the existing accessor comment was explicitly introduced to prevent a
third map from drifting across parallel switches.

### 4. A collision is canonical address equality, not a parent-pool overlap

After normalization, two IPv4 `/32` reservations overlap exactly when their
canonical address keys are equal. `ReserveServiceAddressWithLogger` checks that
single existing entry under the file lock:

- same service: resolve metadata and return an idempotent no-op when unchanged;
- different service: return `*ServiceAddressCollisionError` without patching;
- absent: add the concrete `ServiceEntry` with a HuJSON patch.

Multiple addresses for one service remain legal. No comparison is made with
`subnetPools`, and no containing pool is required. The roadmap's earlier note
about reusing `addrspace.Overlaps` was reasonable before #236 fixed the final
data shapes, but for two normalized `/32`s that helper reduces to equality and
adds parsing paths without adding behavior. Canonical equality is the smaller,
stronger invariant here; `addrspace.Overlaps` remains the right primitive for
variable-length subnet pools.

### 5. The goa API gets additive methods and an explicit invalid-input error

Add `ServiceAddressReservation` and three service methods:

- `list_service_addresses`: `GET /tailnet/{tailnet}/ipam/service-addresses`
- `reserve_service_address`: `POST /tailnet/{tailnet}/ipam/service-addresses`
- `release_service_address`: `DELETE /tailnet/{tailnet}/ipam/service-addresses`
  with `address` as a query parameter

The DELETE input is a query parameter because an accepted `/32` contains a
slash; placing raw input in a path segment would split the route before the
domain normalizer sees it. Results always return the canonical bare address
and canonical service. Reserve uses the existing named `collision` error →
HTTP 409. Reserve and release declare a named `invalid_input` error → HTTP 400;
`internal/ipamsvc` maps typed address/name/metadata validation failures to it,
while registry I/O and malformed stored state remain server failures. The CLI
maps `collision` to exit 1 and all invalid/unreadable cases to exit 2, matching
the current AX scheme.

Generated files are refreshed only with:

```text
goa gen github.com/rshade/tailscale-utils/tail-ipam/design
```

and are reviewed as source-of-truth output, not hand-edited.

Alternative — normalize in Cobra before invoking the generated client:
rejected because remote or non-Cobra consumers would observe a different API
contract, violating the API-first architecture.

### 6. CLI and output fields are service-specific and fully sanitized

Add `cmd/tail-ipam/service.go` with the same reserve/release/list structure,
registry flags, metadata flags, and loopback client path as `subnet.go`.
Extend `outputEnvelope` additively with `Address`, `Service`, and
`ServiceAddresses`; list entries carry `address`, `service`, and embedded
metadata. Human list output reuses `renderReservationLine` after renaming its
second parameter from the site-specific `site` to a neutral subject name.

The sanitization rebuild and identity pre-registration treat addresses as
`ip` and service identities as `service`; owner and tailnet keep their existing
kinds. Reserve/release messages are built from the already-sanitized envelope.
`ServiceAddressCollisionError` gets an anchored, capture-based redaction rule
for address, existing service, and requested service, plus a test that pins the
error format to the regex. This follows the existing collision protections and
avoids unsafe value-based replacement when identities contain quoting or
message-frame words.

The `sanitize.UnhandledFields` guard is updated for every new envelope/list
field. The schema command catalog learns the Cobra subtree automatically, but
its golden fixture must be regenerated and reviewed. The output schema version
does not change: the envelope additions are optional and command-specific,
matching the repository's precedent for additive `Subnets` and `BlockInfo`
payloads.

### 7. Preserve per-map isolation and existing concurrency behavior

`Load` always attempts all three independent map decodes and records a scoped
error for each. Reserve/list check only `serviceAddressesErr`; service release
may bypass semantic map errors only to the same extent the shared release path
allows repair, but it must still have enough canonical/raw-key data to target
the requested entry safely. Site-ID and subnet operations check only their
existing errors and therefore continue to operate if the new map is damaged.

All service reserve/release operations use `withRegistryLock`; no new locking
or temporary-file mechanism is introduced. The read-decide-patch span remains
atomic across all three primitive types because they share the same registry
file and lock suffix.

## Risks / Trade-offs

- [A syntactically valid address may not be usable by the operator's intended
  service range] → Document that this slice is an explicit ledger only; parent
  bounds and live checks remain separate designed work rather than hidden
  assumptions.
- [Accepting both bare and `/32` spellings creates raw-key aliases in hand-edited
  files] → Canonicalize the logical key, reject duplicate aliases, and retain
  the winning raw spelling for precise HuJSON patches.
- [A third entry type can duplicate metadata behavior] → Centralize metadata
  merge/stamp resolution and test identical site/subnet/service behavior.
- [Adding a registry map can accidentally make unrelated corruption global] →
  Keep three scoped errors and add bidirectional isolation tests.
- [Collision-error wording and its sanitizer can drift] → Pin the concrete
  error text against the anchored pattern and exercise adversarial service
  names through the real loopback CLI.
- [Generated output creates a broad mechanical diff] → Change the design first,
  regenerate once with the recorded command, and prohibit hand edits under
  `gen/`.
- [Older binaries do not understand `serviceAddresses`] → They already preserve
  unknown top-level keys during HuJSON patches; rollback loses command access,
  not stored data.

## Migration Plan

This is an additive registry and API change. Existing registry files require
no migration: absence of `serviceAddresses` means an empty service map, and the
first reserve adds it. Existing service maps hand-authored with exact `/32`
keys load through the alias rule; subsequent edits preserve their raw key until
that entry is deliberately changed or released.

Deployment consists of updating the goa design, regenerating the committed
client/server contract, implementing the registry/service/CLI layers, and then
shipping one binary. Rollback to an older binary leaves `serviceAddresses`
untouched as an unknown top-level member. Reapplying the new binary restores
access without conversion or data loss.
