# Changelog

All notable changes to **truffle** are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Security
- Semgrep SAST is now **enforcing** in CI (`--config=auto --error`) rather than
  report-only (#368). The scan was already clean — no findings to triage.

## [0.39.1] - 2026-06-12

### Fixed
- Bump libs to v0.37.1, which fixes stray template variables in the
  `truffle.capacity.summary.*` labels — non-English locales (es/fr/de/ja/pt)
  rendered `[truffle.capacity.summary.<key>]` instead of the translated label
  in `truffle capacity` output.

## [0.39.0] - 2026-06-12

### Added
- `truffle version` now reports whether a newer release is available (an
  explicit, on-demand check) (#53).

## [0.38.1] - 2026-06-11

### Changed
- Bumped substrate to v0.70.0 (the `/emulator` import path) so a downstream
  spawn → truffle dependency resolves cleanly under `go mod tidy` (#49).

## [0.38.0] - 2026-06-10

### Added
- `GetCapabilities` — single instance-type capability lookup, making truffle the
  capability authority other tools (spawn) consume (#48).
- Filter instance types by nested-virtualization support, with a
  `--nested-virtualization` flag and a Nested-Virt output column (#46).

## [0.37.2] - 2026-06

### Added
- Periodic version-check notification.

## [0.37.1] - 2026-06

### Fixed
- Follow-ups on earlier region/pricing fixes (#37, #39).

## [0.37.0] - 2026-06

### Fixed
- Resolved several search/region/pricing issues (#37, #42, #43, #44).

## [0.36.0 – 0.36.10] - 2026-06

The 0.36.x series — search, pricing, and metadata maturing after the move to the
standalone repo. Highlights:

### Added
- Live on-demand pricing via the AWS Price List API; `--show-price` /
  `ShowSavings` / `HourlyRate` (#2).
- `--region` as an alias for `--regions`; `--exact`; Turin/Zen 5 processor
  support; expanded processor + GPU metadata for broad AWS coverage; a warning
  when searching all regions without `--regions`.
- `aws.Finder` interface + `awsmock` package; test coverage raised past 60%.

### Fixed
- Pattern-matching and find-intersection regressions (#20, #29); region,
  pricing, and pattern fixes across #19–#41; human summary printed only for
  table output (#3).

## [0.35.0] - 2026-06

Initial tagged release from the standalone `spore-host/truffle` repository.

---

Older releases are summarized in the
[GitHub Releases](https://github.com/spore-host/truffle/releases) for this repo.

[Unreleased]: https://github.com/spore-host/truffle/compare/v0.39.1...HEAD
[0.39.1]: https://github.com/spore-host/truffle/compare/v0.39.0...v0.39.1
[0.39.0]: https://github.com/spore-host/truffle/compare/v0.38.1...v0.39.0
[0.38.1]: https://github.com/spore-host/truffle/compare/v0.38.0...v0.38.1
[0.38.0]: https://github.com/spore-host/truffle/compare/v0.37.2...v0.38.0
[0.37.2]: https://github.com/spore-host/truffle/compare/v0.37.1...v0.37.2
[0.37.1]: https://github.com/spore-host/truffle/compare/v0.37.0...v0.37.1
[0.37.0]: https://github.com/spore-host/truffle/compare/v0.36.10...v0.37.0
[0.35.0]: https://github.com/spore-host/truffle/releases/tag/v0.35.0
