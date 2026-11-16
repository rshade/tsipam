# Changelog

## 0.1.0 (2026-11-16)


### ⚠ BREAKING CHANGES

* publish tail-ipam as the tsipam module. Existing `tail-ipam` users must
  update:
  * the module path, from `github.com/rshade/tailscale-utils/tail-ipam` to
    `github.com/rshade/tsipam`;
  * the binary, from `tail-ipam` to `tsipam` (`go install
    github.com/rshade/tsipam/cmd/tsipam@latest`);
  * the smoke variables, from `TAIL_IPAM_SMOKE_{BINARY,DIST,LIVE}` to
    `TSIPAM_SMOKE_*`;
  * anything that parses `--version` or the sanitize banner, which now begin
    with `tsipam`;
  * release tags, from `tail-ipam/vX.Y.Z` to `vX.Y.Z`.

  The `<tailnet>.ipam.hujson` registry format and file name are unchanged.

### Features

* publish tail-ipam as the tsipam module ([32aa7b0](https://github.com/rshade/tsipam/commit/32aa7b0ff7b823afa06c74f8c3503ef3294d4b09))


### Bug Fixes

* report the module version from go install ([edb6db9](https://github.com/rshade/tsipam/commit/edb6db9f063640af974c1f846c4402979b8115ff))
