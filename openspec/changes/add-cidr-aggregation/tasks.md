# CIDR Aggregation Tasks

## 1. Aggregate command

- [x] 1.1 Add the calc aggregate Cobra subcommand, IPv4/mapped-input validation, and bare-line human
  rendering; verify successful, covered, and IPv6-refusal CLI tests that reference #253.
- [x] 1.2 Add the versioned `supernets` output field and schema/contract coverage; verify JSON output
  remains parse-clean and unsanitized.

## 2. Documentation and integration

- [x] 2.1 Document calc aggregate, including its stateless scope and IPv6 boundary; verify documented
  command output behavior is covered by tests.
- [x] 2.2 Verify the tail-ipam module with `go test ./...` after command, output, and documentation changes are complete.
