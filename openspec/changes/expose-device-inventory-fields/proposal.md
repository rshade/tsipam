# Proposal

## Why

`devices list` already requests every device field from the control plane and
then drops the ones operators need: tags, which advertised routes are enabled,
whether the node is authorized or ephemeral, and the user, OS, and hostname.
Lab scripts and policy questions then fall back to `curl` or `tailscale status`
for facts the inventory call already paid for (#377, #375).

## What Changes

- `devices list` JSON gains `tags`, `enabled_routes`, `authorized`,
  `ephemeral`, `user`, `os`, and `hostname` on every device. Existing
  `name`, `addresses`, and `advertised_routes` stay. Empty slices are `[]`.
- Human output keeps the current three columns and appends enabled routes,
  advertised-but-not-enabled routes, tags, user, OS, hostname, and the two
  flags.
- Optional `--tag` keeps only devices that carry that tag. A value without
  the `tag:` prefix is read as `tag:<value>`.
- Sanitization follows `tail-acl`: `user` and `hostname` are redacted at
  `identity` and `strict`; tags stay visible at `identity` (policy
  vocabulary) and are redacted at `strict`. `os`, `authorized`, and
  `ephemeral` are not identities. `strict` is no longer identical to
  `identity` for device tags only.
- `-schema`, the JSON envelope reference, and the devices README describe
  the new fields and the tag exception.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `devices`: the list payload, human columns, `--tag` filter, and which
  new fields sanitization redacts.
- `schema`: `devices list` documents `--tag`, and the sanitization text
  states the tag exception between `identity` and `strict`.

## Impact

- `tail-ipam` only. `internal/devices` copies the fields the v2 client
  already returns. The CLI envelope, sanitizer registration, schema golden,
  docs, and the live smoke assertion change.
- Additive JSON keys. No exit-code change. Lab script edits that could drop
  their `curl` / `docker exec` workarounds stay out of this change.
