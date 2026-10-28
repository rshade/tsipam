# Design

## Context

`devices.List` calls `Devices().List` with `IncludeFieldsAll`. The v2
`Device` value (module `tailscale.com/client/tailscale/v2` v2.12.0) already
carries `Tags`, `EnabledRoutes`, `Authorized`, `IsEphemeral`, `User`, `OS`,
and `Hostname`. The copy loop keeps `Name`, `Addresses`, and
`AdvertisedRoutes`. `deviceRecord` and `renderDevices` mirror that.

`tail-acl` leaves `tag:` selectors visible at `--sanitize identity` and
redacts them only at `strict` (`sanitizeOrdinarySelector`). `tail-ipam`
today documents `strict` as equivalent to `identity`. #377 and #375 disagree
on tags. This change follows the `tail-acl` rule and narrows the equivalence
sentence to every field except device tags.

Lab scripts that still `curl` for tags or `docker exec` for approved routes
are a follow-up. They read `advertised_routes` from JSON and must keep
working.

## Goals / Non-Goals

**Goals:**

- Additive device fields in JSON and appended human columns.
- `--tag` filter, including the `tag:` prefix default.
- Sanitization decisions the `UnhandledFields` guard will accept.
- Schema, README, and the JSON envelope reference match the output.

**Non-Goals:**

- Writing tags, routes, or authorization back to the control plane.
- Editing `lab/scripts`.
- Changing how `plan` / `check` decide which routes are taken. They keep
  using advertised routes.

## Decisions

1. **Copy the API fields in `devices.List` and in `CollectSnapshot`.**
   Snapshot callers ignore the new fields. Copying them keeps one `Device`
   shape. Alternative: CLI-only copy. Rejected because a later reader of
   `Snapshot.Devices` would see zero tags on a fully fetched device.

2. **JSON keys are always present.** No `omitempty` on the new fields.
   `nonNilStrings` covers `tags` and `enabled_routes`. Booleans use the
   API values (`IsEphemeral` → `ephemeral`).

3. **Human line stays prefix-compatible.** Columns one through three stay
   `name`, `%v` addresses, and `routes=%v` of the advertised list. Append
   `enabled=`, `pending=`, `tags=`, `user=`, `os=`, `hostname=`,
   `authorized=`, `ephemeral=`. `pending` is advertised routes absent from
   `enabled_routes`, in advertised order. `enabled` is the API list, so an
   enabled route that was not advertised still shows.

4. **`--tag` filters after collection.** Empty flag lists everyone. Trim
   space. If the value does not start with `tag:`, prepend it. `tag:` with
   an empty name is a usage error (exit 2). Zero matches is success and
   `listed 0 device(s)`.

5. **Sanitizer kinds.** `user` registers as `login`. `hostname` registers as
   `hostname` (same kind as the device name, so an identical literal shares
   one placeholder). Tags call `Value("tag", …)` only when the level is
   `strict`, so an identity `Text` sweep cannot redact them. `os` is copied
   through. Enabled routes use the existing `networkKind` helper.

6. **Schema text.** Replace the sentence that says `strict` is equivalent
   to `identity` with the tag exception. Other tail-ipam fields stay
   equivalent.

## Risks / Trade-offs

- Human lines get longer. Scripts that require the whole line to equal the
  old three columns will break. Scripts that read the first three
  tab-separated fields, and JSON consumers of the old keys, keep working.
- `strict` and `identity` diverge for one field. The schema text has to
  change in the same commit or `-schema` would lie.

## Migration Plan

Ship as one `feat(tail-ipam)` change. No registry migration. Rollback is
reverting the commit. OpenSpec archive waits until the PR merges.

## Open Questions

None. The tag rule is the `tail-acl` precedent the roadmap told this change
to apply.
