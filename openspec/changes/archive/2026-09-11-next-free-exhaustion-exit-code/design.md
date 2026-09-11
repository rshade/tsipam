# Design: Calc Next-Free Exit Code

## Context

See proposal.md for motivation. Current state: `calc next-free` writes
exhaustion messages ("no free /SIZE block") to stdout with exit 0, breaking
the documented shell composition and making it impossible for scripts to
distinguish success from exhaustion without text parsing.

The `calc split` command already handles a parallel case (oversized split)
by exiting 1 with the message on stderr, establishing a precedent within
the same CLI.

## Goals

1. Make the exhaustion case unambiguous for scripts via exit code or empty stdout
2. Move the human-readable message to stderr (diagnostics belong there)
3. Leave stdout composable: either empty (silent success) or not written at all
4. Decide between two internally consistent options and implement the chosen one

## Decisions

### Where to make the change

**`tail-ipam/cmd/tail-ipam/calc.go:nextFreeOutput()` (line ~164)**

- Currently builds the message and returns status "ok"
- Decision logic branches here: which option was chosen?
- Exit code: determined in `main.go` based on the returned status (currently "ok" → 0)

#### Output routing

- Message moves from stdout → stderr
- Option 1: keep status "ok", message goes to stderr, stdout empty
- Option 2: introduce status "exhausted" (or similar), message goes to stderr, exit 2

#### Test assertions

- `calc_test.go:TestCalcNextFree` checks message content (currently in stdout)
- Smoke test `tail-ipam/smoke/live_test.go:TestLiveNextFreeEdges` pins current behavior

### Key implementation difference between options

**Option 1**: Add stderr output logic without changing exit semantics

- Status remains "ok" → exit 0
- Add `fmt.Fprintf(os.Stderr, ...)` call in `nextFreeOutput()`
- Minimal code changes, fully backwards-compatible for exit codes

**Option 2**: Introduce a new status value for output operation failure

- Add status "exhausted" to the status-to-exit-code mapping
- Status "exhausted" → exit 2 in `main.go` (per ax-conventions: "output operation failed")
- Semantic clarity: the allocation operation couldn't be completed (no free block exists)
- Breaks documented contract: current scripts expecting exit 0 will fail

## Risks / Trade-offs

| Risk | Mitigation |
| --- | --- |
| Option 2 breaks backward compat for exit 0 consumers | Clearly documented in CHANGELOG, dated release tag, communication in README |
| Smoke test `TestLiveNextFreeEdges` is pinned to exit 0 | Updated together with the code change; regenerate once option is chosen |
| Golden test fixture may exist under `testdata/` | Search for "no free.*block" in goldens; regenerate as needed |

## Migration Plan

1. Choose Option 1 or Option 2 per issue #347 (user decision)
2. Implement in `nextFreeOutput()` + routing logic (1-2 code locations)
3. Update test assertions in `calc_test.go` (message now on stderr, not stdout)
4. Update smoke test `TestLiveNextFreeEdges` to expect new exit code behavior
5. Update README section on `calc next-free` (exit-code table, composition examples)
6. Update `-schema` JSON output (exit-code descriptions and reasons)
7. Verify all tests pass and golden fixtures match

## Open Questions

None. Decision made: Option 2, exit 2 (output operation failed per ax-conventions).
