# Changelog

## [0.7.0](https://github.com/tfcace/hash/compare/v0.6.1...v0.7.0) (2026-06-20)


### Features

* **onboarding:** agent-aware first-run welcome ([#67](https://github.com/tfcace/hash/issues/67)) ([0de0b46](https://github.com/tfcace/hash/commit/0de0b46b1355627a57c2049233d713358192e97d))

## [0.6.1](https://github.com/tfcace/hash/compare/v0.6.0...v0.6.1) (2026-06-17)


### Dependencies

* bump charm.land/lipgloss/v2 from 2.0.0 to 2.0.4 ([#61](https://github.com/tfcace/hash/issues/61)) ([87404d8](https://github.com/tfcace/hash/commit/87404d867e888b1815b0a123a78b5d55a947307f))
* bump github.com/mattn/go-sqlite3 from 1.14.32 to 1.14.45 ([#64](https://github.com/tfcace/hash/issues/64)) ([3c78ed8](https://github.com/tfcace/hash/commit/3c78ed8e92f5eedc99b85da616b42762918d31c1))
* bump github.com/pelletier/go-toml/v2 from 2.2.4 to 2.4.0 ([#63](https://github.com/tfcace/hash/issues/63)) ([e2ac17f](https://github.com/tfcace/hash/commit/e2ac17f84a7fea710e92d7e85e5acea9c5bf7a4e))
* bump golang.org/x/term from 0.41.0 to 0.44.0 ([#62](https://github.com/tfcace/hash/issues/62)) ([a30840a](https://github.com/tfcace/hash/commit/a30840a42502bfb9cacbb81f11dc85b004159e80))

## [0.6.0](https://github.com/tfcace/hash/compare/v0.5.1...v0.6.0) (2026-06-12)


### Features

* turn-by-turn agent conversations, model selection, and zsh dialect ([#59](https://github.com/tfcace/hash/issues/59)) ([5d41fec](https://github.com/tfcace/hash/commit/5d41fec9290712cdfde0d0ab64e39852eef494b0))

## [0.5.1](https://github.com/tfcace/hash/compare/v0.5.0...v0.5.1) (2026-06-05)


### Bug Fixes

* **completion:** stabilize autocomplete interactions ([#57](https://github.com/tfcace/hash/issues/57)) ([0ab97a4](https://github.com/tfcace/hash/commit/0ab97a4dec044de5e9eb9ee947d78649b9836ff9))

## [0.5.0](https://github.com/tfcace/hash/compare/v0.4.3...v0.5.0) (2026-05-26)


### Features

* add !! shortcut for quick issue submission ([58dc805](https://github.com/tfcace/hash/commit/58dc80560ece6ff3fb781cdcc72037f8a401dd4b))
* add bash/zsh migration system with compatibility layer ([8474089](https://github.com/tfcace/hash/commit/847408985fca0c84e307c501a6d9a99b55542d79))
* add build script with version injection ([76fa01b](https://github.com/tfcace/hash/commit/76fa01b00b8086fe9601144b33f2752cc68bae05))
* add issue builtin for GitHub issue submission ([addb432](https://github.com/tfcace/hash/commit/addb4323c10217ca97dd9e8290ec836023011ead))
* add ldflags support for version injection ([8769415](https://github.com/tfcace/hash/commit/8769415f308bd0a3e8be218aedb208470d4f1e8d))
* add ldflags support for version injection ([0ba9e52](https://github.com/tfcace/hash/commit/0ba9e5209752a9bf3600a6e55532b4d5cdc04cf0))
* **agent:** add ghost text streaming for inline AI suggestions ([e0afb6c](https://github.com/tfcace/hash/commit/e0afb6c1f147a0d1e14dbcc1119f3a818435a141))
* **agent:** enable ACP tool calls ([#55](https://github.com/tfcace/hash/issues/55)) ([215d173](https://github.com/tfcace/hash/commit/215d173b7f6c05392cd26bba13b02651d2081175))
* alias/function tab-completion ([ec68874](https://github.com/tfcace/hash/commit/ec68874b6df35df861a1db8cbf690d88b8e3f226))
* capture stderr for issue reporting ([487c2fa](https://github.com/tfcace/hash/commit/487c2facba2e20405e92d215e1d83ef6a05a1121))
* **completion:** add fuzzy filtering flag to Router ([c74fe2c](https://github.com/tfcace/hash/commit/c74fe2cbb48ca10ddf19414c5daa2f27a82831df))
* **completion:** add fuzzy mode to FileCompleter ([6fbc54d](https://github.com/tfcace/hash/commit/6fbc54d37d31291cac6fe06f012d035219fb5299))
* **completion:** mask sensitive env var values in completion preview ([09d2d88](https://github.com/tfcace/hash/commit/09d2d882798be34f7fe4d0c10650176a41199f09))
* **context:** wire context picker into editor and agent flow ([548e8f1](https://github.com/tfcace/hash/commit/548e8f17de1ee1cc6c91252ba595de40cc63f358))
* delightful ux ([c36611e](https://github.com/tfcace/hash/commit/c36611ebd25c75dc0cb52dba221eb9d5a2c3723d))
* **editor:** improve helix mode UX with mode indicator and ESC fix ([4abd97b](https://github.com/tfcace/hash/commit/4abd97bbb1495741a73963fd019403a7716b4db8))
* **hash:** weekend drop ([9193d1f](https://github.com/tfcace/hash/commit/9193d1f07278fcfd61c16e3fb1937c7db10d9bc9))
* **shell:** add trace system and improve streaming UX ([9163d09](https://github.com/tfcace/hash/commit/9163d09e4cc7d044a37f24a4b576baee9404644f))
* **shell:** persistent interpreter with lazy starship and cd sync ([2eb1431](https://github.com/tfcace/hash/commit/2eb1431014746d520688aa47b6c8141d57542c87))
* **shell:** wire fuzzy completion config to router ([c19ca97](https://github.com/tfcace/hash/commit/c19ca97b4725486252103560580ae8017564cdee))


### Bug Fixes

* add environment variable tab-completion with value preview ([f07c236](https://github.com/tfcace/hash/commit/f07c23672333a42b664731362ba1f8e81b56cf36))
* **agent:** broken pipe on context cancelation ([0bcef00](https://github.com/tfcace/hash/commit/0bcef0025f028f085b29832393d8385a4abfe317))
* **agent:** parse arguments from command string in ACP config ([bbe838f](https://github.com/tfcace/hash/commit/bbe838f386178cf1e0f5250b1e4011b32d64cb2b))
* **agent:** send session/cancel on context cancellation ([58adf13](https://github.com/tfcace/hash/commit/58adf13f1d6702d1fb90975c254bffa655309476))
* **alias:** pass arguments when converting aliases into functions ([1bc4b43](https://github.com/tfcace/hash/commit/1bc4b4303968f4f072cca66c7eef7c9cda1bf302))
* **ci:** explicitly pass GITHUB_TOKEN to release-please ([ccadd62](https://github.com/tfcace/hash/commit/ccadd627c8ebc029d04b9b0cc0cfdd0b4625b1b7))
* **ci:** install X11 dev libraries for clipboard build ([e665a8e](https://github.com/tfcace/hash/commit/e665a8e55ab68f081b944f85f5cb999e87cd6b52))
* **ci:** prevent duplicate release PRs and fix Homebrew formula generation ([c67cc5b](https://github.com/tfcace/hash/commit/c67cc5beaa921a98d1cb7b4c907a6c76837d3029))
* copy agent response to system clipboard with feedback ([9110225](https://github.com/tfcace/hash/commit/91102252b9bbeff318649563fd5fa792328df3cd))
* disable ONLCR on PTY when stdout is a pipe ([8b7fc9c](https://github.com/tfcace/hash/commit/8b7fc9c69057a5aa7f5f2c09dd1aded6e9595d0f))
* **executor:** prevent PTY output stalls with configurable capture limits ([76a7707](https://github.com/tfcace/hash/commit/76a770700f7eb30c37e89a8b7398318951a378e9))
* **executor:** stop PTY stdin copy from consuming prompt input ([d63e941](https://github.com/tfcace/hash/commit/d63e9410553371585983bb18c3366cd5c7a682e5))
* handle agent startup failures gracefully ([67959bd](https://github.com/tfcace/hash/commit/67959bd113593dbb9c388140cffc1cc00b7592bf))
* improved handling of complex aliases when migrating from zsh ([#12](https://github.com/tfcace/hash/issues/12)) ([43ec193](https://github.com/tfcace/hash/commit/43ec1930122474292864c9e330e75a6f7469f29d))
* lint errors (gofmt, gosec, staticcheck) ([9b39a3d](https://github.com/tfcace/hash/commit/9b39a3d73ecfee27d52c1996689f7306eea4126e))
* preserve cursor position when rendering completion menu ([fa7f0f3](https://github.com/tfcace/hash/commit/fa7f0f3199b86cc6539c67b999a603b1959b68e1))
* race condition in spinner ([ed5608d](https://github.com/tfcace/hash/commit/ed5608d4529cd73ee83a44035010cd4a2c42d1a6))
* resolve release CI failures ([#13](https://github.com/tfcace/hash/issues/13)) ([0c5f775](https://github.com/tfcace/hash/commit/0c5f7750f915e28e0cca48ae966de0c3243a1c5e))
* resource leaks and resilience improvements ([b32e0fe](https://github.com/tfcace/hash/commit/b32e0fed596486b511fdf85bd007d957257f2f96))
* **shell:** improve ghost text and confirmation UI error handling ([6cfb0c1](https://github.com/tfcace/hash/commit/6cfb0c1ab92a2dec165a2c64148d7eff4c159f6d))
* **shell:** prevent Ctrl+C at empty prompt from exiting shell ([3985942](https://github.com/tfcace/hash/commit/39859427719ebbbd3f21ba2ccfdac98299a28c76))
* use graceful degradation for source/eval compatibility ([7101c49](https://github.com/tfcace/hash/commit/7101c4989ccfa669689fd4b06f20783f00166c69))

## [0.4.3](https://github.com/tfcace/hash/compare/v0.4.2...v0.4.3) (2026-01-31)


### Bug Fixes

* **agent:** broken pipe on context cancelation ([0bcef00](https://github.com/tfcace/hash/commit/0bcef0025f028f085b29832393d8385a4abfe317))
* lint errors (gofmt, gosec, staticcheck) ([9b39a3d](https://github.com/tfcace/hash/commit/9b39a3d73ecfee27d52c1996689f7306eea4126e))
* resource leaks and resilience improvements ([b32e0fe](https://github.com/tfcace/hash/commit/b32e0fed596486b511fdf85bd007d957257f2f96))

## [0.4.2](https://github.com/tfcace/hash/compare/v0.4.1...v0.4.2) (2026-01-31)


### Bug Fixes

* race condition in spinner ([ed5608d](https://github.com/tfcace/hash/commit/ed5608d4529cd73ee83a44035010cd4a2c42d1a6))

## [0.4.1](https://github.com/tfcace/hash/compare/v0.4.0...v0.4.1) (2026-01-29)


### Bug Fixes

* resolve release CI failures ([#13](https://github.com/tfcace/hash/issues/13)) ([0c5f775](https://github.com/tfcace/hash/commit/0c5f7750f915e28e0cca48ae966de0c3243a1c5e))

## [0.4.0](https://github.com/tfcace/hash/compare/v0.3.0...v0.4.0) (2026-01-29)


### Features

* add bash/zsh migration system with compatibility layer ([8474089](https://github.com/tfcace/hash/commit/847408985fca0c84e307c501a6d9a99b55542d79))
* alias/function tab-completion ([ec68874](https://github.com/tfcace/hash/commit/ec68874b6df35df861a1db8cbf690d88b8e3f226))
* **completion:** mask sensitive env var values in completion preview ([09d2d88](https://github.com/tfcace/hash/commit/09d2d882798be34f7fe4d0c10650176a41199f09))


### Bug Fixes

* add environment variable tab-completion with value preview ([f07c236](https://github.com/tfcace/hash/commit/f07c23672333a42b664731362ba1f8e81b56cf36))
* copy agent response to system clipboard with feedback ([9110225](https://github.com/tfcace/hash/commit/91102252b9bbeff318649563fd5fa792328df3cd))
* disable ONLCR on PTY when stdout is a pipe ([8b7fc9c](https://github.com/tfcace/hash/commit/8b7fc9c69057a5aa7f5f2c09dd1aded6e9595d0f))
* handle agent startup failures gracefully ([67959bd](https://github.com/tfcace/hash/commit/67959bd113593dbb9c388140cffc1cc00b7592bf))
* improved handling of complex aliases when migrating from zsh ([#12](https://github.com/tfcace/hash/issues/12)) ([43ec193](https://github.com/tfcace/hash/commit/43ec1930122474292864c9e330e75a6f7469f29d))
* preserve cursor position when rendering completion menu ([fa7f0f3](https://github.com/tfcace/hash/commit/fa7f0f3199b86cc6539c67b999a603b1959b68e1))
* use graceful degradation for source/eval compatibility ([7101c49](https://github.com/tfcace/hash/commit/7101c4989ccfa669689fd4b06f20783f00166c69))

## [0.3.0](https://github.com/tfcace/hash/compare/v0.2.0...v0.3.0) (2026-01-26)


### Features

* delightful ux ([c36611e](https://github.com/tfcace/hash/commit/c36611ebd25c75dc0cb52dba221eb9d5a2c3723d))

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
