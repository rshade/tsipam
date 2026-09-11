# Calc Next-Free Exit Code Delta

## MODIFIED Requirements

### Requirement: Calc next-free handles exhaustion and unsupported families

When no requested child is available in the pool, `calc next-free` SHALL
communicate exhaustion through either exit code 0 with a message on stderr
(Option 1) or exit code 2 with a message on stderr (Option 2). In both
cases, stdout SHALL be empty, allowing downstream commands to detect the
absence of a suggestion. Native IPv6 input SHALL be rejected as a usage
error that references the IPv6 follow-up issue.

**Decision Point**: The following scenarios are mutually exclusive.
Implementation will choose **Option 1** or **Option 2** per issue #347.

#### Scenario: Option 1 - Exhausted pool exits 0, message to stderr, empty stdout

- **WHEN** exclusions cover every requested child of the pool and Option 1
  is chosen
- **THEN** the command exits 0, writes nothing to stdout, writes "no free
  /SIZE block in POOL" to stderr, and returns a successful JSON envelope
  with empty free_blocks

#### Scenario: Option 1 - Shell composition fails with correct attribution

- **WHEN** the documented composition
  `tail-ipam calc next-free 10.20.0.0/16 --size 24 | tail-ipam subnet reserve ...`
  runs under Option 1 and the pool is exhausted
- **THEN** the pipe passes an empty stdin to `subnet reserve`, which fails
  with exit 2 and error "subnet CIDR must not be empty" (the pool
  exhaustion is clear from calc's stderr message)

#### Scenario: Option 2 - Exhausted pool exits 2, message to stderr, output operation failed

- **WHEN** exclusions cover every requested child of the pool and Option 2
  is chosen
- **THEN** the command exits 2 (output operation failed per
  ax-conventions), writes nothing to stdout, writes "no free /SIZE block
  in POOL" to stderr

#### Scenario: Option 2 - Shell composition fails with clear exit code

- **WHEN** the documented composition
  `tail-ipam calc next-free 10.20.0.0/16 --size 24 | tail-ipam subnet reserve ...`
  runs under Option 2 and the pool is exhausted
- **THEN** the pipe fails with calc's exit 2 before reaching
  `subnet reserve`, and stderr shows the exhaustion reason from calc (the
  allocation output operation failed)

#### Scenario: IPv6 waits for the follow-up capability

- **WHEN** an operator supplies an IPv6 pool
- **THEN** the command exits with a usage error that names IPv6 and issue #257
