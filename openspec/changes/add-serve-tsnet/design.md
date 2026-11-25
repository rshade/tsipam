# Design: Serve the registry as a Tailscale Service

## Context

See `proposal.md` for motivation. This change builds on four earlier ones:

- `refactor-registry-store`: `registry.Ledger` holds every rule and runs
  against a `registry.Store` (`View`/`Update`). `ipamsvc.Service` holds a
  `*registry.Ledger`.
- `refactor-client-server-boundary`: `ipamhttp.NewHandler` builds the goa
  handler, `ipamconn.NewClient` builds the generated client, `loopback.Run`
  serves a handler over an in-process Unix socket, and
  `cmd/tsipam/target.go` is the one place commands reach them. An import
  test enforces it.
- `add-registry-export-import`: the `registry` command group.
- `add-sqlite-store`: `--registry <url>` and `TSIPAM_REGISTRY` select the
  backend; `sqlitestore` writes one `events` row per mutation with a
  nullable `actor` read from `registry.ActorFrom(ctx)`; the optional
  `registry.IdentitySource` interface feeds the sanitizer.

Facts verified in `tailscale.com@v1.104.5` from the module cache:

- `tsnet.Server.ListenService(name, mode)` advertises the node as a Service
  host. Hosts must be tagged, and the Service needs approval by an admin or
  an auto-approver.
- `ListenService` does not hand the application tailnet connections. It
  binds a host socket with `net.Listen("tcp", "localhost:0")` and points
  serve's TCP forwarder at it (`tsnet/tsnet.go`, `ListenService`). An
  accepted connection's `RemoteAddr` is therefore loopback, never the
  caller.
- `tsnet.ServiceModeTCP{Port, TerminateTLS, PROXYProtocolVersion}`: with
  `PROXYProtocolVersion` set, serve writes a PROXY protocol header carrying
  the caller's tailnet source address before forwarding
  (`ipn/ipnlocal/serve.go:743`, through `github.com/pires/go-proxyproto`,
  Apache-2.0, already a `tailscale.com` dependency). `ServiceModeHTTP`
  proxies through serve, and serve deliberately sets no identity headers
  for tagged nodes (`ipn/ipnlocal/serve.go:1174`).
- `LocalClient().WhoIs` returns `UserProfile`, `Node` (with tags), and
  `CapMap`. `WhoIsForService(ctx, addr, svc)` returns the same with
  `CapMap` scoped to the Service. `tailcfg.UnmarshalCapJSON[T](capMap,
  name)` decodes capability values.
- `tsnet.Server` has `Dir`, `Hostname`, `AuthKey`, `AdvertiseTags`, and
  `Store`.

Facts from the sibling project tail-watch, checked on a lab tailnet on
2026-10-17 (its archived `add-ui-authorization` design, D2):

- An app grant whose `dst` is only `svc:<name>` is returned by
  `WhoIsForService` and **not** by plain `WhoIs`.
- `WhoIs` accepts a bare IPv4 address, a bare IPv6 address, and
  `ip:port`.
- The policy API rejects a capability name in the `tailscale.com` domain.
- A `*` grant destination does not cover a `svc:` destination, so a Service
  needs its own network grant. A Service must be defined in the admin
  console before advertising it has any visible effect.

Constraints from the repo:

- `cmd/tsipam/errors.go` `classify` maps collisions and drift to 1 and
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

`tsipam serve` creates `tsnet.Server{Dir: stateDir, Hostname, AdvertiseTags,
AuthKey: TS_AUTHKEY}`, calls `Up`, then `ListenService(service,
tsnet.ServiceModeTCP{Port: 443, TerminateTLS: true, PROXYProtocolVersion: 2})`.
It wraps the returned listener in a `proxyproto.Listener` whose policy is
`REQUIRE`, so a connection without a valid PROXY header is closed before any
HTTP is read, and runs a standard `http.Server` with the `ipamhttp.NewHandler`
handler on the wrapped listener. The wrapped connection's `RemoteAddr` is the
caller's tailnet source address.

*Alternatives considered:*

- **The Kubernetes operator's ingress in front of a plain HTTP pod.** It
  uses serve, which sets no identity for tagged nodes, so CI callers would
  be anonymous.
- **`ServiceModeHTTP`.** Same identity problem, since the request goes
  through serve's reverse proxy.
- **Listening on the node's own name instead of a Service.** This ties
  clients to one node's name and loses the stable `svc:` address that lets
  the host change.

*Assumption, checked by the live test in task 1.2:* through a real
Service, the PROXY header's source address is the caller's tailnet
address, and `WhoIsForService` on that address returns a grant whose `dst`
is the Service. The tail-watch result makes the second likely but was
observed through `tailscale serve` on a kernel node, not tsnet. If either
is false, the change stops and returns to design; there is no silent
fallback.

### The local-process gap

The forwarded socket is an ordinary loopback port. Any process that can
reach the server's loopback interface can connect to it and send a PROXY
header naming another peer's tailnet address, and `WhoIsForService` will
then report that peer's identity and grants. In Kubernetes every container
in a pod shares one network namespace, so a sidecar is such a process.

This change does not close the gap; tsnet offers no in-process Service
listener to close it with. The deployment guide says plainly that the
`tsipam serve` container must be the only workload in its pod and network
namespace, with no sidecars, and `docs/reference/authorization.md` states
the trust boundary. tail-watch documents the same gap for its loopback
listener. If tsnet later exposes an in-process Service listener, switching
to it closes the gap without changing the API.

### Identity on every request

`http.Server.ConnContext` stores the connection's PROXY-derived source
address. A middleware calls
`LocalClient().WhoIsForService(ctx, addr, service)` on every request and
builds `Caller{Login, NodeName, Tags, Caps}`. The `CapMap` is scoped to the
Service, so a grant must name `svc:tsipam` (or a tag the host carries) to
apply. If the lookup fails, the request is denied with `forbidden`. The
middleware puts the `Caller` in the request context and sets
`registry.WithActor(ctx, actor)`, where actor is `user:<login>` or
`node:<node-name>`.

There is no cache. Caching per connection would let a goa client keep a
revoked grant for as long as it held a keep-alive connection. A lookup on
every request means a revoked grant takes effect on the caller's next
request. The tsnet `LocalClient` is served in process, so the call does
not leave the process. If the live test measures it as too slow, a cache
keyed by address with a TTL of at most 5 seconds is the fallback, and the
spec's "next request" wording would become "within 5 seconds".

### The capability name is configurable and validated

`--capability` names the peer capability, defaulting to
`github.com/rshade/tsipam/cap/ipam`. A second server on the same tailnet,
such as staging, can use its own name so the grants of one never authorize
the other. Startup refuses a name without a `/cap/` segment, and a name
whose domain is `tailscale.com` or a subdomain of it, because the policy
API rejects those and a grant could never match.

### `whoami` shows the caller what the server sees

A new goa method `whoami` (`GET /whoami`) needs no action. It returns
`identified`, `login` (empty for a tagged node), `node`, `tags`, the
capability name, the decoded capability values (`grants`), and
`ignored_values`, the count of values that did not parse. When the lookup
fails, it returns `identified: false` instead of `forbidden`, so a denied
caller can see why. `tsipam whoami` calls it and needs `--server` or
`TSIPAM_SERVER`; without one it exits 2. Under sanitization, `login` and
`node` are redacted like `owner`. This mirrors tail-watch's
`GET /api/v1/me`.

### Authorization is a pure function plus a transaction guard

`internal/server/authz` decodes the capability values into
`[]Grant{Actions, Tailnets, Sites, Services}` and evaluates them with
`path.Match` globs. Each value is decoded on its own, as tail-watch's
`roleFromCaps` does, so one malformed value is ignored without discarding
the valid values beside it. Two checkpoints apply them:

1. **At method entry** (a goa endpoint middleware, passed to
   `ipamhttp.NewHandler` through a new parameter), the method's action and
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

`internal/remote.Run(ctx, serverURL, logger, fn)` builds the generated client
with `ipamconn.NewClient` over an `http.Client` that uses system
TLS roots (Service certificates are publicly trusted `*.ts.net`
certificates), a 10-second dial timeout, and a 60-second overall request
timeout. The resolver in `cmd/tsipam/target.go` turns flags and
environment into either `{local Store URL}` or `{server URL}`. All eight
call sites, including `plan` and `check`, already go through that file, so
the transport choice happens in one place. Command code branches on mode
only for live reads and credential checks (`calc next-free` calls
`list_live_routes` instead), for sanitizer seeding from `list_identities`, and
for `whoami`, which needs a server.

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

### Runtime contracts

`adopt-ax-go-runtime` landed first, so this change meets its contracts:

- **MCP:** `serve` is excluded with `mcp.Exclude`, because it is the one
  long-running command. `whoami` is `read-only` in `leafContracts` and joins the
  pinned tool set: an agent may ask what it is allowed to do.
- **Remote access through MCP:** that change made it a non-goal. This one lifts
  it for `TSIPAM_SERVER` only, set in the environment by whoever starts
  `mcp-server`. A `server` input in a tool call is refused with
  `validation_error`, so a client cannot choose the server, and with it whose
  registry it reads or where its requests go.
- **Traces:** `internal/ipamhttp` and `internal/ipamconn` already carry
  `traceparent`, for the loopback and for HTTPS. A remote caller's header is
  used only to correlate, never for identity or authorization.
- **Dry run:** a `dry_run` request needs the same capability as the real action,
  because it answers the same question. It leaves no audit event.
- **Errors:** `forbidden` gets the code `access_denied`, and the transport
  classes get `server_unreachable`, `server_untrusted`, and
  `server_response_invalid`. All exit 2, and none is `retryable`, which is about
  live reads.
- **Comparisons:** remote and local output match once the fields `__schema`
  publishes and the error object's `trace_id` are masked. The envelope is
  version 2 in both.

### Goa changes

- The error `forbidden` (HTTP 403) is added to every method through an
  API-level `Error` plus a `Response("forbidden", StatusForbidden)` in each
  HTTP block.
- New methods: `list_live_routes`, `list_identities`, and `whoami`.
  `whoami` is the one method that does not return `forbidden`.
- `plan.live_tailnet` becomes optional.
- No goa `Security` schemes. Authentication happens at the network layer,
  and an empty scheme would mislead OpenAPI readers. The OpenAPI
  description says so instead.

### Serve command and lifecycle

Flags: `--registry` (required, `sqlite://` only), `--service`
(`svc:tsipam`), `--hostname` (`tsipam`), `--advertise-tags` (required),
`--capability` (`github.com/rshade/tsipam/cap/ipam`), `--state-dir`
(`/var/lib/tsipam/tsnet`), and `--health-listen` (empty by default).
Startup logs the capability name and the Service name.

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
- **No sidecars.** The pod runs only `tsipam serve`, because anything that
  shares its network namespace can forge a PROXY header (see "The
  local-process gap").
- **Define the Service first** in the admin console (`svc:tsipam`, TCP
  443). Advertising an undefined Service does nothing visible.
- **Policy snippet:** `tagOwners` for `tag:tsipam`; `autoApprovers.services`
  for `svc:tsipam` approving `tag:tsipam`; a network grant with
  `dst: ["svc:tsipam"]` and `ip: ["tcp:443"]`, needed even in a tailnet
  that allows `*`; and app grants of the capability whose `dst` is
  `svc:tsipam`.
- **Troubleshooting order**, after tail-watch's: the name does not resolve
  (Service undefined or host not approved); the connection times out
  (approval pending or no network grant); `tsipam whoami` reports no
  grants (the app grant targets something other than `svc:tsipam`).

## Risks / Trade-offs

- [The PROXY header's source is not the caller, or `WhoIsForService` does
  not return Service grants on a tsnet host] → The live test in task 1.2
  checks both before any other work. Failing either reopens the design.
- [A local process forges a PROXY header] → Not closed by this change. The
  deployment forbids sidecars and the authorization reference states the
  trust boundary. See "The local-process gap".
- [`tailscale.com` is a large dependency: govulncheck exposure, build time,
  Windows and macOS cross-compiles] → Task 1.1 adds it and runs
  `mise run cross`, `govulncheck`, and `license-check` first. If Windows
  fails, `serve` gets a build constraint and the client stays portable.
- [One replica means a few seconds of unavailability on restart] → Accepted.
  tsipam is not in the data path, and clients report `server unreachable`
  with exit 2, which CI retries.
- [Capability globs are easy to get wrong] → `authz` is a pure function with
  table tests, and denial messages name the missing action and scope kind.
- [A lookup on every request adds latency] → The tsnet `LocalClient` runs
  in process, and the live test records the lookup time. A cache of at most
  5 seconds is the fallback (see "Identity on every request").
- [A grant aimed at the node tag instead of `svc:tsipam`] → It may or may
  not appear in the Service-scoped map. The docs lead with `svc:tsipam` as
  the grant destination, and `tsipam whoami` shows what the server
  actually received.
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

- Should a server refuse to start when it detects other processes in its
  network namespace? Detection is unreliable across runtimes, so the docs
  carry the rule for now.

- Should node state move to a Kubernetes Secret (`kubestore`) so the
  volume holds only data? This is deferrable, since the volume already
  exists.
- Which registry should publish the container image (GHCR through
  goreleaser, or `ko`)? This is deferrable to the image follow-up.
