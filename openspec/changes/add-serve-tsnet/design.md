# Design: Serve the registry as a Tailscale Service

## Context

See `proposal.md` for motivation. This change builds on three earlier ones:

- `refactor-registry-store`: `registry.Ledger` holds every rule and runs
  against a `registry.Store` (`View`/`Update`). `ipamsvc.Service` holds a
  `*registry.Ledger`. `loopback` builds the generated goa client over an
  in-process Unix socket.
- `add-registry-export-import`: the `registry` command group.
- `add-sqlite-store`: `--registry <url>` and `TSIPAM_REGISTRY` select the
  backend; `sqlitestore` writes one `events` row per mutation with a
  nullable `actor` read from `registry.ActorFrom(ctx)`; the optional
  `registry.IdentitySource` interface feeds the sanitizer.

Facts verified in `tailscale.com@v1.104.5` from the module cache:

- `tsnet.Server.ListenService(name, mode)` advertises the node as a Service
  host. Hosts must be tagged, and the Service needs approval by an admin or
  an auto-approver.
- `tsnet.ServiceModeTCP{Port, TerminateTLS}` gives the application a raw
  connection after TLS termination. `ServiceModeHTTP` proxies through
  serve, and serve deliberately sets no identity headers for tagged nodes
  (`ipn/ipnlocal/serve.go:1174`).
- `LocalClient().WhoIs` returns `UserProfile`, `Node` (with tags), and
  `CapMap`. `tailcfg.UnmarshalCapJSON[T](capMap, name)` decodes capability
  values.
- `tsnet.Server` has `Dir`, `Hostname`, `AuthKey`, `AdvertiseTags`, and
  `Store`.

Constraints from the repo:

- `cmd/tsipam/main.go` `exitCodeFor` maps collisions and drift to 1 and
  everything else to 2. The schema spec fixes the catalog at three codes.
- Failures write nothing to stdout (`docs/reference/json-envelope.md`).
- Sanitization pre-registers identity values from the registry before the
  command runs, so placeholders are stable across runs.
- `mise run cross` builds Linux, macOS, and Windows with no cgo.

## Goals / Non-Goals

**Goals:**

- One registry API served to the whole tailnet, with identity and access
  control coming from the tailnet itself.
- Remote output and exit codes identical to local mode, so scripts and CI
  only add `--server`.
- Clients never need storage access or Tailscale API credentials.

**Non-Goals:**

- More than one replica, leader election, or Postgres.
- Storing node state in a Kubernetes Secret (`kubestore`). The volume that
  holds the SQLite file also holds node state.
- Drift timer, `/metrics`, and the web UI (`add-server-observability`).
- Remote `registry import`/`export`. These stay local operations on the
  database file.
- More than one live tailnet per server.
- Funnel or any access from outside the tailnet.
- Publishing a container image. A Dockerfile and docs ship here;
  publishing it is a follow-up.
- A tsnet-based client. The client relies on the device's own Tailscale
  connection and MagicDNS.

## Decisions

### Embed tsnet and listen on the Service with TCP and TLS termination

`tsipam serve` creates `tsnet.Server{Dir: stateDir, Hostname,
AdvertiseTags, AuthKey: TS_AUTHKEY}`, calls `Up`, then
`ListenService(service, tsnet.ServiceModeTCP{Port: 443, TerminateTLS: true})`,
and runs a standard `http.Server` with the goa handler on that listener.

*Alternatives considered:*

- **The Kubernetes operator's ingress in front of a plain HTTP pod.** It
  uses serve, which sets no identity for tagged nodes, so CI callers would
  be anonymous.
- **`ServiceModeHTTP`.** Same identity problem, since the request goes
  through serve's reverse proxy.
- **Listening on the node's own name instead of a Service.** This ties
  clients to one node's name and loses the stable `svc:` address that lets
  the host change.

*Assumption, checked by task 1.1:* a connection accepted on the Service
listener reports the caller's tailnet IP as `RemoteAddr`, so `WhoIs`
resolves it; and grant app capabilities whose `dst` is `svc:tsipam` appear
in that `WhoIs` `CapMap`. If either is false, the change stops and
returns to design; there is no silent fallback.

### Identity once per connection

`http.Server.ConnContext` stores the connection's remote address. A
middleware calls `WhoIs` the first time a connection carries a request and
caches the resulting `Caller{Login, NodeName, Tags, Caps}` on that
connection. If `WhoIs` fails, the request is denied with `forbidden`. The
middleware puts the `Caller` in the request context and sets
`registry.WithActor(ctx, actor)`, where actor is `user:<login>` or
`node:<node-name>`.

### Authorization is a pure function plus a transaction guard

`internal/server/authz` decodes the capability values into
`[]Grant{Actions, Tailnets, Sites, Services}` and evaluates them with
`path.Match` globs. Two checkpoints apply them:

1. **At method entry** (a goa endpoint middleware), the method's action and
   the registry alias are checked. A caller with no action on the alias is
   denied here, before any registry read, which gives the non-disclosure
   requirement.
2. **Inside the Ledger transaction**, the site or service scope is checked
   by a guard the Ledger calls with the resolved subject: the requested
   site for reserves and allocations, and the stored entry's site or
   service for releases. This adds
   `registry.WithGuard(ctx, func(Subject) error)` beside `WithActor`. The
   local CLI sets no guard, so behavior is unchanged. A release cannot be
   approved against stale data, because the check and the removal share
   one transaction.

`admin` grants every action. `reserve`, `release`, and `allocate` imply
`read` for the same `tailnets` scope; site and service scopes do not narrow
it. The reason: a collision message already names the other site, so
pretending a mutating caller cannot see stored state would be hollow.

*Alternative:* one check in `ipamsvc` after a separate `View`. That leaves a
time-of-check/time-of-use gap on release and needs an extra transaction.

### Owner default lives in the Ledger, behind context

The server sets `registry.WithDefaultOwner(ctx, login-or-node-name)`.
`Ledger` reserve and allocate apply it only when the key is new and the
request has no owner, before `resolveReservationEntry`, so the existing
first-metadata rule stamps `reserved_at`. Local mode sets no default.

*Decision on `--owner`:* an explicit owner is free text that any caller
with `reserve` or `allocate` may set. The audit `actor` is the unforgeable
record of who made a change, and `owner` stays an annotation (often a
team). Requiring `admin` for `--owner platform-team` would push routine
reserves to admins for no security gain.

### Remote client: the same goa client, a different transport

`internal/remote.Run(ctx, serverURL, logger, fn)` builds the generated
`ipamclient.Client` with `goahttp` over an `http.Client` that uses system
TLS roots (Service certificates are publicly trusted `*.ts.net`
certificates), a 10-second dial timeout, and a 60-second overall request
timeout. A single resolver `cmd/tsipam/target.go` turns flags and
environment into either `{local Store URL}` or `{server URL}`. Every
registry call site changes from `loopback.RunWithLogger(ctx, registryDir,
...)` to `target.Run(ctx, ...)`. That is the payoff of the loopback design:
command code does not branch on mode.

Error classification for stderr, all exiting 2:

| Error | Message prefix |
| --- | --- |
| `*url.Error` wrapping a dial or DNS error | `server unreachable` |
| `*tls.CertificateVerificationError`, `x509.*` | `TLS verification failed` |
| goa `forbidden` | `access denied: needs <action> on <scope kind>` |
| unexpected status or decode error | `unexpected server response` |

Collision and drift keep their current mapping, because goa
`ServiceError`s arrive the same way they do over loopback.

### Server-side live reads

`ipamsvc.Service` gains `LiveFromServer bool`. When it is set:

- `allocate_subnet_pool` refuses a non-empty `live_taken` with
  `invalid_input`, and collects live routes itself with `CollectLive`
  inside the request.
- `plan` makes `live_tailnet` optional. The design change drops it from
  `Required`, and the loopback client still sends it. The server uses its
  configured tailnet and refuses a different value.
- `list_live_routes` (new, `GET /tailnet/{tailnet}/ipam/live-routes`,
  needs `read`) returns the advertised routes `calc next-free` counts as
  taken.

A missing credential is reported as `comparison_failed` or `invalid_input`
with the text `server has no Tailscale API credentials configured`.

### Sanitizer seeding in remote mode

`list_identities` (new, `GET /tailnet/{tailnet}/ipam/identities`, needs
`read`) returns `registry.IdentitySource` values. Before the operation, the
remote CLI calls it whenever sanitization is active and registers the
values in the same order local mode does. It also registers the server
host and its MagicDNS domain (`tsipam.example.ts.net`, `example.ts.net`).
If the call fails, the command fails closed with exit 2.

### Goa changes

- The error `forbidden` (HTTP 403) is added to every method through an
  API-level `Error` plus a `Response("forbidden", StatusForbidden)` in each
  HTTP block.
- New methods: `list_live_routes` and `list_identities`.
- `plan.live_tailnet` becomes optional.
- No goa `Security` schemes. Authentication happens at the network layer,
  and an empty scheme would mislead OpenAPI readers. The OpenAPI
  description says so instead.

### Serve command and lifecycle

Flags: `--registry` (required, `sqlite://` only), `--service`
(`svc:tsipam`), `--hostname` (`tsipam`), `--advertise-tags` (required),
`--state-dir` (`/var/lib/tsipam/tsnet`), and `--health-listen` (empty by
default).

The registry opens before the tailnet node starts, so a bad database fails
fast. tsnet's own log lines go to the diagnostics logger at debug level,
filtered so `TS_AUTHKEY` never appears. Approval status is read from the
local client after `Up` and logged as a warning when pending. Shutdown runs
`http.Server.Shutdown` with a 10-second deadline, then `tsnet.Server.Close`,
then closes the store.

### Deployment shape (docs)

- **One container, one PVC** at `/data`, holding `tsipam.db` and `tsnet/`.
- **StatefulSet** with `replicas: 1` and `ReadWriteOncePod` access mode.
  A Deployment would need `strategy: Recreate`; the docs say why rolling
  updates are unsafe.
- **Block storage only.** NFS, EFS, and Azure Files are explicitly
  unsupported.
- **A Secret** for `TS_AUTHKEY` (tagged `tag:tsipam`) and the Tailscale API
  OAuth client.
- **Probes** on `--health-listen :8080`.
- **Policy snippet:** `tagOwners` for `tag:tsipam`; `autoApprovers.services`
  for `svc:tsipam` approving `tag:tsipam`; grants with the
  `github.com/rshade/tsipam/cap/ipam` app capability.

## Risks / Trade-offs

- [The Service listener's `RemoteAddr` is not the peer IP, or capabilities
  are not delivered to Service hosts] → Task 1.1 prototypes both before any
  other work. Failing either reopens the design.
- [`tailscale.com` is a large dependency: govulncheck exposure, build time,
  Windows and macOS cross-compiles] → Task 2.1 adds it and runs
  `mise run cross`, `govulncheck`, and `license-check` first. If Windows
  fails, `serve` gets a build constraint and the client stays portable.
- [One replica means a few seconds of unavailability on restart] → Accepted.
  tsipam is not in the data path, and clients report `server unreachable`
  with exit 2, which CI retries.
- [Capability globs are easy to get wrong] → `authz` is a pure function with
  table tests, and denial messages name the missing action and scope kind.
- [`WhoIs` adds latency] → Cached per connection. The HTTP keep-alive
  connections that goa clients use make this one lookup per command.
- [The first request after a fresh deploy waits for certificate issuance] →
  The client's 60-second timeout covers it. The docs mention the first
  slow request.
- [Server logs could carry caller identities] → Server diagnostics are
  identity-free by default. Actors are stored only in `events`.

## Migration Plan

1. Ship the client and server together. Local mode is unchanged and
   `--server` is opt-in.
2. Operators run `tsipam registry import` into a SQLite file, deploy
   `serve` with it, add the policy snippet, and point users with
   `TSIPAM_SERVER`.
3. Rollback: unset `TSIPAM_SERVER` and use the local registry (export from
   the server's database first if it has newer reservations).

## Open Questions

- Should node state move to a Kubernetes Secret (`kubestore`) so the
  volume holds only data? This is deferrable, since the volume already
  exists.
- Which registry should publish the container image (GHCR through
  goreleaser, or `ko`)? This is deferrable to the image follow-up.
