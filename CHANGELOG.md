# Changelog

## [0.2.0](https://github.com/rshade/tsipam/compare/v0.1.0...v0.2.0) (2026-11-26)


### ⚠ BREAKING CHANGES

* add dry run and require approval to release ([#50](https://github.com/rshade/tsipam/issues/50))
* envelope JSON results and choose the format by terminal ([#46](https://github.com/rshade/tsipam/issues/46))
* run every command through the ax-go runtime ([#45](https://github.com/rshade/tsipam/issues/45))

### Features

* accept every positional input by flag ([#47](https://github.com/rshade/tsipam/issues/47)) ([adbfdfd](https://github.com/rshade/tsipam/commit/adbfdfdf525e5220eb28943cc14611ae1174ec61))
* add dry run and require approval to release ([#50](https://github.com/rshade/tsipam/issues/50)) ([2a0d897](https://github.com/rshade/tsipam/commit/2a0d8972071c2923fadfb629f840b6c4b30e3f6d))
* envelope JSON results and choose the format by terminal ([#46](https://github.com/rshade/tsipam/issues/46)) ([e2b68ac](https://github.com/rshade/tsipam/commit/e2b68ac12dfee901fb1267c997c345086003da14))
* **registry:** add sqlite backend and registry history ([#56](https://github.com/rshade/tsipam/issues/56)) ([76575a7](https://github.com/rshade/tsipam/commit/76575a7f9cb279439244416fe298d93996eb2b74))
* run every command through the ax-go runtime ([#45](https://github.com/rshade/tsipam/issues/45)) ([9e99d50](https://github.com/rshade/tsipam/commit/9e99d50caf61716c4ab02335aab3dcd7b8f1304c))
* serve the commands to an MCP client over stdio ([#48](https://github.com/rshade/tsipam/issues/48)) ([96a6280](https://github.com/rshade/tsipam/commit/96a6280f732b67fbae1a4f35c7aebdd19a0eac47))


### Bug Fixes

* **deps:** roll up the open Renovate updates ([#39](https://github.com/rshade/tsipam/issues/39)) ([8d25076](https://github.com/rshade/tsipam/commit/8d25076a7e670583b26ee7955c185b517c5ae6f2))
* **deps:** update sqlite to v1.63.0 ([#58](https://github.com/rshade/tsipam/issues/58)) ([70d3914](https://github.com/rshade/tsipam/commit/70d3914208b8fc8a20c0635be5605fa36756dccc))
* **deps:** update tsacl to v0.2.0 ([#64](https://github.com/rshade/tsipam/issues/64)) ([28e058d](https://github.com/rshade/tsipam/commit/28e058de04fa4b361c12e6216aafc0485fbedc25))
* print the same version from go install and release archives ([#2](https://github.com/rshade/tsipam/issues/2)) ([1ce364a](https://github.com/rshade/tsipam/commit/1ce364a7c0201b5f6df27286e29117380d6bf95b))
* **registry:** count IPv4-mapped prefixes as taken ([#57](https://github.com/rshade/tsipam/issues/57)) ([02edcab](https://github.com/rshade/tsipam/commit/02edcab68835362c705ab1bf0c78cf84c542228b))

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
