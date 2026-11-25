# Tasks: Separate client code from server code

## 1. Baseline

- [x] 1.1 On `main`, save stdout, stderr with `--verbose` (timestamps and
      socket paths removed), and the exit code of a reserve, a collision, a
      release, a list, `check-policy` with drift, `calc next-free` and `plan`
      without credentials, and `-schema`; verify the files exist outside the
      repository and a second run is identical

## 2. Handler, client, and loopback

- [x] 2.1 Add `internal/ipamhttp.NewHandler` per D2; verify a test serves one
      method through `httptest` and a collision returns 409, and that the
      package does not import `ipamsvc`
- [x] 2.2 Add `internal/ipamconn.NewClient` per D3; verify a test calls
      `ipamhttp.NewHandler` through it over `httptest`
- [x] 2.3 Replace `RunWithLogger` and `RunWithService` with `loopback.Run`
      per D4, add `cmd/tsipam/target.go` per D5, move all eight call sites to
      it, and have `loopback_test.go` build its own handler; verify
      `go test ./...` passes with no other test file edited and
      `grep -rn 'RunWithLogger\|RunWithService' --include='*.go' .` is empty

## 3. CLI output types

- [x] 3.1 Replace the `policycheck` types per D6 and move the sanitization
      guard in `policy_check_test.go` to the new types; verify
      `go test ./cmd/tsipam` passes with no golden change, that adding a
      field to `policyReservedOutput` without a decision fails the guard, and
      that renaming a JSON tag on any of the three types fails
      `TestPolicyCheckOfflineContract`
- [x] 3.2 Replace the two `planner` constants per D6; verify a test asserts
      equality with `planner`'s and the sanitization tests pass unchanged

## 4. Import test and documentation

- [x] 4.1 Add `cmd/tsipam/boundary_test.go` per D7; verify it passes, and that
      it fails for `ipamsvc` imported in `subnet.go`, `registry` in
      `loopback`, `loopback` in `ipamsvc`, an unused list entry, and a new
      `internal/` package with no role
- [x] 4.2 Update the `AGENTS.md` package list with `ipamhttp`, `ipamconn`, the
      narrower `loopback`, and the boundary rule, update the `loopback`
      package comment, and add this change to the shared-server section of
      `ROADMAP.md`; verify `mise run lint-md` passes

## 5. Integration verification

- [x] 5.1 Run `mise run test vet lint cross smoke` and `go test -race ./...`;
      verify all pass
- [x] 5.2 Repeat the 1.1 commands on the branch; verify no difference
- [x] 5.3 Verify `git diff --stat main -- design/ gen/ smoke/ go.mod go.sum
      cmd/tsipam/testdata/` is empty
- [x] 5.4 Check the plans in the contact table against the merged names;
      verify `openspec validate --all --strict` reports no new problem

## Workflow follow-up

- Commit as `refactor: ...` (no release entry, since nothing user-visible
  changes).
- Archive after merge, before groups 3 to 9 of `adopt-ax-go-runtime` and
  before `add-sqlite-store` and `add-serve-tsnet`.
