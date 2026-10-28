# Tasks

## 1. Inventory copy

- [x] 1.1 Add the new fields to `devices.Device` and fill them in `List` and
  `CollectSnapshot`, with nil slices normalized to empty slices.
- [x] 1.2 Carry the same fields on `deviceRecord`, including the `UnhandledFields` list.

## 2. CLI output

- [x] 2.1 Map the fields in `collectDevices` and append the human columns without changing the first three.
- [x] 2.2 Add `--tag` on `devices list` and filter after collection.
- [x] 2.3 Redact `user` and `hostname` at identity and strict, redact tags only at
  strict, and redact `enabled_routes` like advertised routes.

## 3. Contract

- [x] 3.1 Extend the devices API fixture and tests for the new columns, JSON keys, the tag filter, and both sanitize levels.
- [x] 3.2 Update `-schema` sanitization text, regenerate `schema.golden.json`, and read the diff.
- [x] 3.3 Update the README, JSON envelope reference, redact guide, and
  `TestLiveDevicesList` so a live run asserts the keys and that identity
  hides user and hostname.

## 4. Verification

- [x] 4.1 Run `go test ./...` and `golangci-lint run ./...` in `tail-ipam` with `GOWORK=off`.
- [x] 4.2 Validate the OpenSpec change.
