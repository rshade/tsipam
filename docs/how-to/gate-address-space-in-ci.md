# Gate address space in CI

Use `tsipam` in two stages: an offline policy check on every pull request
that touches the policy or the registry, and a credentialed registry check
after merge or on a schedule.

## Check pull requests offline

`check-policy` needs the policy file and the registry, nothing else. Install a
fixed `tsipam` release in the job and run:

```bash
tsipam check-policy policy.hujson --registry-dir . --tailnet production
```

It exits `0` when every authored ipset CIDR sits inside a reserved pool and
every pool is referenced, `1` when either direction drifts, and `2` when the
policy or registry cannot be read or resolved. Fail the job on anything but
`0`; an exit `2` is not a pass.

The check compares `ipsets` only. Grants, ACLs, site IDs, service addresses,
and UDP ports are out of its scope.

## Check the registry against the tailnet

Expose credentials only to a protected job:

```bash
export TAILSCALE_TAILNET=example.ts.net
export TAILSCALE_API_KEY="${TAILSCALE_API_KEY_FROM_SECRET_STORE}"
tsipam check --registry-dir . --tailnet production
```

`check` exits `1` when a reservation has no live counterpart or a live route,
site ID, or service has no reservation. Exit `2` means the comparison did not
complete (credentials, API, or registry), with no report on stdout. Each API
request has a 30-second timeout.

Whether `1` should fail the job depends on how you use the registry:

- Registry as intent: a `reserved_but_unadvertised` row is expected while a
  site is being built. Treat `1` as a warning and review the rows.
- Registry as inventory: any drift is a finding. Fail on `1`.

Either way, `unreserved_but_live` is the row to act on first. Something is
advertising address space nobody recorded.

## Keep evidence

Add `--format json` and capture stdout on its own; stderr carries diagnostics
and must not be mixed into the document:

```bash
tsipam check --registry-dir . --tailnet production --format json \
  > check.json 2> check.log
```

`comparison.entries` lists every row with its state and evidence, and
`comparison.drift_count` is the number of non-matching rows. The field names
are a compatibility surface; see
[JSON envelope](../reference/json-envelope.md).

When the artifact leaves the team, add `--sanitize identity` so device names,
addresses, routes, and the tailnet name are replaced by stable placeholders.
Set `TAILSCALE_SANITIZE_KEY` from the secret store so placeholders stay the
same run over run; see [Redact output for sharing](redact-output-for-sharing.md).

## A GitHub Actions shape

```yaml
jobs:
  policy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: tsipam check-policy policy.hujson --registry-dir . --tailnet production

  registry:
    needs: policy
    if: github.ref == 'refs/heads/main'
    runs-on: ubuntu-latest
    environment: tailnet-read
    env:
      TAILSCALE_TAILNET: example.ts.net
      TAILSCALE_API_KEY: ${{ secrets.TAILSCALE_API_KEY }}
    steps:
      - uses: actions/checkout@v4
      - run: tsipam check --registry-dir . --tailnet production --format json > check.json
      - uses: actions/upload-artifact@v4
        if: always()
        with: { name: registry-check, path: check.json }
```

Install `tsipam` from a pinned release archive in each job; see the
[tsipam README](../../README.md) for the release layout.
