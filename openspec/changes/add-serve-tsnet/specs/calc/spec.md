# Calc Specification Delta

## ADDED Requirements

### Requirement: Remote next-free reads live routes from the server

In remote mode, `calc next-free` SHALL read subnet-pool reservations and
live advertised routes from the server, so the client needs no Tailscale
API credentials. It SHALL return the same blocks local mode would return
for the same registry and live state. `calc aggregate --from-registry` SHALL
read reservations from the server. Stateless calc commands (`split`,
`info`, `aggregate` without `--from-registry`) SHALL ignore `--server` and
`TSIPAM_SERVER` and send no request.

#### Scenario: Remote next-free excludes a live route

- **WHEN** a pool's first child is reserved on the server and its second
  child is advertised by a live device, and a client with no API
  credentials runs
  `calc next-free 10.0.0.0/16 --size 24 --server https://tsipam.example.ts.net`
- **THEN** the command returns the third child CIDR

#### Scenario: Stateless calc never contacts a server

- **WHEN** `TSIPAM_SERVER` is set and an operator runs
  `calc split 10.0.0.0/16 --size 18`
- **THEN** the command prints the four children without any network
  request
