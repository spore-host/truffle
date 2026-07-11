# Changelog

All notable changes to **truffle** are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.42.0] - 2026-07-11

### Added
- **SageMaker `ml.*` instance discovery in `search` and `find`** (#79). Pass
  `--service sagemaker` to search the SageMaker namespace instead of EC2:
  `truffle search --service sagemaker "ml.g5.*"` lists the `ml.*` instance types
  offered in each region (from Service Quotas, the authoritative source — there
  is no SageMaker `DescribeInstanceTypes`), with vCPU/memory/GPU/architecture
  specs derived from the underlying EC2 type. Results are tagged
  `service: "sagemaker"` in JSON/YAML and flagged in the table footer. Default
  behavior (`--service ec2`) is unchanged.
- **SageMaker `ml.*` on-demand pricing** (#80). `--service sagemaker` results now
  carry a `$/hr` rate from the SageMaker Price List offer (`AmazonSageMaker`),
  which includes the management premium over the equivalent EC2 rate (e.g.
  `ml.g5.2xlarge` ≈ $1.515/hr vs `g5.2xlarge` ≈ $1.212/hr). `find` shows and
  sorts by it automatically; `search` shows it with `--show-price`. Prices are
  cached per type/region like EC2 pricing.
- **SageMaker managed-spot eligibility + per-type quota** (#81). `--service
  sagemaker` results now mark which `ml.*` types are usable with **managed spot
  training** (a "Spot-Eligible" column + footer): managed spot is a billed-time
  discount of up to 90%, not a spot market, so there is no separate spot price —
  the marker reflects the presence of a "spot training job usage" service quota.
  A new `--show-quota` flag adds a per-type training-job quota column (a `0`
  means an increase must be requested before launching). Both fields
  (`managed_spot_eligible`, `training_job_quota`) also appear in JSON/YAML. This
  reuses the quota data already fetched for discovery — no extra API calls.

### Documentation
- **SageMaker discovery guide** (#82). New [`docs/sagemaker.md`](docs/sagemaker.md)
  covering `--service sagemaker`, how discovery works (Service Quotas as the
  offered-set source, specs from the underlying EC2 type), the pricing
  management premium, managed-spot eligibility, `--show-quota`, and the
  JSON/YAML fields. README gains SageMaker discovery examples and links the guide.

### Security
- **Pinned the CI/release Go toolchain to 1.26.5** to clear GO-2026-5856, a
  `crypto/tls` standard-library advisory present in go1.26.4. Builds now link the
  patched stdlib and govulncheck is green.
- **Bumped `golang.org/x/net` to v0.55.0** to clear five HIGH advisories in the
  v0.52.0 transitive dependency (CVE-2026-25681/27136/33814/39821/42502 —
  `x/net/html` arbitrary-code and related). Also pulls `x/sys` v0.45.0 and
  `x/text` v0.37.0. No code change; restores the Trivy scan to green.
- **Pinned all GitHub Actions to commit SHAs** (with version comments) in the
  CI/security/release workflows, and pinned `trivy-action` from the mutable
  `@master` to a release. Clears the Semgrep `github-actions-mutable-action-tag`
  finding and hardens the CI supply chain against tag hijacking.

### Fixed
- **`truffle find trn1.32xlarge` (and other accelerator types) now works.** The
  single-word instance-type detector only recognized single-letter family
  prefixes (`m7i`, `p5`), so multi-letter accelerator families — Trainium
  (`trn1`/`trn2`), Inferentia (`inf1`/`inf2`), Habana (`dl1`), video (`vt1`) —
  fell through to the natural-language parser, which matched *every* instance
  type and made `find` hang or return the whole catalog instead of the one type.
  `find` now routes these to the same fast exact-lookup path `search` already
  used. (`truffle search` was unaffected.)
- An exact-type search for an instance type that isn't offered in a region is
  treated as a clean no-match, not a region failure (#64). `DescribeInstanceTypes`
  with an explicit type filter returns `InvalidInstanceType`/
  `InvalidParameterValue` for an unavailable type; that's now classified as
  no-match. (Matters more now that an all-regions failure returns an error —
  otherwise searching for an unavailable type would hard-fail.)
- `CanLaunch` no longer overstates Spot headroom (#64). Current Spot usage isn't
  tracked (usage is only subtracted from the on-demand quota), so the Spot path
  previously treated usage as zero and returned a confident "ok". It now confirms
  the request fits the full Spot quota but states that remaining headroom is
  unverified rather than implying it checked usage.
- `SearchInstanceTypes` and `GetSpotPricing` no longer report success when
  *every* region query fails (#63). A total failure (expired credentials,
  throttling, an SCP denying the API) previously returned an empty result that
  callers could not distinguish from a legitimate "no matches", so truffle —
  the discovery authority spawn/lagotto consume — could silently conclude a
  type/region was unavailable when the query never ran. Now an all-regions
  failure returns an error, and a partial failure prints a warning to stderr
  (not only under `--verbose`).

### Documentation
- **Demoted the never-shipped native-CGO Python binding to design notes** (#76).
  `bindings/python/` presented an installable, "10-50× faster" binding, but the
  Go library (`native.go`) and Python wrapper were never committed — `pip install`
  and `from truffle import Truffle` both failed. Moved to
  `docs/design/native-cgo-binding/` with a clear "not shipped, use the spore-host
  SDK" banner, and the README now points Python users at
  [`pip install spore-host`](https://github.com/spore-host/python-sdk).
- README: add the `capacity-blocks` command to the command table and list
  French in the `--lang` options (both were already supported in the CLI).

## [0.41.0] - 2026-06-18

### Added
- `capacity-blocks` gains **`--days`** (the natural unit for Capacity Blocks for ML —
  `--days 1` instead of `--duration-hours 24`) and **`--start-date YYYY-MM-DD`** to
  search for blocks starting on a given calendar day without hand-building RFC3339
  timestamps. `--days` overrides `--duration-hours`.
- `capacity-blocks --sort price|start` orders offerings cheapest-first (default) or
  soonest-first. (The previous output claimed cheapest-first but actually sorted by
  start date.)

### Changed
- `capacity-blocks` now **searches a date window by default** (now → the soonest a
  block of the requested duration could end) instead of only the immediate instant,
  so a bare query finds near-future inventory it previously missed (#69).
- `capacity-blocks` now shows a single **WINDOW (LOCAL)** column in your local
  timezone (e.g. `Jun 18 04:30 → Jun 19 04:30 PDT`) instead of two raw UTC ISO-8601
  `START`/`END` columns — far easier to read, and the redundant end-date is dropped
  when the window stays within one local day. Same for the owned-blocks table
  (`capacity --blocks`).
- `capacity-blocks --duration-hours` is **rounded up to a valid Capacity Block
  duration** (1-day steps to 14 days, then 7-day steps to 182), with a notice,
  instead of forwarding an invalid value and surfacing AWS's opaque "duration is not
  valid" error. Durations over 182 days are rejected with a clear message (#69).
- **Renamed `--start-before` → `--end-by`** to match the API's real semantics: the
  underlying `EndDateRange` is the *latest block end*, not "starts before". The old
  name silently constrained the end date and could exclude the very block requested.

### Fixed
- `--start-date` (and the default window) derive their end bound accounting for the
  API's `EndDateRange` being the *latest end*: a block that starts on the chosen day
  runs its full duration and ends up to ~12h into a later day (all blocks end at
  11:30 UTC), so the window covers start-of-day + duration + a cushion. Without this,
  the exact block you asked for was filtered out. Closes #69.

## [0.40.0] - 2026-06-17

### Added
- **`truffle capacity-blocks`** — discover **purchasable** EC2 Capacity Block for ML
  offerings (read-only), via `DescribeCapacityBlockOfferings` (#67). Filter by
  `--instance-type` (required), `--count`, `--duration-hours` (required), optional
  `--start-after`/`--start-before` and `--region`. Surfaces each offering's id,
  instance type/count, AZ, start/end, duration, and **up-front price** — the offering
  id is what `spawn capacity-block purchase` reserves. Table/JSON/YAML/CSV output.

### Fixed
- `truffle capacity --blocks` now actually shows your existing/scheduled Capacity
  Blocks for ML (#67). The flag was previously a no-op — it never reached
  `GetCapacityBlocks`, so the command always listed On-Demand Capacity Reservations
  regardless. (For *purchasable* offerings, use the new `truffle capacity-blocks`.)

### Security
- Semgrep SAST is now **enforcing** in CI (`--config=auto --error`) rather than
  report-only (#368). The scan was already clean — no findings to triage.

### CI
- Pin govulncheck to v1.3.0; v1.4.0 panics analyzing generics
  (`ForEachElement called on type containing *types.TypeParam`), crashing the
  scan rather than reporting a real vulnerability.

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

[Unreleased]: https://github.com/spore-host/truffle/compare/v0.42.0...HEAD
[0.42.0]: https://github.com/spore-host/truffle/compare/v0.41.0...v0.42.0
[0.41.0]: https://github.com/spore-host/truffle/compare/v0.40.0...v0.41.0
[0.40.0]: https://github.com/spore-host/truffle/compare/v0.39.1...v0.40.0
[0.39.1]: https://github.com/spore-host/truffle/compare/v0.39.0...v0.39.1
[0.39.0]: https://github.com/spore-host/truffle/compare/v0.38.1...v0.39.0
[0.38.1]: https://github.com/spore-host/truffle/compare/v0.38.0...v0.38.1
[0.38.0]: https://github.com/spore-host/truffle/compare/v0.37.2...v0.38.0
[0.37.2]: https://github.com/spore-host/truffle/compare/v0.37.1...v0.37.2
[0.37.1]: https://github.com/spore-host/truffle/compare/v0.37.0...v0.37.1
[0.37.0]: https://github.com/spore-host/truffle/compare/v0.36.10...v0.37.0
[0.35.0]: https://github.com/spore-host/truffle/releases/tag/v0.35.0
