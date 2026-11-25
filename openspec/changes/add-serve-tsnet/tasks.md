# Tasks: Serve the registry as a Tailscale Service

## 1. Dependencies and the live identity check

- [ ] 1.1 Record the release binary size, then add `tailscale.com` and
      `github.com/pires/go-proxyproto` to `go.mod`; verify
      `go list -m tailscale.com github.com/pires/go-proxyproto` names both.
      Nothing imports them yet, so 5.1 and 5.2 hold the checks that see only
      imported code
- [ ] 1.2 Add `TestLiveServiceIdentity` in `internal/server`, skipped unless
      `TSIPAM_LIVE_AUTHZ=1` and never run in CI (modeled on tail-watch's
      `TestLiveAuthzWhoIs`). It starts a tagged tsnet host from
      `TSIPAM_LIVE_HOST_AUTHKEY` serving `TSIPAM_LIVE_SERVICE` (default
      `svc:tsipam-live`) with `ServiceModeTCP{Port: 443, TerminateTLS: true,
      PROXYProtocolVersion: 2}` behind a `REQUIRE` `proxyproto.Listener`, and
      a caller tsnet node from `TSIPAM_LIVE_CALLER_AUTHKEY` that dials the
      Service. It logs the raw `RemoteAddr` (expected loopback), the PROXY
      source (expected the caller's tailnet address), the capability map
      from plain `WhoIs` and from `WhoIsForService`, and the lookup time.
      Add `docs/contributing/live-authz-test.md` with the policy and the
      three grant states to run: a grant to the host tag only, a grant to
      the Service only, and no capability grant. Run all three, record the
      results in design.md under the `WhoIsForService` facts, and stop and
      revise design.md if the PROXY source is not the caller or the
      Service-only grant is missing from `WhoIsForService`. The test is the
      first import of both modules from 1.1, so verify `mise run tidy-check`
      passes here

## 2. Contract hooks

- [ ] 2.1 Add `registry.WithGuard`/`GuardFrom` with a `Subject{Action, Kind,
      Key, Site, Service}` type and `registry.WithDefaultOwner`; have every
      `Ledger` reserve, allocate, and release call the guard inside its
      transaction (stored entry for releases) and apply the default owner only
      to new keys without an owner; verify with `storetest` cases (guard denial
      writes nothing, release guard sees the stored site, default owner skipped
      on repeat and on explicit owner) passing for both stores, and
      `go test ./...` passing with no golden change

## 3. API design

- [ ] 3.1 In `design/`, add the API-level `forbidden` error with a 403
      response on every method except `whoami`, add `list_live_routes`,
      `list_identities`, and `whoami`,
      make `plan.live_tailnet` optional, and document network-layer
      authentication in the API description; regenerate with `mise run gen`
      and verify `mise run gen-check` passes, the `gen/` diff touches only
      these additions, and `go build ./...` fails only in `internal/ipamsvc`:
      for the missing `ListLiveRoutes`, `ListIdentities`, and `Whoami`, and
      for the uses of `PlanPayload.LiveTailnet`, now a pointer
- [ ] 3.2 Implement `ListLiveRoutes` and `ListIdentities` in `ipamsvc` (the
      latter over `Ledger.IdentityValues`) and `LiveFromServer` (refuse
      client `live_taken`, collect live inside allocate, server tailnet for
      `plan`, refuse a mismatched `live_tailnet`). Add a `Whoami` that
      returns `identified: false` until 5.1 supplies the caller, and update
      the other uses of `PlanPayload.LiveTailnet`, in
      `cmd/tsipam/planning.go` and the tests; verify `go build ./...` and
      `go vet ./...` pass, with `ipamsvc` unit tests using a fake collector,
      including the missing-credential message

## 4. Authorization

- [ ] 4.1 Add `internal/server/authz` with the server role in
      `cmd/tsipam/boundary_test.go`: validate the capability name (a
      `/cap/` segment, not the `tailscale.com` domain or a subdomain),
      decode each value of the configured capability on its own with
      `tailcfg.UnmarshalCapJSON` and count the ones that fail, then evaluate
      actions, `admin`, implied `read`, and `tailnets`/`sites`/`services`
      globs; verify with table-driven tests covering every scenario in
      `specs/authorization/spec.md`, including a malformed value granting
      nothing while a valid value beside it still grants, and name
      validation for `tailscale.com/cap/x`, `sub.tailscale.com/cap/x`,
      `example.com/x`, and the default; the boundary tests pass
- [ ] 4.2 Add an endpoint-middleware parameter to `ipamhttp.NewHandler`, and
      add `internal/server` (server role in the boundary test) with the
      goa endpoint middleware that maps each method to its action, checks the
      alias before any registry read, and installs the `registry.WithGuard`
      scope check; verify with a test that a caller with no action on the alias
      gets an identical `forbidden` for a reserved and an unreserved key, and
      that the store saw no transaction; the boundary tests pass

## 5. Server

- [ ] 5.1 Add to `internal/server`: tsnet lifecycle (`Dir`, `Hostname`,
      `AdvertiseTags`, `TS_AUTHKEY`), the Service listener with
      `PROXYProtocolVersion: 2` wrapped in a `REQUIRE` `proxyproto.Listener`,
      `ConnContext` storing the PROXY source, the
      `ipamhttp.NewHandler` handler behind an identity middleware that
      calls `WhoIsForService` on every request and sets `WithActor` and
      `WithDefaultOwner`, the caller for `whoami`, a pending-approval warning,
      the health listener, and graceful shutdown; verify unit tests with a
      fake identity source cover a lookup on each of two requests on one
      connection (a grant removed between them denies the second), lookup
      failure as denial and as `identified: false` from `whoami`, a
      connection without a PROXY header closed with no response, the health
      states, and shutdown order. This is the first non-test import of `tsnet`
      and `go-proxyproto`, so also verify
      `mise run cross ::: license-check ::: govulncheck` passes
- [ ] 5.2 Add `cmd/tsipam/serve.go` with the flags from design.md: refuse
      `file://` and `--registry-dir`, require `--advertise-tags`, validate
      `--capability`, open the registry before joining, log the capability
      and Service names, and keep `TS_AUTHKEY` out of every output. Give
      `serve.go` its own entry in `commandExceptions`, listing exactly the
      server packages it imports. Verify the boundary tests pass, `cmd`
      tests for each startup refusal (exit 2, exact stderr), including
      a `tailscale.com` capability, and a test that the auth key never
      appears in stderr at `--verbose`. The binary links the server from here
      on, so record its release size beside the one from 1.1

## 6. Remote client

- [ ] 6.1 Add `internal/remote` with the client role in the boundary test:
      `Run` (`ipamconn.NewClient` over HTTPS, system
      roots, timeouts) and error classification into `server unreachable`,
      `TLS verification failed`, `access denied: ...`, and
      `unexpected server response`; verify with `httptest.NewTLSServer`
      tests for each class plus pass-through of `collision`; the boundary
      tests pass
- [ ] 6.2 Extend `cmd/tsipam/target.go` to resolve `--server`, `TSIPAM_SERVER`,
      `--registry`, `--registry-dir`, and `TSIPAM_REGISTRY` with the exclusion
      and precedence rules, add `--server` to every registry-backed command
      including `plan` and `check`, and choose `loopback.Run` or `remote.Run`
      there. Add `internal/remote` to the `target.go` entry in
      `commandExceptions`; verify the boundary tests pass, with no other
      command file listing `loopback` or `remote`, and `cmd` tests for each
      scenario in
      `specs/remote-client/spec.md` on selection and exclusion
- [ ] 6.3 Make `plan`, `check`, `subnet allocate`, and `calc next-free` skip
      local device reads and credential checks in remote mode, and use
      `list_live_routes` for next-free; verify `cmd` tests with no
      `TAILSCALE_*` credentials set and a fake server
- [ ] 6.4 Seed the sanitizer from `list_identities` plus the server host and
      MagicDNS domain, and fail closed when the call fails; verify a
      sanitized remote error mentioning `tsipam.example.ts.net` shows a
      placeholder and the fail-closed case exits 2 with empty stdout
- [ ] 6.5 Add `cmd/tsipam/whoami.go`: require `--server` or
      `TSIPAM_SERVER`, print the `whoami` result in the JSON envelope, and
      redact `login` and `node` under sanitization; verify `cmd` tests for
      an identified caller with grants, one with none, `identified: false`,
      the missing-server exit 2, and a sanitized run
- [ ] 6.6 Meet the runtime contracts: exclude `serve` from MCP, declare `whoami`
      `read-only` and add it to the pinned tool set, refuse a `server` input in a
      tool call, give `forbidden` and the transport classes their error codes in
      `classify`, and authorize `dry_run` as the real action with no audit event;
      verify the tool-set and leaf-contract tests, the classification table, a
      stdio MCP test for `TSIPAM_SERVER` and for a refused `server` input, and
      server tests for a rehearsal by a reader and by a writer
- [ ] 6.7 Add an end-to-end `cmd` test that runs the goa server with a fake
      identity middleware behind `httptest.NewTLSServer` and runs reserve,
      collision, list, release, allocate, and check-policy remotely; verify
      stdout, stderr, and exit codes match the same commands run locally
      against an identical SQLite registry, with the published run-varying fields
      and the error object's `trace_id` masked, and that the server's spans
      continue the client's trace

## 7. Schema and documentation

- [ ] 7.1 Update the schema catalog (`serve` and its flags including
      `capability`, `whoami`, `server` on registry commands, exit-code 2
      text) and regenerate
      the three schema goldens in `cmd/tsipam/testdata/` with `-update`; verify
      the golden diffs contain only these additions
- [ ] 7.2 Add `docs/how-to/run-a-shared-server.md` (define `svc:tsipam` in
      the admin console first; policy snippet with `tagOwners`,
      `autoApprovers.services`, a network grant to `svc:tsipam` on
      `tcp:443` with the note that `*` does not cover `svc:`, and app grants
      whose `dst` is `svc:tsipam`; `registry import`; a StatefulSet with
      `replicas: 1`, no sidecars, a `ReadWriteOncePod` PVC, probes, and
      Secrets; the no-NFS, no-rolling-update, and no-sidecar warnings;
      first-request certificate delay; a troubleshooting section in the
      order unresolved name, timeout, then `tsipam whoami` showing no
      grants) and `deploy/Dockerfile`; verify
      `mise run lint-md` and `docker build -f deploy/Dockerfile .` succeed
- [ ] 7.3 Add `docs/reference/authorization.md` (capability naming rules
      and `--capability`, value format, action-to-method table, scopes,
      implied read, owner and actor rules, `whoami`, per-request
      revocation, and the local-process trust boundary of the forwarded
      loopback socket) and
      update `docs/reference/exit-codes.md`,
      `docs/how-to/redact-output-for-sharing.md`, `docs/README.md`, and the
      "Why the transport is HTTP" section of
      `docs/explanation/registry-and-live-state.md`; verify
      `mise run lint-md` passes and every example uses `example.ts.net` or
      `example.com`
- [ ] 7.4 Update the `AGENTS.md` package list (`internal/server`,
      `internal/remote`) and its boundary rule, which now also names
      `serve.go`; verify `mise run lint-md` passes

## 8. Integration verification

- [ ] 8.1 Run `mise run test ::: lint ::: cross ::: smoke`, and verify every task
      passes and no existing golden or smoke fixture changed except the three
      schema goldens
- [ ] 8.2 Deploy to a test tailnet with the documented manifest, and verify
      a user and a `tag:ci` node can each reserve, a denied caller exits 2,
      `tsipam whoami` reports each caller's grants, removing a grant denies
      that caller's next request without a reconnect, `registry history`
      shows both actors, and a pod restart keeps the node identity; record
      the run in the PR description

## Workflow follow-up

- Archive after merge, then propose the container-image publishing
  follow-up and `add-server-observability`.
