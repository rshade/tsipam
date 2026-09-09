# Tasks: add-ipv6-calc-info

## 1. IPv6 calc-info command

- [x] 1.1 Remove the IPv6 rejection translation and preserve mapped-IPv4
      normalization so native IPv6 reaches `ipcalc.Info`; verify
      `cd tail-ipam && go test ./cmd/tail-ipam` passes.
- [x] 1.2 Add human and JSON CLI coverage for `/64`, `/48`, `/127`, `/128`,
      exact decimal counts, masked input, and omitted broadcast; verify raw
      JSON contains no IPv6 `broadcast` key.

## 2. Documentation and compatibility

- [x] 2.1 Update help and README with IPv6 examples, no-broadcast semantics,
      and the no-cutover exact-decimal count rule; verify `make lint-md`.
- [x] 2.2 Regenerate and inspect the command-schema golden only if command
      help changes it; verify `cd tail-ipam && go test ./...` passes.

## 3. Repository-wide verification

- [x] 3.1 Validate artifacts and run repo-wide gates for the policycore blast
      radius: `make check-tools test build cross check-release snapshot-release lint`.
