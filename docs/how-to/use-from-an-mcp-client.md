# Use tsipam from an MCP client

Use this when an agent should plan or reserve address space through tools
instead of a shell. `tsipam mcp-server` serves the commands to a Model Context
Protocol client on the same machine, over stdin and stdout.

## Decide what the client may do

The server gives the client the authority you already have in a shell:

- It can read any registry directory and policy file your user can read.
- It can write registries. Reserve and allocate need no approval; a release
  needs `yes` in that call.
- It can read devices and Services when `TAILSCALE_TAILNET` and credentials are
  in its environment. Leave them out to keep it offline.

It opens no network listener, so nothing else on the network reaches it. It
never writes to the tailnet.

Run it as a user, or in a container, that can reach only what the agent should.
If the results will be shared, set `TAILSCALE_SANITIZE=identity` in the server's
environment.

## Register the server

Most clients take a command and an environment:

```json
{
  "mcpServers": {
    "tsipam": {
      "command": "tsipam",
      "args": ["mcp-server"],
      "env": {
        "TAILSCALE_SANITIZE": "identity"
      }
    }
  }
}
```

The client starts the process, and the process exits when the client closes its
stdin. Nothing stays running.

## See which tools it serves

```bash
tsipam __schema --as=mcp | jq -r '.tools[].name'
```

Every leaf command except `check` is a tool, named by its path with hyphens:
`tsipam-site-id-reserve`, `tsipam-calc-split`, `tsipam-plan`, and so on. Each
tool's arguments are the command's flags, so a value you would type by position
has a name: `id`, `site`, `cidr`, `pool`, `address`, `service`, `port`, `device`,
or `policy-file`. See
[inputs by position or by flag](../../README.md#inputs-by-position-or-by-flag).

Pass `registry-dir` and `tailnet` in each call that uses the registry. The
default directory is the one the client started the server in, which is rarely
the one you want.

The tools carry the standard hints. The lists, `plan`, `check-policy`, and the
calculations are read-only. Reserve and release are marked destructive: a repeat
reserve can replace a reservation's metadata. Allocate is not, because it only
adds.

### Why `check` is absent

`check` fails on drift, and a failed call returns the error and no result, so an
agent would learn that there is drift but not where. `plan` makes the same
comparison and returns the report as a result. Read
`data.comparison.drift_count` in it.

`check-policy` has no such twin, so it is a tool. On drift it fails with
`policy_drift_detected` and the count in `context.drift_count`. Run the command
itself to see the rows.

## Read the results

A successful call returns the JSON document the command line prints; see
[JSON envelope](../reference/json-envelope.md). A failed call returns the error
object, with `isError` set, and the server keeps running; see
[Exit codes and error objects](../reference/exit-codes.md).

Branch on `error_code`. Three are worth handling by name:

| `error_code` | What the agent should do |
| --- | --- |
| `registry_collision` | Choose another key. Do not release the other holder's. |
| `confirmation_required` | Ask a person, then repeat the call with `yes`. |
| `pool_exhausted` | Choose a larger pool or a longer prefix. |

## Preview and approve writes

Any write accepts `dry-run`. It reports the outcome and changes nothing, even
beside `yes`:

```json
{ "id": "1", "registry-dir": "/etc/tsipam", "tailnet": "production", "dry-run": true }
```

A release without `yes: true` fails with `confirmation_required`. The server
never prompts and never supplies approval, and approval does not carry over: each
release needs its own `yes`. Configure the client to ask you before it calls a
tool marked destructive, because the client is what decides whether you see the
request.

## Keep identities out of results

Each call resolves its own tailnet and sanitization, so two calls never share
placeholders. A call may raise the level with `sanitize`, and cannot lower the one
`TAILSCALE_SANITIZE` sets.

`tsipam mcp-server` refuses `--sanitize`, `--dry-run`, `--yes`, and
`--idempotency-key`. A flag on the server would not reach its calls, so
`--dry-run` would not stop a call from writing.

Argument names are not redacted. A call with an argument the tool does not have
is refused before `tsipam` sees it, and the refusal repeats that name
(rshade/ax-go#354).

Set `TAILSCALE_SANITIZE_KEY` in the server's environment to keep placeholders the
same from call to call. See
[Redact output for sharing](redact-output-for-sharing.md).

## Check it by hand

This asks the server for its name and tools:

```bash
{
  echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"sh","version":"1"}}}'
  echo '{"jsonrpc":"2.0","method":"notifications/initialized"}'
  echo '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  sleep 1
} | tsipam mcp-server | jq -c '.result.serverInfo // [.result.tools[].name]'
```

The `sleep` matters. A pipe that closes stdin right after its last request can
lose the response; a real client keeps the stream open.

Logs go to stderr and never to stdout, which carries only protocol messages. Pass
`verbose` in a call to log its details.
