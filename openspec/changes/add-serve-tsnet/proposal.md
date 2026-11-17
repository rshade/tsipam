# Proposal: Serve the registry as a Tailscale Service

## Why

Several people and CI jobs need to reserve address space in the same
registry at the same time, and to look up who holds what, without each of
them holding a copy of the registry or Tailscale API credentials. The
loopback transport was built so that "pointing the client at a shared
service is an endpoint change". With `add-sqlite-store` providing
concurrent-safe storage, the remaining piece is a server reachable on the
tailnet that knows who is calling.

## What Changes

- Add `tsipam serve`: a long-running process that embeds `tsnet`, joins the
  tailnet as a tagged node, and serves the existing goa API over HTTPS as
  the Tailscale Service `svc:tsipam` (reachable as
  `https://tsipam.<tailnet>.ts.net`). It runs one replica against a SQLite
  registry and refuses `file://` registries.
- Identify the caller of every request with a Service-scoped
  `WhoIsForService` lookup on the caller's tailnet address, which the
  Service forwarder delivers in a PROXY protocol header. Users and tagged
  nodes (CI runners) are both identified; nothing is read from HTTP
  request headers. Identity is resolved per request, so a revoked grant
  applies on the next request.
- Authorize every request from a grant app capability, by default
  `github.com/rshade/tsipam/cap/ipam` and configurable with `--capability`:
  actions `read`, `reserve`, `release`, `allocate`, and `admin`, optionally
  scoped by registry alias, site, and service globs. With no matching
  grant, the request is denied.
- Add `tsipam whoami`, which shows a caller the identity and grants the
  server resolved for them.
- Record the caller as the actor of every audited change. A new reservation
  made without `--owner` records the caller as its owner.
- Add `--server <url>` and `TSIPAM_SERVER` to every registry-backed command.
  Remote mode replaces the in-process loopback with an HTTPS client and is
  mutually exclusive with `--registry`/`--registry-dir`.
- Move live Tailscale API reads for `plan`, `check`, `subnet allocate`, and
  `calc next-free` to the server in remote mode, so clients need no API
  credentials. Add the read-only methods `list_live_routes` and
  `list_identities`; the latter feeds the client's sanitizer.
- Add the goa error `forbidden` (HTTP 403). Forbidden, unreachable-server, and
  TLS failures exit 2 with empty stdout. The exit-code catalog stays at three
  codes and the JSON envelope does not change.
- Document a container and Kubernetes deployment: a StatefulSet with one
  replica and no sidecars, a `ReadWriteOncePod` volume on block storage,
  and the tailnet policy (tag owner, Service definition and auto-approval,
  a network grant to `svc:tsipam`, capability grants).

## Capabilities

### New Capabilities

- `server`: `tsipam serve`, which covers tailnet membership, the Tailscale
  Service listener, storage restrictions, live-read credentials, the health
  endpoint, and the startup and shutdown contract.
- `remote-client`: `--server`/`TSIPAM_SERVER` selection, mutual exclusion
  with local storage, transport and TLS failures, and how sanitization works
  against a server.
- `authorization`: caller identity, the capability format, how actions and
  scopes are evaluated, denial behavior, recording the actor, and
  `whoami`.

### Modified Capabilities

- `plan-check`: in remote mode, the live tailnet and its credentials come
  from the server rather than the client's environment.
- `metadata`: remote reserves record the caller as `owner` when the
  reservation is new and `--owner` is omitted.
- `subnet`: remote `subnet allocate` collects live routes on the server.
- `calc`: remote `calc next-free` reads live routes from the server.
- `schema`: the catalog lists `serve`, `whoami`, and the `server` flag,
  and describes the new failures that exit 2.

## Impact

- **Code:** new `cmd/tsipam/serve.go`; new `internal/server` (tsnet
  lifecycle, identity, authorization middleware); new `internal/remote`
  (HTTPS client counterpart of `internal/loopback`); `internal/ipamsvc`
  (authorization guard, server-side live reads, owner default, new methods);
  `cmd/tsipam/main.go` (storage selection, sanitizer seeding, exit codes).
- **API:** `design/` gains `forbidden`, `list_live_routes`,
  `list_identities`, and `whoami`; `plan.live_tailnet` and
  `allocate_subnet_pool.live_taken` become server-filled in remote mode.
  `gen/` is regenerated.
- **Dependencies:** `tailscale.com` (BSD-3-Clause, on the allowlist) for
  `tsnet` and `tailcfg`; `github.com/pires/go-proxyproto` (Apache-2.0),
  already required by `tailscale.com`, becomes a direct import.
- **Precedent:** tail-watch's `ui-authorization` capability, whose lab
  findings on `WhoIsForService` this design relies on.
- **Depends on:** `refactor-registry-store` (`Ledger`, `Store`),
  `add-sqlite-store` (`--registry` URLs, the `events` table with `actor`).
- **Enables:** `add-server-observability` (drift timer, `/metrics`,
  read-only UI).
- **Docs:** new how-to guides for running the server and for
  authorization; updates to the exit-codes, redaction, and README pages.
