# Add next-free allocation tasks

## 1. Command and output contract

- [x] 1.1 Add the `calc next-free` Cobra command, flags, IPv4 validation, and
  registry/device exclusion collection; verify command tests cover validation
  and source combinations.
- [x] 1.2 Add free-block fields and human/JSON rendering, including
  composable single-CIDR output and successful exhaustion output; verify
  byte-exact command output tests.

## 2. Documentation and integration

- [x] 2.1 Document IPv4 free-block planning, registry and live-route inputs,
  output modes, and shell composition in README; verify Markdown lint passes.
- [x] 2.2 Run focused tail-ipam tests and root repository gates to verify the
  linked policycore integration and unchanged CLI modules.
