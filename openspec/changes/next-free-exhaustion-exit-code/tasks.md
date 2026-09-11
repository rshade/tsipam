# Tasks: next-free-exhaustion-exit-code

## 1. Decision

- [x] 1.1 **DECISION MADE**: Option 2, exit 2 (output operation failed per
  ax-conventions). Exhaustion means the allocation operation couldn't be
  completed, not a program error.

## 2. Code Implementation

- [x] 2.1 Implement Option 2 in `calc.go:nextFreeOutput()` — set status to
  `statusExhausted` when blocks are empty. **Corrected by code review**: the
  caller's first pass emitted output before returning the exhaustion error,
  so the message still reached stdout in both formats (the exact bug the
  change exists to fix) and was duplicated onto stderr. Fixed to return the
  error before ever calling `emitOutput`, mirroring `calc split`'s
  `ErrTooManyChildren` precedent — nothing reaches stdout on exhaustion.
  Fixed lint issue with `errors.New()` instead of `fmt.Errorf()` with a
  non-constant string.

- [x] 2.2 Added `statusExhausted = "exhausted"` constant to `output.go`. Exit
  code 2 is automatic: generic errors from RunE fall through to `exitUsage`
  (2) in `exitCodeFor()`.

## 3. Update Tests

- [x] 3.1 Updated `TestCalcNextFreeMultipleAndExhaustedOutput` subtest
  "exhaustion_is_an_output_operation_failure" to expect err != nil on
  exhaustion. Also updated `TestCalcNextFreeIPv6JSON` and
  `TestCalcNextFreeIPv6Sanitization` to expect errors on exhaustion (when
  blocks are empty). All tests pass with `go test ./cmd/tail-ipam -run
  NextFree`.

  **Corrected by code review**: the original assertions decoded stdout as a
  JSON envelope on exhaustion and required `errOut == ""`, which only passed
  because those tests call `root.Execute()` directly and never observe real
  CLI stderr (see `runCalcSplitCLI`'s doc comment for why). Reworked to add
  `runCalcNextFreeCLI` (mirrors `runCalcSplitCLI`), which drives `runCLI` so
  exit code, empty stdout, and the stderr message are all asserted against
  what a real invocation produces. Moved the IPv6 exhaustion case out of
  `TestCalcNextFreeIPv6JSON` into `TestCalcNextFreeIPv6JSONExhaustion`, and
  split `TestCalcNextFreeIPv6Sanitization`'s exhaustion cases into
  `TestCalcNextFreeIPv6ExhaustionSanitizesStderr`, since sanitization of the
  exhaustion error only happens in `runCLI`'s `sanitizeErrorIfNeeded`, not in
  `emitOutput` (which the exhaustion path no longer calls at all).

- [ ] 3.2 Smoke test `TestLiveNextFreeEdges` does not exist (file not found
  in `tail-ipam/smoke/live_test.go`). Skip this task — can be added in future
  when live smoke tests are implemented.

- [x] 3.3 Checked `tail-ipam/testdata/` recursively — no golden fixtures
  found containing "no free" or referencing
  `calc_next_free`/`next-free`/`nextfree`. No golden files need
  regeneration.

## 4. Documentation Updates

- [x] 4.1 Updated `README.md` lines 567-570: changed "command succeeds" to
  "exits with code 2 (output operation failed)" and clarified message goes
  to stderr. Shell composition examples remain unchanged (they show success
  cases).

  **Corrected by code review**: the first pass's wording ("The JSON output
  contains an empty `free_blocks` array and the message...") documented the
  stdout-leak bug as intended behavior. Corrected to state that nothing
  reaches stdout in either format on exhaustion.

- [x] 4.2 Exit code descriptions in `schema.go` (line 102-106) already
  include "output failure" in the exit 2 description, which semantically
  covers pool exhaustion. No schema change needed.

## 5. Integration & Verification

- [x] 5.1 Ran full tail-ipam test suite: `go test ./...` — all tests pass.
  No goldens required regeneration.

- [ ] 5.2 Repo-wide gates not yet run (gate requires commit and push). Will
  run in phase 6.

- [ ] 5.3 Live tailnet verification requires active tailnet access. Skip for
  now — can be verified manually post-merge with live registry.

## 6. Deliverables

- [ ] 6.1 Ready for commit. Changes implemented:
  - Modified `calc.go` to use `statusExhausted` when no blocks found
  - Modified call site to return the exhaustion error before ever calling
    `emitOutput`, so nothing reaches stdout
  - Added `statusExhausted` constant to `output.go`
  - Updated tests in `calc_test.go` and `calc_nextfree_ipv6_test.go`, added
    `runCalcNextFreeCLI` and `TestCalcNextFreeIPv6ExhaustionSanitizesStderr`
  - Updated README.md documentation
  - All unit tests passing, `golangci-lint run ./...` clean
  - Commit message: `fix(tail-ipam): exit 2 when pool exhausted in calc
    next-free`

- [ ] 6.2 PR checklist:
  - [ ] Push branch to origin/issue-347
  - [ ] Create PR with changes
  - [ ] Verify CI gates pass (test, lint, build, cross-compile)
  - [ ] Merge once CI passes
