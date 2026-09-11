# IPv6 next-free tasks

## 1. CLI and regression coverage

- [x] 1.1 Accept IPv6 pools and update help; verify IPv6 bare-CIDR output,
  canonicalization, target bounds, and mapped-prefix compatibility in CLI tests.
- [x] 1.2 Verify mixed-family registry/live exclusions, count/all ordering,
  JSON, exhaustion, sanitization, and read-only behavior with CLI tests.
- [x] 1.3 Update README examples and deliberately regenerate the schema golden;
  inspect the fixture diff and run Markdown lint.

## 2. Integration verification

- [x] 2.1 Validate both OpenSpec changes and run repository test, build, lint,
  cross-build, release validation, and snapshot gates; review against issue #257.
