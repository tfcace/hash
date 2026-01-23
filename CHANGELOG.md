# Changelog

## [0.1.2](https://github.com/tfcace/hash/compare/v0.1.1...v0.1.2) (2026-01-23)


### Bug Fixes

* **ci:** install X11 dev libraries for clipboard build ([e665a8e](https://github.com/tfcace/hash/commit/e665a8e55ab68f081b944f85f5cb999e87cd6b52))

## [0.1.1](https://github.com/tfcace/hash/compare/v0.1.0...v0.1.1) (2026-01-23)


### Features

* add !! shortcut for quick issue submission ([58dc805](https://github.com/tfcace/hash/commit/58dc80560ece6ff3fb781cdcc72037f8a401dd4b))
* add build script with version injection ([76fa01b](https://github.com/tfcace/hash/commit/76fa01b00b8086fe9601144b33f2752cc68bae05))
* add issue builtin for GitHub issue submission ([addb432](https://github.com/tfcace/hash/commit/addb4323c10217ca97dd9e8290ec836023011ead))
* add ldflags support for version injection ([8769415](https://github.com/tfcace/hash/commit/8769415f308bd0a3e8be218aedb208470d4f1e8d))
* add ldflags support for version injection ([0ba9e52](https://github.com/tfcace/hash/commit/0ba9e5209752a9bf3600a6e55532b4d5cdc04cf0))
* **agent:** add ghost text streaming for inline AI suggestions ([e0afb6c](https://github.com/tfcace/hash/commit/e0afb6c1f147a0d1e14dbcc1119f3a818435a141))
* capture stderr for issue reporting ([487c2fa](https://github.com/tfcace/hash/commit/487c2facba2e20405e92d215e1d83ef6a05a1121))
* **completion:** add fuzzy filtering flag to Router ([c74fe2c](https://github.com/tfcace/hash/commit/c74fe2cbb48ca10ddf19414c5daa2f27a82831df))
* **completion:** add fuzzy mode to FileCompleter ([6fbc54d](https://github.com/tfcace/hash/commit/6fbc54d37d31291cac6fe06f012d035219fb5299))
* **context:** wire context picker into editor and agent flow ([548e8f1](https://github.com/tfcace/hash/commit/548e8f17de1ee1cc6c91252ba595de40cc63f358))
* **editor:** improve helix mode UX with mode indicator and ESC fix ([4abd97b](https://github.com/tfcace/hash/commit/4abd97bbb1495741a73963fd019403a7716b4db8))
* **hash:** weekend drop ([9193d1f](https://github.com/tfcace/hash/commit/9193d1f07278fcfd61c16e3fb1937c7db10d9bc9))
* **shell:** add trace system and improve streaming UX ([9163d09](https://github.com/tfcace/hash/commit/9163d09e4cc7d044a37f24a4b576baee9404644f))
* **shell:** wire fuzzy completion config to router ([c19ca97](https://github.com/tfcace/hash/commit/c19ca97b4725486252103560580ae8017564cdee))


### Bug Fixes

* **agent:** send session/cancel on context cancellation ([58adf13](https://github.com/tfcace/hash/commit/58adf13f1d6702d1fb90975c254bffa655309476))
* **ci:** explicitly pass GITHUB_TOKEN to release-please ([ccadd62](https://github.com/tfcace/hash/commit/ccadd627c8ebc029d04b9b0cc0cfdd0b4625b1b7))
* **executor:** prevent PTY output stalls with configurable capture limits ([76a7707](https://github.com/tfcace/hash/commit/76a770700f7eb30c37e89a8b7398318951a378e9))
* **executor:** stop PTY stdin copy from consuming prompt input ([d63e941](https://github.com/tfcace/hash/commit/d63e9410553371585983bb18c3366cd5c7a682e5))
* **shell:** improve ghost text and confirmation UI error handling ([6cfb0c1](https://github.com/tfcace/hash/commit/6cfb0c1ab92a2dec165a2c64148d7eff4c159f6d))
