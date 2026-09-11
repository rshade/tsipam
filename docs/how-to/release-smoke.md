# Smoke-test a tail-ipam release

Run this before merging a `chore(main): release tail-ipam x.y.z` pull request.
It executes a built binary against the contract the README documents and, when
asked, the release archives goreleaser would attach and the commands that talk
to a real tailnet.

## What it covers

| Section | What passes |
| --- | --- |
| Discovery | `--help` names the exit codes; `-schema` is valid JSON and lists every command; bad flags exit 2 |
| `calc` | The README's `info`, `split`, and `aggregate` examples verbatim; the over-limit `split` refusal exits 1 with no stdout |
| Registry | Reserve, idempotent re-reserve, collision (exit 1), invalid input (exit 2), list, release, release-as-no-op for all four allocation kinds; metadata echo and amend; ports scoped by site; comments survive rewrites; corrupt or missing registries exit 2 without a transport envelope |
| Sanitization | `TAILSCALE_SANITIZE=identity` redacts stdout and stderr, announces itself, leaves stateless `calc` output alone, and gives stable placeholders under a fixed key |
| `check-policy` | Drift exits 1 with both directions classified; consistency exits 0; missing, cyclic, or dynamic input exits 2 |
| Live (opt-in) | `devices list` human/JSON parity; `plan` on an empty registry, reserve everything it reports, `check` exits 0, a stale reservation flips it to 1; `next-free` single, `--count`, JSON, exhaustion, IPv6; with at least one advertised IPv4 route, the matching rules: `next-free` never offers it, a containing pool matches it, a narrower reservation does not |
| Artifacts (opt-in) | Six archives with verified checksums, each carrying `LICENSE`, `NOTICE`, `README.md`, and the binary; the host binary reports the tagged version and commit |

## Run it against a source build

```bash
make smoke
```

This builds `tail-ipam` from the checkout and runs every offline section. It
takes a few seconds.

## Run it against the release archives

On a clean checkout of the commit the release PR will tag:

```bash
make smoke-release
```

The target does what `.github/workflows/release.yml` does: it creates a local
`v0.0.0-smoke` tag (never pushed, deleted on exit), runs
`goreleaser release --skip=publish`, then runs the suite against the host
archive it produced plus the artifact checks. Pass `SMOKE_TAG=v0.2.0` to stamp
the version the release will carry.

To test archives built elsewhere, point at their directory:

```bash
make smoke SMOKE_DIST=tail-ipam/dist
```

## Run the live section

The live cases need `TAILSCALE_TAILNET` and either `TAILSCALE_API_KEY` or an
OAuth client, with read access to devices and Services. They skip unless
`SMOKE_LIVE` is set. Inject credentials from your secret store rather than
exporting them in a shell you will keep using:

```bash
esc run tailscale-phase-2/tailscale -- make smoke SMOKE_LIVE=1
```

The round-trip test reserves every live allocation it finds into a temporary
registry, and the matching-rules test needs one advertised IPv4 route between
`/20` and `/30`. On a tailnet with no routes both still pass or skip with a
message saying the rules were not exercised. A throwaway node is enough:
`tailscale up --advertise-routes=10.10.20.0/24` on an ephemeral, userspace
container makes every case run. The suite writes nothing to the tailnet.

## Read a failure

Each failure prints the command's exit code, stdout, and stderr. Two questions
settle most of them:

- Did the **contract** change on purpose? A different exit code, envelope
  field, or human column is a compatibility change; the README and the
  OpenSpec specs must change with it, and the assertion follows.
- Did the **README example** drift? The `calc` cases assert the README's
  expected output byte for byte; fix whichever side is wrong.

A failure in `TestRegistryFile` that shows `invalid response code 500` means
the binary predates the fix that renders service errors as their message;
release 0.1.0 has this and 0.1.1 does not.

## Where the tests live

`tail-ipam/smoke/` is an external test package behind the `smoke` build tag,
so `make test` never picks it up and it never needs network access unless you
opt in. Add cases there when a command gains behavior the README promises.
