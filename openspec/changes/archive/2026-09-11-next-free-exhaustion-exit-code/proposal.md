# Proposal: Calc Next-Free Exit Code

## Why

The `calc next-free` command currently reports pool exhaustion (no free
block found) by writing a human message to stdout with exit 0. This breaks
the documented shell composition
`tail-ipam calc next-free ... | tail-ipam subnet reserve ...` in two ways:

1. An empty stdout reaches `subnet reserve` as an invalid CIDR, causing
   exit 2 with a misleading error message
2. Exit 0 signals success, so scripts cannot distinguish "found a free
   block" from "pool is exhausted" without parsing the text output

The `calc split` command already handles a similar exhaustion case (too
many subnets to enumerate) by exiting 1 and writing diagnostics to
stderr—establishing a precedent in the same CLI.

## What Changes

Choose and implement one of two options to align the exit code and output streams with CLI semantics and the split precedent:

### Option 1: Exit 0 (preserve semantics), move message to stderr

- Keep exit code 0 (request was well-formed)
- Move "no free /24 block in..." to stderr
- Leave stdout empty (no CIDR to pass downstream)
- Shell composition fails with `subnet CIDR must not be empty` (correct source)
- Scripts can use `[ -n "$(tail-ipam calc next-free ...)" ]` to test success

### Option 2: Exit 2 (output operation failed)

- Exit 2 on exhaustion (the allocation operation couldn't be completed)
- Move "no free /24 block in..." to stderr
- Aligns with ax-conventions: "output operation failed" (per docs/reference/ax-conventions.md)
- Breaks the documented contract (current behavior is exit 0 on exhaustion)

Either option requires updates to:

- README shell composition and exit-code table
- `-schema` output (exit-code descriptions)
- Smoke test `TestLiveNextFreeEdges` (pins current behavior)

## Capabilities

### Modified Capabilities

- `calc-next-free-exit-code`: Exit code and output stream semantics for pool exhaustion

## Impact

- **Code**: `tail-ipam/cmd/tail-ipam/calc.go:nextFreeOutput()` (message and
  exit behavior), `tail-ipam/cmd/tail-ipam/calc_test.go` (message assertions)
- **Tests**: `tail-ipam/smoke/live_test.go::TestLiveNextFreeEdges` (pinned to current behavior)
- **Docs**: README sections on `calc next-free` (composition, exit codes)
- **Schema**: JSON `-schema` output and CLI help text
