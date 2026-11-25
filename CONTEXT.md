# tsipam Context & Boundaries

## Core Architectural Identity

`tsipam` plans Tailscale address space before it is used. It keeps a
registry of declared intent: 4via6 site IDs, subnet CIDR pools, per-service
`/32` reservations for Tailscale Services, and per-device UDP listen ports.
It compares that intent with what the live tailnet advertises. It is the
planning half of a pair. `tailnet-audit` owns collision detection in what is
already live, and `tsacl` owns policy authoring. `tsipam` duplicates neither.

It is design-first on `goa`. `design/` is the single source of truth for the
`ipam` service's types, validation, and `/tailnet/{tailnet}/ipam/...` paths,
and `gen/` is generated from it. Every command, even a fully local one, runs
the generated HTTP server and client over a private Unix socket for the
length of one invocation (`internal/loopback`). A local call and a remote
call therefore share one transport, encoding, and validation path. The
planned `tsipam serve` puts that same generated server on the tailnet as a
Tailscale Service, and remote mode swaps the Unix socket for an HTTPS client.

## Technical Boundaries ("Hard No's")

Crossing one of these is a bug, not a trade-off.

- **Never writes to the tailnet.** The only Tailscale API calls are device
  and Service listings. `tsipam` writes nothing but its own registry: the
  HuJSON file and its lock, or the SQLite database. It only reads the policy
  file `check-policy` compares. `tsipam serve` joins the tailnet as a tagged
  node and advertises `svc:tsipam`, but the operator defines that Service,
  its tags, and its grants; `tsipam` never edits policy, Service
  definitions, routes, or devices.
- **No daemon, except `tsipam serve`.** Every other command leaves nothing
  running: no listener, no PID file, no supervision. `tsipam serve` is the
  one long-running process (decided 2026-11-20, specified in the
  `add-serve-tsnet` OpenSpec change), and it holds to these conditions:
  - It starts only when run explicitly. No command spawns it, and a command
    talks to it only when given `--server` or `TSIPAM_SERVER`. Local mode
    stays the default.
  - It serves every goa endpoint of the `ipam` service, plus `/metrics` and
    the read-only web UI, on one mux reachable only through the Tailscale
    Service. The opt-in `--health-listen` endpoint is the one host listener,
    and it serves health and nothing else.
  - Every request is identified from the tailnet (`WhoIsForService`) and
    authorized by a grant app capability. No matching grant means deny;
    nothing is trusted from HTTP headers.
  - It runs one replica against a SQLite registry and refuses `file://`.

  `tsipam mcp-server` is not a daemon: an MCP client starts it as a child,
  it speaks only over that client's stdio, opens no listener, and exits when
  the client closes the stream.
- **Live state never writes declared intent on its own.** `plan` and `check`
  exist to catch the wire drifting from the registry. Anything that copies
  live state into the registry (adopt-from-live) needs an explicit decision
  first.
- **Never edit `gen/` by hand.** Change `design/`, run `mise run gen`, and
  commit both. CI's `gen-check` fails on drift.
- **No reimplemented policy or address logic.** Policy parsing,
  sanitization, HuJSON editing, and CIDR arithmetic come from
  `github.com/rshade/tsacl` (`pkg/policycore/...`, `pkg/ipcalc`). A gap there
  is fixed in `tsacl`, not copied here.
- **Compatibility contracts change only on purpose.** Commands, flags, exit
  codes, the JSON envelope, and the `<tailnet>.ipam.hujson` format are
  contracts. A change to one goes through OpenSpec, and a breaking change is
  `feat!`.
- **Output channels are never mixed.** Results go to stdout, as human text or
  the versioned JSON envelope. Logs and diagnostics go to stderr. stdout
  stays parse-clean.
- **No new personal-repo dependencies.** There are two deliberate exceptions.
  `github.com/jaxxstorm/tscli` is the Tailscale API client (and through it
  `viper`). `github.com/rshade/ax-go` is the CLI runtime: the root package and
  `mcp` in `cmd/tsipam` only, and `axtest` in tests only. It brings the
  OpenTelemetry SDK, the MCP Go SDK, and zerolog, and it has one maintainer.
  `goa` is a deliberate framework dependency. Adding another needs the same
  explicit decision.
- **No real identities in the repo.** Fixtures use `example.com` and
  `example.ts.net`. CI's gitleaks rule rejects any other MagicDNS name.

## Data Source of Truth

| Data | Source | Owner |
| --- | --- | --- |
| Declared intent | `<registry-dir>/<tailnet>.ipam.hujson`, comment-preserving HuJSON (the default), or a SQLite database (`sqlite://<path>`, planned) | `tsipam` (`internal/registry`) |
| Live use | Tailscale API device and Service listings, read through `tscli` | The tailnet |
| Policy membership | The tailnet policy file passed to `check-policy` | The operator |
| Caller identity and permissions (`serve` only) | `WhoIsForService` and grant app capabilities | The tailnet policy |

`{tailnet}` is a routing and storage key, not necessarily the live tailnet
name. It resolves from `--tailnet`, then `$TAILSCALE_TAILNET`, then the
literal `local`.

## Interaction Model

- **Inbound:** the `tsipam` CLI. Flags or environment variables supply
  credentials (`TAILSCALE_API_KEY` or an OAuth client) for the live commands;
  `calc` and the registry commands need none. An MCP client on the same
  machine reaches the same commands as tools over the stdio of
  `tsipam mcp-server`. Planned: HTTPS requests from tailnet callers to
  `tsipam serve` through `svc:tsipam`.
- **Outbound:** read-only Tailscale API calls; reads and writes of the local
  registry; stdout and stderr per the AX conventions in
  [`docs/reference/ax-conventions.md`](docs/reference/ax-conventions.md).
  Planned: in remote mode, HTTPS requests to the server instead of the local
  registry and API.
- **Internal:** CLI, then the goa-generated client, then a Unix socket (or
  HTTPS to `tsipam serve` in remote mode), then the generated server, then
  `internal/ipamsvc`, then `internal/registry`.

## Verification

Ask these of any proposed feature:

1. Does it write to the tailnet, or keep a process running after the command
   exits? If so, it violates a hard no, unless it is `tsipam serve` acting
   within the conditions above.
2. Does it detect collisions in what is already live, or author policy? If
   so, it belongs in `tailnet-audit` or `tsacl`.
3. Does it change a command, flag, exit code, the JSON envelope, or the
   registry format? If so, it needs an OpenSpec change first.
4. Does it need policy parsing or CIDR arithmetic that `tsacl` lacks? If so,
   add it to `tsacl` and consume a release.
