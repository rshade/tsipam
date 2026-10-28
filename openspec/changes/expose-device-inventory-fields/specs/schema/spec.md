# Spec Delta

## ADDED Requirements

### Requirement: Schema documents the device tag filter and tag redaction

`tail-ipam -schema` SHALL list a `tag` flag on `devices list`. The document's
`sanitization` text SHALL state that `identity` redacts device users and
hostnames while leaving device tags visible, and that `strict` also redacts
device tags. It SHALL NOT claim that `strict` and `identity` are equivalent
for every field.

#### Scenario: Devices list advertises the tag flag

- **WHEN** an operator runs `tail-ipam -schema`
- **THEN** the `devices list` command entry includes a flag named `tag`

#### Scenario: Sanitization text names the tag exception

- **WHEN** the schema document's `sanitization` field is read
- **THEN** it says device tags stay visible at `identity` and are redacted
  at `strict`
