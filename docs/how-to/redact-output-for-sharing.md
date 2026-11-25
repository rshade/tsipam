# Redact output for sharing

Use this when a `tsipam` report has to leave the team: a support ticket, a
public issue, a slide. Sanitization replaces identities with placeholders after
the real operation has completed, so the command's effect and exit code are
unchanged.

## Turn it on

Per command:

```bash
tsipam check --registry-dir /etc/tsipam --tailnet production --sanitize identity
```

For a whole session or CI job, set the environment variable. It overrides
`--sanitize` and announces itself on stderr so a reader knows the output is
redacted:

```bash
export TAILSCALE_SANITIZE=identity
tsipam subnet list
# stderr: tsipam: TAILSCALE_SANITIZE=identity is active; output is sanitized
```

`strict` matches `identity`, except that device tags stay visible at
`identity` and are redacted at `strict`. `off` disables sanitization.

## What changes and what does not

Redacted, on stdout and stderr alike:

- Tailnet names and registry aliases
- Site labels, service identities, reservation owners
- Device names, addresses, advertised routes, enabled routes, users, and hostnames
- Reserved subnet CIDRs, reserved service addresses
- Registry URLs, directories, and database paths in error messages
- Actors, keys, and values in `registry history`
- Ipset names and CIDRs in `check-policy` output

`devices list` redacts device names, addresses, advertised routes, enabled
routes, users, and hostnames at `identity`. Device tags stay visible at
`identity`, because they are policy vocabulary, and are redacted at `strict`.
OS, `authorized`, and `ephemeral` stay visible at both levels.

Left as they are:

- Numeric site IDs and UDP port numbers
- `description`, `ticket`, and `reserved_at`
- States, counts, exit codes, and field names
- Output from `calc info`, `calc split`, and positional `calc aggregate`,
  which are arithmetic over the CIDRs you typed

`calc aggregate --from-registry` and `calc next-free` read the registry or
live routes, so their CIDRs are redacted.

A collision error keeps its shape, so the reader still sees that a subnet
overlapped another reserved for a different site, without learning which.

## Make placeholders stable

By default each run picks its own placeholders. To correlate two reports, or
to let a reader match a redacted device across `devices list` and `check`,
set a 64-hex-character key from your secret store:

```bash
export TAILSCALE_SANITIZE_KEY=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
```

Placeholders are scoped to the artifact's routing key. Registry commands use
the `--tailnet` alias; `devices list` uses the live `TAILSCALE_TAILNET`. Passing
an alias that differs from the live name therefore yields two placeholder
domains for one real tailnet, by design. Use the same key and the same
tailnet key when you need reports to line up.

## Check before you paste

Sanitization is exact-token replacement of values the tool knows about. It
does not scan free text: a site name that also appears inside a `description`
survives there. Read the output once before sharing it.
