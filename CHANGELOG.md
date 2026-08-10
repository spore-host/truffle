# Changelog

All notable changes to **truffle** are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed
- **`quotas.CanLaunch`'s Spot path now tracks current Spot usage** instead of
  only confirming a request fits the *full* Spot quota (#132). `QuotaInfo`
  gains `SpotUsage`, populated by splitting `getCurrentUsage`'s
  `DescribeInstances` scan by `InstanceLifecycle` (spot vs on-demand) rather
  than summing both into one map. That split also fixes a second bug in the
  same code path: on-demand `Usage` previously included running Spot
  instances' vCPUs too, understating on-demand headroom. Real-world case: an
  account with a 64-vCPU Spot quota already fully saturated by 8 running
  instances got a false "fits" for 2 more, since 16 ≤ 64 (the full quota) in
  isolation — the actual launch then failed with
  `MaxSpotInstanceCountExceeded` with zero prior warning.

## [0.48.1] - 2026-08-07

### Security
- **A pin's version comment can no longer silently misstate what CI runs.**
  `actions/checkout@df4cb1c...` — used in `ci.yml`, `security.yml`, and
  `release.yaml` — is really `v6.0.3`, and had sat labelled `# v6` (indistinguishable
  from a routine same-line bump) with no test catching it; three more pins
  (`setup-go`, `goreleaser-action`, `attest-build-provenance`, `codecov-action`)
  carried the same bare-major shape. Two complementary halves now enforce exact
  labels: `internal/hygiene.TestActionsArePinnedToSHAs` requires an exact
  `vX.Y.Z` comment (offline, hermetic), and a new `scripts/verify-pins.sh`
  resolves each SHA against the tag its comment claims and fails if they
  disagree (needs the network, so it runs as its own CI step). Neither alone
  suffices — a bare label defeats the offline check, and an exact-but-false
  label defeats a check that never queries the tag. CI-only; no change to the tool.

- **Also moved `Test` and `E2E`-equivalent CI off the self-hosted orion fleet
  onto `ubuntu-latest`.** The fleet (colima/Docker on orion.local) is being
  decommissioned org-wide in favor of GitHub-hosted runners. No behavior change
  to the tool; `setup-go`'s `cache: false` workaround (needed only because the
  orion containers' filesystem persisted between jobs) is removed since
  GitHub-hosted runners start clean every time.

- **Added a Dependabot config, so the SHA-pinned actions and Go deps get bumped (#124).**
  Every action here is pinned to a commit SHA, which closes the mutable-tag hole
  but opens a staleness one: a SHA never moves — including past a security fix —
  and unlike `@v6` nothing updates it for you. Pinning is only safe if something
  bumps the pins, and nothing did. `actions/checkout@v6` had already moved
  upstream while this repo went on pinning the older commit, silently.

  This matters most for `release.yaml`, which pins the release-signing actions
  (`goreleaser-action`, `cosign-installer`, `attest-build-provenance`) — the
  supply-chain machinery. A frozen `cosign-installer` means releases keep getting
  signed by an old cosign, and cosign 3.x already changed its CLI surface.

  Weekly, grouped, with a 7-day cooldown so a freshly-published tag isn't proposed
  the day it ships. The actions group pattern is `*` rather than `actions/*`
  precisely because those signing actions aren't under `actions/`. Two tests
  enforce coverage — every action in every workflow must be matched by a group
  pattern, and every `go.mod` must be watched — so adding one without wiring it up
  fails CI instead of going unnoticed. CI-only; no change to the tool.

- Bumped `golang.org/x/text` to v0.39.0 to clear CVE-2026-56852 (a `norm.Iter`
  infinite loop on crafted input; HIGH). Indirect dependency; no API change.

### Changed
- **CI now fails on unformatted code (#122).** Nothing did before: CI had no
  formatting check, and `make fmt` rewrites files and always succeeds —
  convenient locally, but it cannot fail a build, so it never gated anything.
  Seven files sat unformatted on `main` and reappeared as unrelated diffs in
  whatever PR ran the formatter next.

  `make check-fmt` reports drift instead of fixing it — offenders listed with a
  diff — and now runs in CI. The seven drifted files are formatted (struct-field
  and comment alignment, trailing whitespace, one import re-sorted; no behavior
  change).
- Bumped the `substrate` test dependency v0.70.0 → v0.85.0. Test-only; no runtime
  or API change. Two of the fixes matter directly to truffle's own tests:
  `DescribeInstances`/`DescribeImages` and friends now raise `Invalid*ID.NotFound`
  for an explicitly-named ID that does not exist instead of returning an empty
  list (substrate#391), and each service serializes errors in its own wire
  protocol so `Error.Code` is the symbolic AWS code rather than an HTTP status
  (substrate#392) — so our not-found branches are reachable offline for the first
  time. Substrate also gained a `pricing` plugin emulating the Price List Query
  API (`GetProducts`, `DescribeServices`, `GetAttributeValues`) together with a
  parser fix for `api.<service>.<region>` hosts (substrate#401, substrate#403),
  which is what truffle's on-demand and SageMaker rate lookups talk to.

  The v0.82.0–v0.85.0 span additionally makes EC2's rejection of reserved
  `aws:`-prefixed tag keys reachable offline (substrate#452) — truffle reads such
  tags (a fleet's `aws:ec2:fleet-id`) but never writes them, so this is coverage
  we gain rather than behavior we had to change.

### Fixed
- Bumped `libs` to v0.43.3, which fixes `catalog.Validate()` incorrectly
  flagging a private-overlay image binding as if the shipped catalog itself
  were broken (libs #392). truffle doesn't call `catalog.Validate()` directly,
  but it does import `libs/catalog` (`catalog.List()`/`catalog.Lookup()` in
  `app`/`find`), so this raises the minimum `libs` version any consumer that
  depends on both truffle and `libs/catalog` (e.g. spawn) resolves to,
  avoiding the buggy floor via Go's minimum-version selection.

### Documentation
- Added the project hero image to the top of the README.

## [0.48.0] - 2026-07-28

### Added
- **`SageMakerPriceFor` — ask for the rate that matches your use case** (#107).
  SageMaker meters each usage as a separate Price List component, and their rates
  are not always equal: for `ml.p4d.24xlarge` the `Cluster` (HyperPod) rate is
  $25.910/hr while `Hosting`/`Training` are $25.251/hr. Pass a `SageMakerUsage`
  (`UsageInference`, `UsageTraining`, `UsageHyperPod`, …) to price what you're
  actually running. Embedders with a custom `SageMakerPricer` are unaffected — the
  usage-aware method is an optional extension interface
  (`SageMakerUsagePricer`), and a pricer that doesn't implement it falls back to
  the plain lookup.
- **`truffle available <instance-type>` — can I actually GET this?** (#108) A new
  command answering the question `find` doesn't: for scarce accelerator types the
  price is often not the deciding number, since a type can be listed, priced, and
  completely unobtainable. Reports four signals for one type in one region — spot
  placement score per AZ (from `GetSpotPlacementScores`), the offered-AZ footprint
  with the region's AZ count as a denominator, On-Demand vCPU quota headroom with
  the quota code to request an increase against, and purchasable Capacity Block
  offerings. Table, JSON and YAML output.

  Each signal is reported separately rather than collapsed into one score, because
  they fail independently and call for different remedies: a single-AZ footprint, a
  1/10 spot score and a zero quota are three different problems. Signals are
  best-effort — `GetSpotPlacementScores` is commonly denied by SCP, so an
  unavailable one shows as a visible warning rather than failing the command, and
  never as a silent blank that could read as a good result.
- **`aws.Client.Obtainability(ctx, instanceType, region)`** — the same signals as a
  library call for embedders (#108). Returns an `Obtainability` carrying per-AZ
  `SpotPlacement` scores (with both the AZ name and the account-specific AZ ID,
  since AWS returns the ID and it's the stable identifier across accounts), the
  offered AZs, quota headroom and code, and `InstanceHeadroom()` to convert vCPU
  headroom into a launchable instance count.
- **`Client.OnDemandPriceWithSource`** reports whether a rate came from the live
  AWS Price List (`PriceSourceLive`) or truffle's built-in fallback table
  (`PriceSourceStatic`), so an embedder that gates spending can refuse a
  possibly-stale rate while a savings estimate still uses it. `OnDemandPrice` is
  unchanged. A pricer injected via `SetOnDemandPricer` reports
  `PriceSourceUnknown` unless it implements the new optional
  `SourcedOnDemandPricer` interface.
- **`truffle find --show-price` now warns when a price came from the built-in
  table** rather than the live Price List, naming how many of the results are
  affected — so a reader comparing costs knows which figures are current. The
  flag's help text no longer describes pricing as static; it has been live since
  0.36.2.

### Changed
- **`capacity-blocks` now names the instance count when it finds nothing** (#109).
  An empty result used to print the generic "No capacity reservations found matching
  criteria", which reads as "this type has no capacity blocks" — but
  `DescribeCapacityBlockOfferings` silently gates on `--count`, so the truth is
  usually "not that many at once". Verified 2026-07-27: at `--count 8` every
  accelerator type returned zero offerings, while at `--count 1`, `p5.48xlarge`,
  `p5e.48xlarge`, `p5en.48xlarge` and `p6-b200.48xlarge` all had them. The message
  now states the count, type, region and duration actually queried, and suggests
  retrying with `--count 1`.

### Fixed
- **`SearchInstanceTypes`/`SearchSageMakerInstanceTypes` no longer crash the
  process on a nil pattern** (#106). A nil `matcher` now means "no instance-type
  constraint" (other filters still apply) instead of panicking. This was worse
  than a normal bad-argument bug: the search fans out into per-region goroutines,
  so the panic could not be recovered by the caller — `recover` only works in the
  panicking goroutine — and took down in-process embedders such as
  `spore-host-mcp` entirely.
- **SageMaker prices no longer depend on Price List response ordering** (#107).
  `pickSageMakerRate` returned whichever accepted component AWS happened to list
  first, so the same type could report an inference rate on one call and the
  higher HyperPod rate on the next. Selection now follows a fixed preference
  order (`Hosting` first, `Cluster`/HyperPod last) and is stable across orderings.
- **A reservation upfront fee can no longer be reported as an hourly rate**
  (#107). Rows with no `component` attribute — notably
  `USE1-TrainingPlanUpfrontFee` — are now skipped outright. Previously a type
  whose only priced row was the upfront fee would report $13.57/hr for
  `ml.p4d.24xlarge` against a real rate of $25.25/hr, making SageMaker look ~38%
  *cheaper* than the equivalent EC2 instance.
- **`GetCapacityBlockOfferings` no longer reports a failed query as "none
  available"** (#109). Per-region errors were discarded entirely (printed only
  under `--verbose`), so expired credentials or an SCP denying the API produced an
  empty list indistinguishable from genuinely sold-out inventory — the same class of
  bug as #63, and more misleading here because "no offerings" is a plausible answer
  to this question. An all-regions failure now returns an error and a partial
  failure warns on stderr, matching `SearchInstanceTypes`.
- **An unpriceable instance type now reports an error instead of a made-up price
  (#114).** When the AWS Price List had no rate — most often because the type
  isn't offered in that region — truffle fell back to its built-in table, which
  guessed from the instance family rather than admitting it didn't know. So
  `truffle` reported `hpc7a.96xlarge` at **$0.20/hr** against a real **$7.20**,
  and `p5.48xlarge` at **$9.60** against a real **$55.04**, both with no error.
  Two silent substitutions caused it: an unknown region quietly reused us-east-1
  prices, and an unknown type fell through to a per-family estimate. Both are
  gone. The built-in table is still used when the Price List API is unreachable
  (no credentials, no network), but only for a type and region it actually
  covers; anything else is an error that names what couldn't be priced.

  This mattered most to consumers spending money on the answer: spawn's
  `slurm estimate`/`submit` quoted these rates before launching billable
  instances (spore-host/spawn#447).

## [0.47.0] - 2026-07-22

### Security
- **Bump `google.golang.org/grpc` → 1.82.1** (indirect) — resolves
  GHSA-hrxh-6v49-42gf (gRPC-Go xDS RBAC / HTTP/2, HIGH).
- **Release artifacts are now signed** with keyless [cosign](https://docs.sigstore.dev/)
  (Sigstore) and carry SLSA build provenance (#104). The release signs
  `checksums.txt` — which lists every archive/package hash — with the release
  workflow's GitHub OIDC identity (no long-lived key), publishing
  `checksums.txt.bundle`, and attests build provenance. Verify a download with
  `cosign verify-blob --bundle checksums.txt.bundle --certificate-identity-regexp …
  --certificate-oidc-issuer https://token.actions.githubusercontent.com checksums.txt`,
  then check your file against `checksums.txt`. Takes effect from the next tagged
  release.

### Added
- **`truffle find --show-price`** (#50) — `find` now takes `--show-price` to
  populate the on-demand `$/hr` column, matching the old `search` command that
  `find` replaced (#44). Composes with `--regions` so price shows per region.
  Previously only the deprecated `search` and the spot-focused `spot` exposed
  pricing, so the canonical "what does this instance cost" question couldn't be
  answered with the canonical search command.
- **Zenodo DOI**: truffle is archived on Zenodo with a citable DOI (concept DOI
  [10.5281/zenodo.21439669](https://doi.org/10.5281/zenodo.21439669), always
  latest). Added to `CITATION.cff` and a README badge.

## [0.46.0] - 2026-07-19

### Added
- **G7 instance family (NVIDIA RTX PRO 4500)** added to the GPU database. `truffle
  find "rtx pro 4500"` (and the `nvidia`/`g7` family lookups) now resolve the six
  `g7.*` types. Complements the existing `g7e` (RTX PRO 6000). Surfaced while
  fixing spawn#384 (GPU AMI auto-detection).
- **`CITATION.cff`** — machine-readable citation metadata so the repo is citable
  (GitHub "Cite this repository"); base for Zenodo DOI minting.

## [0.45.0] - 2026-07-19

### Added
- **Command/flag reference is now generated from the CLI and drift-gated.** A
  hidden `truffle gen-docs` command (via `libs/docgen`) emits the exhaustive
  per-command reference to `docs-gen/`; `make gen-docs` regenerates it and a CI
  `check-docs` gate fails if the committed reference drifts from the code, so the
  docs site's reference can no longer go stale (2026-07 docs audit). Because it's
  generated from the binary, the `search` deprecation and the `capacity-blocks
  --end-by` rename now show correctly. Run `make gen-docs` after changing a
  command or flag. Uses `libs/docgen` v0.43.2, which HTML-escapes bare `<…>` and Vue
  `{{ … }}` tokens so the reference renders on the VitePress docs site.

## [0.44.0] - 2026-07-17

### Added
- **Shared spore.host config base.** truffle now honors the suite-wide
  `libs/sporeconfig` settings: a new persistent `--profile` flag (and `--account`),
  the `SPORE_PROFILE`/`AWS_PROFILE` env vars, and the `[spore]` table of
  `~/.config/spore/config.toml`, resolved flag > env > file > default. Both AWS
  client constructors (`pkg/aws.NewClient`, `pkg/quotas.NewClient`) load through a
  shared `pkg/awscfg` helper, so a suite-wide AWS profile applies consistently
  (truffle previously used the bare ambient chain with no profile concept).
  Region stays truffle's per-request `--regions`; unset profile = unchanged
  (ambient AWS chain).

### Fixed
- **Quota family classification for DL and VT instances** (#64). `dl1`/`dl2q`
  (Habana Gaudi / Qualcomm) now map to a dedicated DL quota family instead of
  falling through to Standard, and `vt1` maps to the G family (AWS groups it under
  the "G and VT" quota). Classification now matches the leading letter-run of the
  instance type rather than a loose prefix check, so multi-letter families aren't
  misfiled. `quotas` displays the DL family too.
- **`quotas` uses the shared `--regions` flag.** It previously defined its own
  local `--regions` that shadowed the root persistent flag and behaved
  differently from every other command; it now uses the shared flag (defaulting
  to us-east-1 when none is given, since it fans out an API call per region) (#64).

### Changed
- **`quotas` vCPU estimation logs unmappable instance sizes** instead of silently
  assuming 2 vCPU, so an unknown size can't quietly understate usage/headroom (#64).

## [0.43.0] - 2026-07-15

### Added
- **`find.ResolveCard(card) ([]string, error)`** and **`find.CardInstanceTypes`** —
  a strict GPU-card → EC2-instance-type resolver for library consumers. Unlike the
  free-text search pipeline, an unresolved card returns the exported
  **`find.ErrNoMatch`** sentinel instead of silently falling back to a match-all
  (`.*`) pattern that a caller could mistake for a real result (#90).

### Fixed
- Multi-word GPU card names now resolve **longest-match-first, up to the longest
  catalog key** (previously the token classifier only tried 2- and 3-word phrases).
  The 4-word canonical key `"rtx pro server 6000"` was unreachable by full-string
  match and resolved only via the single-token `rtx` alias; it — and the common
  marketing spelling `"rtx pro 6000"` (now an alias) — now resolve to the g7e
  family exactly (#90).

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

[Unreleased]: https://github.com/spore-host/truffle/compare/v0.48.1...HEAD
[0.48.1]: https://github.com/spore-host/truffle/compare/v0.48.0...v0.48.1
[0.48.0]: https://github.com/spore-host/truffle/compare/v0.47.0...v0.48.0
[0.47.0]: https://github.com/spore-host/truffle/compare/v0.46.0...v0.47.0
[0.46.0]: https://github.com/spore-host/truffle/compare/v0.45.0...v0.46.0
[0.45.0]: https://github.com/spore-host/truffle/compare/v0.44.0...v0.45.0
[0.44.0]: https://github.com/spore-host/truffle/compare/v0.43.0...v0.44.0
[0.43.0]: https://github.com/spore-host/truffle/compare/v0.42.0...v0.43.0
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
