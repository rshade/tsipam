# Implementation tasks

## 1. Static comparison

- [x] 1.1 Add pure HuJSON ipset resolution; verify ordered operations, aliases, cycles, and invalid-input tests.
- [x] 1.2 Add bidirectional pool classification; verify matched and both drift directions with fixture-pair goldens.

## 2. Command and API

- [x] 2.1 Add the goa method and service; regenerate transport and verify offline service integration tests.
- [x] 2.2 Add CLI output, exit codes, and sanitization; verify human/JSON, error, and immutable-input regressions.
- [x] 2.3 Document static scope and command schema; inspect the regenerated schema golden and validate OpenSpec.

## 3. Verification and review

- [x] 3.1 Run repo-wide test, build, cross, release, lint, and Markdown gates; record actual results in the PR.
- [x] 3.2 Review standards and issue/spec compliance, assess scout findings, and fix confirmed issues before commit.
