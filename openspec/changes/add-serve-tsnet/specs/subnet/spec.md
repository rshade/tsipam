# Subnet Specification Delta

## ADDED Requirements

### Requirement: Remote allocation collects live routes on the server

In remote mode, `subnet allocate` SHALL let the server collect the live
advertised routes it counts as taken, inside the server's handling of the
request. The client SHALL NOT read devices and SHALL NOT send live routes,
and the server SHALL reject client-supplied live routes as invalid input.
If the server cannot complete its live read, nothing SHALL be written and
the client SHALL exit 2.

#### Scenario: The server's live read excludes an advertised route

- **WHEN** the pool's first child is advertised by a live device and a
  client runs `subnet allocate 10.0.0.0/16 --size 24 lab --server https://tsipam.example.ts.net`
- **THEN** the server reserves the second child and the client reports it

#### Scenario: A failed server live read writes nothing

- **WHEN** the server's Tailscale API read fails during a remote allocation
- **THEN** the client exits 2 with empty stdout and `subnet list` shows no
  new pool
