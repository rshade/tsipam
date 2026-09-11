# Tasks: Registry CIDR Aggregation

## 1. Registry aggregation

- [x] 1.1 Add source selection and reuse the generated subnet-pool read and
  aggregation path; verify IPv4 output, empty input, selection precedence,
  source conflicts, and no live-device access with command tests.
- [x] 1.2 Track supernet provenance through output and error sanitization;
  verify identity/strict human and JSON output, stable tailnet scoping,
  positional exemptions, and safe registry failures.

## 2. Discovery and documentation

- [x] 2.1 Document the new mode and sanitization distinction in README and
  schema; regenerate the schema golden deliberately and inspect its diff.

## 3. Verification and review

- [x] 3.1 Run module build, tests, and lint; root `make test lint cross lint-md`;
  OpenSpec validation and whitespace checks. Confirm changes stay in tail-ipam.
- [x] 3.2 Complete Standards, Spec, and Scout reviews against issue #254 and
  these artifacts; resolve confirmed findings and verify all tasks complete.
