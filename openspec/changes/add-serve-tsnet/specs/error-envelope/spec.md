# Error Envelope Serve Delta

## ADDED Requirements

### Requirement: Remote failures have codes

The documented set of `error_code` values SHALL also include `access_denied`,
`server_unreachable`, `server_untrusted`, and `server_response_invalid`. Each
SHALL exit 2 and SHALL NOT be `retryable`.

#### Scenario: Each remote failure has its code

- **WHEN** a remote command is denied, cannot reach the server, fails the TLS
  check, or gets a response that is not part of the API
- **THEN** it exits 2 with `access_denied`, `server_unreachable`,
  `server_untrusted`, or `server_response_invalid`
