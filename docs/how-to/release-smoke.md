# Smoke-test a tsipam release

Run this before merging a `chore(main): release tsipam x.y.z` pull request.
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
mise run smoke
```

This builds `tsipam` from the checkout and runs every offline section. It
takes a few seconds.

## Run it against the release archives

On a clean checkout of the commit the release PR will tag:

```bash
mise run smoke-release
```

The task does what `.github/workflows/release.yml` does: it creates a local
`v0.0.0-smoke` tag (never pushed, deleted on exit), runs
`goreleaser release --skip=publish`, then runs the suite against the host
archive it produced plus the artifact checks. Pass `SMOKE_TAG=v0.2.0` to stamp
the version the release will carry.

To test archives built elsewhere, point at their directory:

```bash
TSIPAM_SMOKE_DIST=dist mise run smoke
```

### After a release

A GitHub Release carries the six archives and `checksums.txt` but not
goreleaser's `metadata.json`. Download them and point the suite at the
directory:

```bash
gh release download vX.Y.Z --repo rshade/tsipam --dir <dir>
TSIPAM_SMOKE_DIST=<dir> mise run smoke
```

Without `metadata.json` the suite reads the version from the archive names
and the commit from the `vX.Y.Z` tag. Fetch tags first; on a
checkout without the tag the commit check is skipped with a note, and the
version check still runs. Add `TSIPAM_SMOKE_LIVE=1` under `esc run` to include the
live section.

## Run the live section

The live cases need `TAILSCALE_TAILNET` and either `TAILSCALE_API_KEY` or an
OAuth client, with read access to devices and Services. They skip unless
`TSIPAM_SMOKE_LIVE` is set. Inject credentials from your secret store rather than
exporting them in a shell you will keep using:

```bash
esc run <environment> -- env TSIPAM_SMOKE_LIVE=1 mise run smoke
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

`smoke/` is an external test package behind the `smoke` build tag,
so `mise run test` never picks it up and it never needs network access unless you
opt in. Add cases there when a command gains behavior the README promises.

## In CI

`.github/workflows/ci.yml` runs the offline section (`mise run smoke`) on every
pull request, so README examples and the CLI contract cannot drift apart.
`.github/workflows/release.yml` runs the suite with the artifact section
against goreleaser's `dist/` after the build and before any archive is
attached to the Release, so a bad archive is never published. The live
section stays opt-in.
