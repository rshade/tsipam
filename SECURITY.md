# Security Policy

## Reporting a Vulnerability

Report suspected vulnerabilities privately through GitHub Security Advisories:

<https://github.com/rshade/tsipam/security/advisories/new>

Do not report vulnerabilities through public GitHub issues, pull requests, or
Discussions before coordinated disclosure.

Please include:

- The affected `tsipam` version, commit, or branch.
- A concise impact statement.
- Reproduction steps or a minimal proof of concept.
- Any relevant logs or output with API keys, tailnet names, and other personal
  data removed.
- Whether the issue is already public or being reported to other projects.

## Response Targets

The maintainer aims to:

- Acknowledge a complete private report within 72 hours.
- Provide an initial triage result within 7 calendar days.
- Send status updates at least every 14 calendar days until resolution or
  closure.

These are targets, not legal guarantees. Reports involving credential exposure,
sanitized output that still identifies a tailnet, or a registry write that
escapes the configured registry directory are prioritized.

## Supported Versions

`tsipam` is pre-v1.0. Patch releases (`0.x.PATCH`) are backward-compatible and
are the preferred vehicle for security fixes. Minor releases (`0.MINOR.0`) may
contain breaking changes.

| Version line | Security support |
| ------------ | ---------------- |
| Latest `0.x` minor | Supported |
| Earlier minors | Not supported; upgrade to the latest minor |
| `main` / unreleased | Best effort |

## Disclosure Policy

`tsipam` follows coordinated disclosure. Keep vulnerability details private
until a fix or documented mitigation is available, unless there is active
exploitation. The default embargo target is up to 90 days from acknowledgment,
adjusted by mutual agreement for severity, patch complexity, and downstream
impact.

## Scope Notes

- `tsipam` reads a Tailscale API key or OAuth credentials from the
  environment. Reports about secrets reaching logs, error output, or registry
  files are in scope.
- `tsipam` serves its API to itself over a private Unix socket. Reports about
  another local user reaching that socket are in scope.
