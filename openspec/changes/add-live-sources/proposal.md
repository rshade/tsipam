# Proposal: Live sources beyond the tailnet

## Why

`add-address-spaces` lets a registry hold on-prem and cloud space, but nothing
checks that space against reality: every unmapped pool is `external`. A plan for
the whole network needs the same drift gate for a VPC that the tailnet has today.

## What Changes

- **Sources.** A named space can declare one or more live sources. A source reads
  one kind of network and returns the prefixes in use there. Sources are
  read-only, compiled in, and hold no credentials.
- **`tsipam source add|remove|list`** manages the declarations, which are stored
  in the space.
- **`plan` and `check`** read every declared source and compare its prefixes with
  the space's pools, using the existing states. `--only` limits a run to the
  tailnet or to named spaces and discloses what it left out, so a space can be
  checked without Tailscale credentials.
- **`subnet allocate` and `calc next-free`** count a source's prefixes as taken.
- **First source: `aws-vpc`.** It reads VPC or subnet CIDRs from one region with
  the standard AWS credential chain and two read permissions.
- **Roadmap.** Cloud VPCs come first, then DNS and DHCP, routers and firewalls,
  and IPAM systems. Out-of-process plugins, and any write to a source, stay
  parked until a plugin boundary exists.

A registry that declares no source keeps its output, exit codes, and credential
requirements.

## Capabilities

### New Capabilities

- `live-sources`: declaring read-only live sources on a space, reading them, and
  comparing their prefixes with its pools.
- `aws-vpc-source`: the AWS VPC source's settings, evidence, credentials, and
  failures.

### Modified Capabilities

- `plan-check`: the tailnet becomes one of several selectable origins.
- `subnet`: allocation in a space with sources reads them.
- `calc`: `next-free` does the same.
- `schema`: the catalog lists the source commands and `--only`.

## Impact

- **Depends on** `add-address-spaces`.
- **Overlaps** `add-serve-tsnet`, which modifies the same `plan-check`
  requirement and moves live reads to the server. Whichever is archived second
  must rebase that delta. Doing this one first is simpler, because the server then
  inherits one collection path.
- **Code.** New `internal/sources` and `internal/sources/awsvpc`; `internal/planner`,
  `internal/ipamsvc`, both stores, `design/` and `gen/`, and `cmd/tsipam`.
- **Dependencies.** `aws-sdk-go-v2` (`config` and `service/ec2`), Apache-2.0. A
  minimal stripped linux/amd64 program grew from 5.9 MB to 10.1 MB with it; `tsipam`
  is 22.8 MB today. It is not a personal-repo dependency.
- **Security.** Settings hold no secrets, but they choose which account and region
  are read with the caller's credentials. Under `add-serve-tsnet` those are the
  server's, so declaring a source there must need the `admin` action.
