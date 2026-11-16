# Registry and live state

`tsipam` keeps a file of address-space intent and compares it with what
the tailnet is doing. This page explains why the file exists, what the
comparison can and cannot conclude, and where the tool stops and its
siblings begin.

## Reserve before you advertise

Tailscale learns about address space when a node advertises it. Until then
nothing in the control plane records that `10.20.0.0/16` belongs to the HQ
site or that site ID `7` is taken. The first person to advertise wins, and
overlap is discovered as an outage or as a finding in an audit.

The registry moves that decision earlier. A reservation is a claim made
before any node advertises, refused when it collides with an existing claim,
and reviewed in version control like the policy file beside it. The tool is
read-only toward the tailnet: it never approves routes, creates services, or
edits policy. Its side effects are confined to one HuJSON file.

Four things can be reserved, because those are the four scarce namespaces a
tailnet hands out: 4via6 site IDs (a 16-bit field), subnet CIDR pools,
per-service IPv4 addresses, and per-device UDP listen ports. On-premises
CIDR planning that never touches the tailnet is deliberately out of scope.

## What a comparison means

`plan` and `check` read the registry once and the API twice (devices, then
Services), then match each reservation against the evidence:

| Reservation | Matches when |
| --- | --- |
| Site ID | Its numeric ID appears in an advertised 4via6 route. |
| Subnet pool | It contains an entire advertised route of the same address family. |
| Service address | The reserved IPv4 address belongs to the reserved `svc:` identity. |

The subnet rule is asymmetric on purpose. A reserved `/16` matches a live
`/24` inside it, because the reservation is a pool and the route is a draw
from the pool. A reserved `/24` does not match a live `/16` around it: the
live route is claiming more than was recorded, and that is exactly the drift
worth surfacing.

4via6 routes contribute two things, their encoded site ID and their actual
IPv6 prefix. They do not imply that the embedded IPv4 range is advertised,
because on the wire it is not.

A `matched` row therefore says one thing: the registry and the API agree on
this allocation. It does not say the route is approved, that traffic flows,
that the router is healthy, or that the site label is true; labels are
annotations the registry owner chose. The two API reads are consecutive
observations, not a snapshot, so a change between them can appear as drift
once.

UDP ports are never compared. The API does not expose a node's listen port,
so `comparison.excluded` names `udp_port` on every report rather than
silently reporting it as matched.

## Why `plan` and `check` differ only in exit code

Both print the same report. `plan` exits `0` after any complete comparison:
it is for reading. `check` exits `1` when any row is not `matched`: it is for
gating. The split lets a CI job use `check` while an operator uses `plan`
without reasoning about whether a non-zero exit was a finding or a failure.
Both exit `2`, with no report, when the comparison could not be completed, so
a consumer never has to distinguish "no drift" from "could not look".

## Policy checks are a different comparison

`check-policy` compares the registry with the policy file, not with the
tailnet. It resolves every `ipsets` definition statically and asks two
questions: does each authored CIDR sit inside one reserved pool, and is each
pool referenced by something. Adjacent pools do not combine to satisfy a
broad authored CIDR, because the policy author claimed one range and the
registry recorded two; that is worth a look even when the addresses add up.

It runs offline, which is why it belongs on every pull request while `check`
belongs after merge.

## Where the siblings take over

- `tail-acl` authors policy. Its `ipset` command labels or edits address
  space in the policy text; it has no allocation state and no collision
  detection. `tsipam` reads the policy it writes.
- `tailnet-audit` inspects what is live for its own sake: overlapping routes
  and colliding site IDs among advertised routes, regardless of intent.
  `tsipam` compares live state with intent and leaves live-only analysis
  there.

The registry sits between them, recording what was decided so both the
policy and the audit have something to be checked against.

## Why the transport is HTTP

Every registry command, including a fully local reserve, runs through the
same generated HTTP client and server that a remote deployment would use,
over an ephemeral Unix socket that lives for one request. Nothing is
daemonized. The point is that pointing the client at a shared service later
is an endpoint change, not a rewrite, and that the request and response
encoding is exercised on every invocation rather than only in a deployment
nobody has built yet.
