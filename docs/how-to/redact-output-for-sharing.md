# Redact output for sharing

Use this when a `tail-ipam` report has to leave the team: a support ticket, a
public issue, a slide. Sanitization replaces identities with placeholders after
the real operation has completed, so the command's effect and exit code are
unchanged.

## Turn it on

Per command:

```bash
tail-ipam check --registry-dir /etc/tail-ipam --tailnet production --sanitize identity
```

For a whole session or CI job, set the environment variable. It overrides
`--sanitize` and announces itself on stderr so a reader knows the output is
redacted:

```bash
export TAILSCALE_SANITIZE=identity
tail-ipam subnet list
# stderr: tail-ipam: TAILSCALE_SANITIZE=identity is active; output is sanitized
```

`strict` is accepted for flag compatibility with the other tools in this
repository and is currently equivalent to `identity` for `tail-ipam`. `off`
disables sanitization.

## What changes and what does not

Redacted, on stdout and stderr alike:

- Tailnet names and registry aliases
- Site labels, service identities, reservation owners
- Device names, addresses, advertised routes
- Reserved subnet CIDRs, reserved service addresses
- Registry directory paths in error messages
- Ipset names and CIDRs in `check-policy` output

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
