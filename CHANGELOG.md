# Changelog

## 0.1.0 (2026-09-11)


### Features

* add tail-ipam module and sanitize output levels ([#229](https://github.com/rshade/tailscale-utils/issues/229)) ([5d458a7](https://github.com/rshade/tailscale-utils/commit/5d458a7ea2c7066a1eab3a6c2f4c81e8029f49b4)), closes [#224](https://github.com/rshade/tailscale-utils/issues/224)
* **repo:** adopt OpenSpec across all modules ([#316](https://github.com/rshade/tailscale-utils/issues/316)) ([c321343](https://github.com/rshade/tailscale-utils/commit/c32134374d6553bb1684dc6c7f07bb08a081ca2a))
* **tail-ipam:** add calc info block-facts subcommand ([#300](https://github.com/rshade/tailscale-utils/issues/300)) ([00273dd](https://github.com/rshade/tailscale-utils/commit/00273ddace76b6377333e7031c29bd5e8fc1525d)), closes [#250](https://github.com/rshade/tailscale-utils/issues/250)
* **tail-ipam:** add IPv4 CIDR aggregation ([#328](https://github.com/rshade/tailscale-utils/issues/328)) ([bd4f27a](https://github.com/rshade/tailscale-utils/commit/bd4f27a9934fd178b83a535bdf4b3fa9d6979d2d)), closes [#252](https://github.com/rshade/tailscale-utils/issues/252)
* **tail-ipam:** add IPv4 next-free allocation ([#326](https://github.com/rshade/tailscale-utils/issues/326)) ([89234b3](https://github.com/rshade/tailscale-utils/commit/89234b3129588fe473c1223e4f5e67a53135a016)), closes [#256](https://github.com/rshade/tailscale-utils/issues/256)
* **tail-ipam:** add IPv6 calc info ([#327](https://github.com/rshade/tailscale-utils/issues/327)) ([da2b61b](https://github.com/rshade/tailscale-utils/commit/da2b61b5df6d32888200ff56ec81328aa2cf35eb)), closes [#251](https://github.com/rshade/tailscale-utils/issues/251)
* **tail-ipam:** add per-device UDP port allocation registry ([#305](https://github.com/rshade/tailscale-utils/issues/305)) ([1a7b90b](https://github.com/rshade/tailscale-utils/commit/1a7b90b4ea9b3296d91e6e88b13a821377e20a24)), closes [#240](https://github.com/rshade/tailscale-utils/issues/240)
* **tail-ipam:** add per-service /32 address reservations ([#301](https://github.com/rshade/tailscale-utils/issues/301)) ([0d706d8](https://github.com/rshade/tailscale-utils/commit/0d706d814014ee2a103c8499821f2653126af33a)), closes [#237](https://github.com/rshade/tailscale-utils/issues/237)
* **tail-ipam:** add registry plan and check ([#341](https://github.com/rshade/tailscale-utils/issues/341)) ([79c22fb](https://github.com/rshade/tailscale-utils/commit/79c22fbf3bd376cecde5e13f064f60e7aec52ae1))
* **tail-ipam:** add reservation ownership metadata ([#294](https://github.com/rshade/tailscale-utils/issues/294)) ([122ced5](https://github.com/rshade/tailscale-utils/commit/122ced53b872092aaed16ea7ce5691773097c747)), closes [#289](https://github.com/rshade/tailscale-utils/issues/289)
* **tail-ipam:** aggregate subnet pools from registry ([#339](https://github.com/rshade/tailscale-utils/issues/339)) ([fec468d](https://github.com/rshade/tailscale-utils/commit/fec468dc0b2a4ab4fd1d1a7fe868b16cd1a481ca))
* **tail-ipam:** calc split for IPv4 subnet division ([#259](https://github.com/rshade/tailscale-utils/issues/259)) ([f35e2dc](https://github.com/rshade/tailscale-utils/commit/f35e2dc87e3fd03e65b3611f849cbdf9e47fdea2)), closes [#248](https://github.com/rshade/tailscale-utils/issues/248)
* **tail-ipam:** check policy against subnet reservations ([#342](https://github.com/rshade/tailscale-utils/issues/342)) ([f8389c5](https://github.com/rshade/tailscale-utils/commit/f8389c5655cf00cf7a6e28a0d847ca69a6b8978a))
* **tail-ipam:** complete the AX shipping contract ([#242](https://github.com/rshade/tailscale-utils/issues/242)) ([2dbbf59](https://github.com/rshade/tailscale-utils/commit/2dbbf59e3ea080ce0850980f6a07d09eb2703564)), closes [#232](https://github.com/rshade/tailscale-utils/issues/232)
* **tail-ipam:** extend calc aggregate to IPv6 ([#332](https://github.com/rshade/tailscale-utils/issues/332)) ([4903a4a](https://github.com/rshade/tailscale-utils/commit/4903a4ae30612a56f1647d8a0939ca3f7e4b1a21)), closes [#253](https://github.com/rshade/tailscale-utils/issues/253)
* **tail-ipam:** extend calc next-free to IPv6 ([#334](https://github.com/rshade/tailscale-utils/issues/334)) ([8eecc12](https://github.com/rshade/tailscale-utils/commit/8eecc1255d1206c57df1d5c66ed8bf7060d193d3))
* **tail-ipam:** extend calc split to IPv6 ([#269](https://github.com/rshade/tailscale-utils/issues/269)) ([be2ceba](https://github.com/rshade/tailscale-utils/commit/be2cebab1ab28f127618f90bd8abca3c382b5a6b)), closes [#249](https://github.com/rshade/tailscale-utils/issues/249) [#261](https://github.com/rshade/tailscale-utils/issues/261)
* **tail-ipam:** extend registry to subnet CIDR pools ([#266](https://github.com/rshade/tailscale-utils/issues/266)) ([5ae1e9a](https://github.com/rshade/tailscale-utils/commit/5ae1e9a60ccc42798a70b97da8c5c7d09193fa5d)), closes [#236](https://github.com/rshade/tailscale-utils/issues/236)


### Bug Fixes

* **deps:** update go4.org/netipx digest to 3075585 ([#344](https://github.com/rshade/tailscale-utils/issues/344)) ([a8a768f](https://github.com/rshade/tailscale-utils/commit/a8a768fcc55e95df9045f8d215a65dcd92f90a80))
* **deps:** update module github.com/jaxxstorm/tscli to v0.10.0 ([#265](https://github.com/rshade/tailscale-utils/issues/265)) ([eaedb2b](https://github.com/rshade/tailscale-utils/commit/eaedb2bfee59e94d41c1cb6f375537c61d38b002))
* **tail-ipam:** exit 2 when pool exhausted in calc next-free ([#356](https://github.com/rshade/tailscale-utils/issues/356)) ([cfa657c](https://github.com/rshade/tailscale-utils/commit/cfa657c5e167929b99aa0075e1bef0214c3cbe94)), closes [#347](https://github.com/rshade/tailscale-utils/issues/347)
* **tail-ipam:** harden registry load, reserve, and release paths ([#304](https://github.com/rshade/tailscale-utils/issues/304)) ([327c7d9](https://github.com/rshade/tailscale-utils/commit/327c7d9638f3a74a3ef13cba9e65f9c06cbaec51))
* **tail-ipam:** name tailnet and directory in registry errors ([#351](https://github.com/rshade/tailscale-utils/issues/351)) ([7c0b462](https://github.com/rshade/tailscale-utils/commit/7c0b46201a9d1e0c7217a7944b3d3c55f392106e))
* **tail-ipam:** sanitize escaped site names in messages ([#267](https://github.com/rshade/tailscale-utils/issues/267)) ([4677e65](https://github.com/rshade/tailscale-utils/commit/4677e65d1b8a2fb9b80020641c37c47d64b03d56)), closes [#262](https://github.com/rshade/tailscale-utils/issues/262)
