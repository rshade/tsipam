# Tasks: add-ipv6-calc-aggregate

## 1. IPv6 calc-aggregate command

- [x] 1.1 Remove the IPv6 guard from `calc aggregate` and drop "IPv4" from
      its help summary while preserving mapped-IPv4 normalization; verify
      `cd tail-ipam && go test ./cmd/tail-ipam` passes.
- [x] 1.2 Replace the IPv6-rejection coverage with human-output cases for
      adjacent `/49`s, an unordered ULA set, an unmergeable IPv6 set, a mixed
      IPv4-and-IPv6 list, and a short mapped prefix, plus a JSON case that pins
      the mixed-family `supernets` order; verify the invalid-CIDR usage error
      still exits with the usage code.

## 2. Documentation and compatibility

- [x] 2.1 Update the README `calc aggregate` section with IPv6 and
      mixed-family examples and remove the follow-up link; verify
      `make lint-md`.
- [x] 2.2 Regenerate the command-schema golden with `go test ./... -update`
      and inspect that only the aggregate summary line changed; verify
      `cd tail-ipam && go test ./...` passes.

## 3. Repository-wide verification

- [x] 3.1 Validate the change artifacts with
      `cd tail-ipam && openspec validate add-ipv6-calc-aggregate` and run
      the repo-wide gates for the policycore blast radius:
      `make check-tools test build cross check-release snapshot-release lint`.
