# Implementation Tasks

## 1. Comparison and collection

- [x] 1.1 Add one-read validated registry loading and pure comparison for all three domains; verify unit cases
  for each state, containment, IPv6, 4via6, duplicate evidence, conflicts, empty input, and invalid live
  records.
- [x] 1.2 Add combined tscli device/Services collection with bounded requests and safe failures; verify
  API-key/OAuth and incomplete-source behavior against a local HTTP server.

## 2. Transport and CLI

- [x] 2.1 Add the goa plan endpoint and regenerate the API with the pinned generator; verify generated-client
  transport, registry preservation, and error propagation.
- [x] 2.2 Add plan/check, report rendering, sanitization, and discovery; verify both formats and flag
  spellings, exit 0/1/2, alias selection, keyed sanitization, output errors, and reflection guards.

## 3. Documentation and verification

- [x] 3.1 Document commands, matching rules, permissions, limits, and tailnet-audit authority; deliberately
  update and inspect schema fixtures, then pass Markdown and OpenSpec validation.
- [x] 3.2 Run module build/test/lint and repository test/lint/cross gates; complete standards/spec review and
  scout, and resolve confirmed findings before commit.
