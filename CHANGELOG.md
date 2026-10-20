# Changelog

All notable changes to **truffle** are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Library: the pattern dispatch `truffle find` uses is exported** (#263).
  `find.LooksLikePattern` reports whether a query is an instance-type
  pattern (an exact type, a bare family, a glob or a regex),
  `find.PatternToRegex` and `find.WildcardToRegex` build the anchored regexp
  for one, `ParsedQuery.Pattern` / `ParsedQuery.IsPattern` say that
  `ParseQuery` read the query as a pattern, and `metadata.InstanceFamilies`
  lists the known family names.

### Deprecated

- **`metadata.EFACapableFamilies`, `metadata.GetFamiliesByEFA` and
  `metadata.IsEFASupported`** (#253). The hand-kept family list stops at p5
  and treats EFA as a family property. truffle no longer uses it; read
  `NetworkInfo.EfaSupported` (or `InstanceTypeResult.EFASupported`) instead.

### Fixed

- **`find.ParseQuery` accepts an exact instance type** (#263). A library
  caller such as spore-host-mcp's `truffle_find` got
  `unknown term: "r8g.2xlarge"` for the most specific query there is, while
  `truffle find r8g.2xlarge` worked, because only the CLI checked for a
  pattern before parsing. `ParseQuery` now does that check itself, so every
  caller matches exactly the named type (or the family, glob or regex), and
  the CLI goes through the same function. A pattern query sets
  `ParsedQuery.Pattern`, and `BuildCriteria` matches what it names with no
  other filter. As in the CLI, a single family-shaped word truffle does not
  know ("zz9q") is now searched as a pattern rather than refused with a
  suggestion, and an invalid regex is an error from `ParseQuery`.
- **Exact types of the `u-*tb1` and `mac-m*` families are found** (#263).
  `truffle find u-6tb1.112xlarge` and `truffle find mac-m4.metal` read the
  type as natural language and failed with "unknown terms", because the
  pattern check expected a generation digit straight after the first letters
  of the name. The family is now recognised whole.
- **`efa_supported` in JSON/YAML results** (#253): `InstanceTypeResult.EFASupported`
  carries EC2's own per-type EFA flag (`NetworkInfo.EfaSupported`). For
  library callers, `aws.FilterOptions.RequireEFA` keeps only EFA-capable
  types, and `aws.FilterOptions.EFAAlternative` admits them beside a pattern
  (the "or efa" half of a query).

- **`efa` queries find every EFA-capable type** (#253). `truffle find "h200
  efa"` matched nothing, and so did `"b200 efa"`, because EFA support came
  from a hand-kept family list that stopped at p5. It missed p5e, p5en,
  p6-b200/b300, trn1, g5/g6/g6e/g7, the 8th-generation CPU families and
  others. EFA is now EC2's per-type flag, checked during the search: "h200
  efa" finds p5e.48xlarge and p5en.48xlarge. A family no longer counts as EFA
  because some of its sizes are: `"c6a efa"` lists c6a.48xlarge and
  c6a.metal, not c6a.large. "h100 or efa" still means H100 instances or any
  EFA-capable type (#144). The "Network: EFA supported" match reason also
  reads the type's own flag.

## [0.61.0] - 2026-10-18

### Added

- **`--limit N` on `truffle find` and `truffle spot`** (#250). It lists only the
  first N results in their ranked order: for `find`, the first N instance types
  with every region row of each, and for `spot`, the first N rows. The table
  keeps its border, legend and footer, which `| head -N` cut off, and a line
  says what was left out ("Listed the first 3 of 28 types; --limit 0 lists
  all"). The "Found N" header and the minimum-spec hint still count every
  match. `0`, the default, lists everything.

- **`accelerator_memory_gib`** (#244): JSON/YAML (`InstanceTypeResult.AccelMemoryGiB`)
  and CSV now carry the per-device GPU or accelerator memory as MiB/1024, to
  0.1 GiB (179.1 for a B200, 22.4 for an A10G), beside the nominal
  `accelerator_memory_gb`. In CSV it is appended as the last column, so every
  existing column keeps its position.
  For library callers, `aws.NominalGPUMemoryUnit` says which unit
  `aws.NominalGPUMemoryGB` is in (`aws.UnitGiB` or `aws.UnitGB`), and
  `aws.FormatGPUMemory` renders it with that unit ("179 GiB", "24 GB").

### Fixed

- **GPU memory is labelled GiB when it is GiB** (#244). The VRAM column was
  headed `VRAM (GB)`, so a B200 (EC2: 183359 MiB, 179.06 GiB) read
  `1432 (179/gpu)` and looked like 179 GB. Most GPUs' sizes are binary
  (H100 80 GiB, B200 179 GiB, Trainium2 96 GiB), a few are decimal (A10G and
  L4 24 GB, L40S 48 GB, dl2q's AI100 16 GB), so each figure now carries its
  own unit and the header carries none: `VRAM` / `Accel mem` read
  `1432 GiB (179/gpu)`, `640 GiB (80/gpu)`, `96 GB (24/gpu)`. The "why it
  matched" lines (`GPU: H100 (80 GiB, training)`, `GPU memory: 24 GB >= 24`)
  do the same. The numbers are unchanged, and so is the per-GPU memory
  filter: `"gpu 24gb"` is still compared with the size the GPU is sold as, in
  its own unit, so it still matches the A10G and L4; the filter's notes and
  hints now say `GB/GiB per GPU, as sold`. Host memory, always MiB/1024, is
  GiB throughout (it already was in the table, CSV and filters); shell
  completion descriptions said `GB RAM` and now say `GiB RAM`, and `find
  --help` says that `"64gb"` host memory means 64 GiB. The JSON/YAML field
  `accelerator_memory_gb` keeps its name and value for compatibility; despite
  the name it is GiB for a binary-sized device, as documented on the field;
  use the new `accelerator_memory_gib` for a binary figure, or
  `accelerator_memory_mib`. `metadata.GPUInfo.MemoryGB` is likewise unchanged
  and documented as in the GPU's own unit.

## [0.60.0] - 2026-10-18

### Added

- **`metadata.IsFractionalGPUFamily`** (#245): whether a family's instances get
  a partition of a GPU (g6f, gr6f) rather than the whole device, so the GPU
  model's memory overstates what they can use.

### Fixed

- **A GPU memory size in a card name is applied** (#245). `find.ResolveCard`
  dropped it, so `"A100-80GB"` resolved to p4d.24xlarge (40 GB A100s) as well as
  p4de.24xlarge (80 GB), and a job sized for 80 GB per GPU could be placed on
  GPUs too small for it. The size is now per-GPU memory from the family's
  recorded figure, applied as a minimum as elsewhere in truffle. So
  `"A100-80GB"` resolves to p4de only, `"V100-32GB"` to p3dn only, and
  `"A100-40GB"` keeps both. A size no variant has is `ErrNoMatch`, and
  fractional GPUs (g6f, gr6f) are left out of a memory-suffixed card. The doc
  comment no longer claims other constraints are rejected; they never were
  (#246).

## [0.59.0] - 2026-10-15

### Added

- **`truffle --version`** (#189). The conventional flag (and what package
  managers' test blocks try) used to fail with `unknown flag: --version`; only
  the `truffle version` subcommand worked. It now prints the same version, git
  commit and build date, without the network update check, so it is safe in
  scripts.

- **`Client.OnDemandPrices` and `Client.SageMakerPrices`** (#210), for library
  callers that price a whole result set: they take a list of
  `PriceKey{InstanceType, Region}`, look each distinct key up once, run at most
  `PriceLookupConcurrency` (4) Price List requests at a time, and return a
  `PriceQuote` (price, source, error) per key. With the default pricer a type
  wanted in several regions costs one Price List request for all of them. A key
  left unpriced because the context ended carries the context error and is not
  filled from the static table.

- **`aws.SortZoneNames` and `aws.ZoneNamesSorted`** (#217), the zone-name
  order truffle uses for every AZ list (standard AZs, then Local and Wavelength
  Zones, each alphabetically), for library callers that build or merge AZ lists.

- **`metadata.AcceleratorFamilies` and `metadata.GetFamiliesByUseCase`**
  (#233): the sorted list of GPU and accelerator families, and the families of
  the GPUs and accelerators filed under a use case ("training", "inference").

- **A `trainium2` entry in `metadata.GPUDatabase`** (#233), for trn2, so
  `find trainium2` and `find training` reach it.

- **Per-GPU memory in the library** (#236): `FilterOptions.MinGPUMemory` and
  `ExactGPUMemory` filter on the memory of one GPU or accelerator device;
  `InstanceTypeResult.AccelMemoryMiB` (JSON/YAML `accelerator_memory_mib`)
  records it (the GPU's, else the Neuron device's for Inferentia and
  Trainium, else the inference accelerator's for dl2q); and
  `aws.NominalGPUMemoryGB` / `aws.MatchesGPUMemory` read a device's MiB as the
  size it is sold as. In `find`, `ParsedQuery.MinGPUMemory`, `GPUMemoryRead`
  and `MemoryReadingNote`, and the `TokenHostMemory` / `TokenGPUMemory`
  token types.

- **Accelerator identity and per-family memory in the library** (#240):
  `InstanceTypeResult.AcceleratorCount`, `AcceleratorModel` and
  `AcceleratorManufacturer` (JSON/YAML `accelerator_count`,
  `accelerator_model`, `accelerator_manufacturer`) name the devices whose
  memory `AccelMemoryMiB` records: the GPUs, else the Neuron devices
  (Inferentia, Trainium; no manufacturer in EC2's record), else the inference
  accelerators (dl2q). `GPUs` and `GPUModel` stay GPU-only.
  `metadata.AcceleratorMemoryMiB(family)` returns the per-device memory EC2
  reported for a GPU or accelerator family on 2026-10-15, where
  `metadata.GPUDatabase`'s `MemoryGB` is one figure per model (unchanged, and
  the database's shape is unchanged).

- **`truffle find --all-regions`** searches every enabled region (the old
  default). Passing it together with `--regions` is an error.

- **`(*aws.Client).ConfiguredRegion()`** (library) returns the default region the
  client's config resolved to, or `""` if none is set.

### Changed

- **GPU memory is shown in GB as sold, matching the GPU-memory filter** (#238).
  The `find`/`search` table's VRAM column is now `VRAM (GB)` and shows the
  same nominal per-GPU figure that `truffle find "gpu 24gb"` compares, so an
  A10G or L4 reads `24 (24/gpu)` instead of `22 (22/gpu)` GiB, and an L40S 48
  instead of 44; binary-sized GPUs are unchanged (H100 `640 (80/gpu)`). CSV
  output gains `accelerator_count`, `accelerator_model` and
  `accelerator_memory_gb` columns, named for the JSON fields, when any result
  has a GPU or other accelerator (CPU-only CSV is unchanged; #240). JSON/YAML keep
  `accelerator_memory_mib` and add `accelerator_memory_gb`, the nominal figure
  (`InstanceTypeResult.AccelMemoryGB` for library callers). The "why it
  matched" GPU line and `metadata.GPUDatabase` use the same figure (A10G and
  L4 24 GB, L40S 48 GB), and the per-GPU memory note no longer says the VRAM
  column is in GiB.

- **`metadata.SizeCategory` is a size range, not a list** (#220). It now has
  `Min` and `Max` multipliers (xlarge = 1) instead of `Sizes`;
  `metadata.SizeMultiplier`, `SizeCategoryOf`, `InSizeCategory` and
  `SizePattern` replace `GetSizesForCategory`, which is removed, since a range
  such as "8xlarge and up" can't be listed.

- **`truffle quotas` and `truffle available` now check your configured region by
  default, not us-east-1** (#200). Both answers are per-region, so a user working
  in us-west-2 was shown quota headroom and obtainability for a region they don't
  launch in. They now use the same lookup as `find`: the spore.host config
  region, then `AWS_REGION` / `AWS_DEFAULT_REGION`, then your AWS profile's
  region. `--region` / `--regions` still override it. us-east-1 is used only when
  no region is configured anywhere, and a note on stderr says so. (The on-demand
  price in `available` still comes from the Price List API's us-east-1 endpoint,
  which it always did; it prices the region being checked.)
- **`truffle find` now searches your configured region by default, not every
  enabled region** (#188). Previously a bare `truffle find "amd turin 16 cores
  64gb"` fanned out to all 18 enabled regions and took ~42 s; it now searches
  the region you already work in — the spore.host config region, then
  `AWS_REGION` / `AWS_DEFAULT_REGION`, then your AWS profile's region — and
  finishes in ~4–5 s. A one-line note names the region and points at the new
  flag. `--regions` still overrides it. If no region is configured anywhere,
  `find` keeps the old behaviour and searches every enabled region, saying why.
  Scripts that relied on the global default should add `--all-regions`.
- **`truffle search` now also searches your configured region by default** (#194),
  matching `find` (it is `find`'s deprecated pattern-only predecessor). A note
  names the region; `--all-regions` restores the old every-region search, and
  with no region configured it still searches every enabled region. **This is a
  behaviour change — release it in a minor version.**
- **Every multi-region command now takes `--all-regions`, and combining it with
  `--regions` is an error** (#194). `spot`, `az`, `capacity` and
  `capacity-blocks` keep every enabled region as their default, because their
  answer is a cross-region one (the cheapest region, AZ coverage per region,
  reservations wherever you own them, which region has a block); for them
  `--all-regions` just says so explicitly. `az --az … --all-regions` is also an
  error, since `--az` already picks the regions. README has a table of each
  command's default.
- **Region searches that report availability zones are faster** (#188). AZ
  offerings are now fetched in one batched call per region instead of one call
  per matching instance type. This affects `find`, `search`, `spot`, and library
  callers of `SearchInstanceTypes` that set `IncludeAZs`. A broad single-region
  `find` drops from ~10 s to ~5 s; the results are identical.
- **Instance-type searches fetch less of the EC2 catalog** (#195). When a
  query names families or exact types (for example `find "amd turin …"` or
  `find "m7i*"`), an architecture, or an exact vCPU count (`--exact`), truffle
  now asks EC2 for just those types instead of paging through the whole catalog
  in every region. Minimum vCPU, memory, and core counts are still checked
  locally, and results are unchanged. The search step of a cross-region
  `find "amd turin 16 cores 64gb" --all-regions` drops from ~6 s to ~4.5 s.
  Most of that command's remaining time is on-demand price lookups (#210).
  This affects `find`, `search`, `az`, `spot`, and library callers of
  `SearchInstanceTypes` / `SearchInstanceTypesByRegion`; no API changes.

- **In a GPU query, `find` reads a bare memory size as memory per GPU**
  (#236). `truffle find "gpu 24gb"` used to mean "any accelerator host with
  at least 24 GiB of RAM" (71 types in us-east-1, including the 16 GB T4 and
  quarter-L4 slices); it now means GPUs with at least 24 GB each: the A10G,
  L4 and larger (53 types), closest fit first. `h100 80gb` and `a100 80gb` (or
  `A100-80GB`) select the 80 GB parts, so `a100 80gb` is now p4de only. The
  size is compared as the GPU is sold, so the A10G and L4, which EC2 reports
  as 22888 MiB (22 GiB in the VRAM column), count as 24 GB; Inferentia and
  Trainium use their Neuron device memory. A one-line note on stderr says
  when a size was read this way. To mean host memory, write `64gb ram` or
  `64gb memory` (`a10g 64gb ram` is g5 hosts with at least 64 GiB); to ask
  for per-GPU memory anywhere, write `24gb vram` or `24gb gpu memory`. Both
  can appear together (`a10g 24gb 64gb ram`). `--exact` matches the GPU size
  exactly. Queries with no GPU or accelerator word are unchanged, except that
  `ram`, `memory` and `mem` after a size are no longer reported as unknown
  terms.

### Deprecated

- **`truffle spot --show-savings`** (#204). It has done nothing since spot began
  always showing savings vs On-Demand (#21). It still parses, prints a
  deprecation notice, and will be removed.
- **`truffle capacity --odcr`** (#205). It never did anything: On-Demand Capacity
  Reservations are shown unless `--blocks` is given, and `--odcr=false` did not
  hide them. It still parses, prints a deprecation notice, and will be removed.

### Fixed

- **Trainium2 memory is 96 GB per chip, and B200/B300 read as AWS documents
  them** (#242). EC2's DescribeInstanceTypes reports 512 GiB per Trainium2
  device (8 TiB for trn2.48xlarge), but a NeuronInfo device is one chip (its
  8 NeuronCores are one Trainium2's) and AWS documents 96 GiB of HBM per chip,
  1.5 TB for trn2.48xlarge's 16. truffle now uses the documented figure in the
  table, the per-accelerator memory filter, the closest-fit sort and JSON
  (`accelerator_memory_mib` 98304, `accelerator_memory_gb` 96), so
  trn2.48xlarge shows `1536 (96/chip)` and `trainium2 256gb` no longer matches
  it. Inferentia and Trainium rows now label their memory `/chip`; inf1 (8),
  inf2 and trn1 (32) were already right. The B200 and B300 read as 179 and 268
  GB, AWS's per-instance totals (1,432 and 2,144 GB) over 8 GPUs, rather than
  179.1 and 268.6, so `b300 268gb --exact` finds p6-b300.48xlarge.

- **Inferentia, Trainium and dl2q rows show their accelerators** (#240). In
  the `find`/`search` table they showed `-` in the GPU columns, since only
  GPUs were read; they now show the device count, model and memory from the
  same record the GPU-memory filter uses, e.g. inf2.xlarge `1 │ Inferentia2 │
  32 (32/dev)`, and the columns are headed `Accel` / `Accel Model` /
  `Accel mem (GB)` whenever such a row is listed (a GPU-only table keeps
  `GPUs` / `GPU Model` / `VRAM (GB)`). CSV fills the same columns.

- **The "why it matched" line gives the GPU memory of the instance's family**
  (#240). It took one figure per model, so p3dn's 32 GB V100s read as 16 GB
  and p4de's 80 GB A100s as 40 GB; it now uses the recorded per-family
  figure (p3 16, p3dn 32, p4d 40, p4de 80), falling back to the model's.

- **dl2q's Qualcomm AI100 is 16 GB, not 16.8** (#240). EC2 reports it as
  16000 MiB, a GB size written as MiB; `aws.NominalGPUMemoryGB` now reads a
  whole number of thousands of MiB (that is not a whole number of GiB) as
  that many GB, in the table and the GPU-memory filter.

- **m5a, m5ad, r5a and r5ad are Naples, not Rome; `naples` is a processor
  word** (#232). AWS's instance-type docs list the AMD EPYC 7571, a
  1st-generation (Naples, Zen) part, for m5a, m5ad, r5a, r5ad and t3a; only
  c5a and c5ad (and g5) have the 2nd Gen EPYC 7R32 (Rome). `find rome` used to
  return the four Naples families. They, and t3a, which had no codename, are
  now under `naples` (alias `zen 1`); `rome` (alias `zen 2`) is c5a, c5ad and
  g5. `avx2` still includes all of them.

- **`find` recognises `gpu`, `gpus`, `accelerator` and `accelerators`**
  (#233). They used to be ignored, so `truffle find "amd gpu"` printed
  `ignored unknown term: gpu` and, since #230, listed only CPU-only AMD
  families. They now mean any GPU or accelerator family, and with a CPU word
  they select the accelerator hosts with that CPU: `amd gpu` is g5, g6, g6e,
  p5, inf2, ...; `graviton gpu` is g5g; `gpu 24gb` is every accelerator type
  with at least 24 GiB of memory. `training` and `inference` are recognised
  too, as the chips AWS positions for each: training is H100, H200, A100,
  V100, B200, B300, Trainium and Trainium2; inference is A10G, T4, L4, L40S,
  Inferentia and Inferentia2. Name a GPU to get one outside its usual use
  case. Queries without these words are unchanged.

- **p3dn is a V100 family** (#233). The V100 entry listed only p3, so
  family-level matches such as `intel v100` left out p3dn.24xlarge.

- **`find` with no recognised words is an error, not a search of everything**
  (#229). A query made only of words truffle doesn't know (`find zzqx`,
  `find "gravitn xlrage"`, a bare number such as `find 8`) used to search
  every instance type in the region and price them all: 1354 rows and over
  two minutes. It now fails at once, names the unknown words, suggests the
  closest known word for each (`"gravitn" (did you mean "graviton"?)`) and
  lists the kinds of term it understands. A query that has some recognised
  words still runs, but now says which words it ignored before the results
  (`Note: ignored unknown term: "zzqx"`), not only when nothing matched.
  `find.ParseQuery` returns the new `*find.UnrecognizedQueryError` for such
  a query; `ParsedQuery.UnknownTerms` and `find.SuggestTerms` are also new.

- **Family and size names are query words** (#229). `find "g5 xlarge"` parsed
  to nothing, since `g5` is a family and `xlarge` a size rather than a size
  word, and so it searched everything. A family name now selects that family
  and a size name that size, so `find "g5 xlarge"` returns g5.xlarge,
  `find "m7i-flex 2xlarge"` returns m7i-flex.2xlarge, and `find "amd m7a"` or
  `find "g5 a10g"` combine as usual. The size words keep their ranges
  (`large` is still 2xlarge to 6xlarge, so `g5 large` is not g5.large; use
  the pattern `g5.large` for that). A bare `find g5` used to match nothing
  (the pattern `^g5$`); it now lists every g5 size. Families are recognised
  from a 2026-10-15 snapshot of DescribeInstanceTypes across five regions plus
  the Nitro table (`TRUFFLE_LIVE_AWS=1` checks it is complete), and
  `ParsedQuery` gains `Families` and `InstanceSizes`.

- **Constraints that no family satisfies together match nothing** (#229).
  `find "intel h100"` (every H100 host has an AMD CPU) or `find "g5 graviton"`
  resolved to an empty family set, which was treated as "any family", so they
  also searched the whole catalog. They now return no results, with a note
  that the constraints exclude each other and that `or` matches any of them.
  `ParsedQuery.HasNoMatchingFamily` reports this case.

- **`amd` and `intel` select every family with that host CPU** (#228). The
  vendor words used the processor-codename lists, which missed 13 AMD and 30
  Intel families, so `find "amd ..."` never returned t3a, x8aedz or hpc8a and
  `find "intel ..."` never returned t3, i4i, m5zn, d3, h1, i3, x1 and others.
  Vendor membership now comes from DescribeInstanceTypes'
  `ProcessorInfo.Manufacturer` (recorded across five regions). CPU words
  (`amd`, `intel`, `graviton`, codenames such as `milan`, and instruction sets)
  select CPU-only families: `find "amd 16 cores 64gb"` lists no GPU hosts.
  When the query also names a GPU or accelerator (`a10g`, `h100`, `nvidia`,
  `inferentia`, `trainium`, a GPU count, `mig`, or a GPU family such as `g5`),
  GPU and accelerator hosts count under their host CPU's maker: g5, g6 and p5
  are AMD, p4d, p5en and trn1 are Intel. So `find "amd a10g"` returns g5 and
  `find "intel h200"` returns p5en. The accelerator families are those with
  GPU, inference-accelerator, Neuron, FPGA or media-accelerator info in the
  same recorded catalog snapshot. The codename words gained the families whose CPU is
  documented: `turin` adds x8aedz and hpc8a (so `find "amd turin 16 cores
  64gb"` also lists x8aedz.6xlarge and up, below the closer fits), `ice lake`
  adds i4i, trn1 and hpc6id, `cascade lake` adds m5zn, d3, g4dn and p4d, and so
  on. x2iezn moves from `ice lake` to `cascade lake`, where its Xeon 8252C
  belongs. t8i, g7, g7e, p6-b300, mac1 and t3a match their vendor word but no
  codename.

- **`graviton` queries include r9g and r9gd** (#221). The Graviton5 list had
  m9g, m9gd, c9g and c9gd but not the memory-optimised r9g and r9gd, so
  `find "graviton large"` (and any vendor or processor query that should match
  Graviton5) left out r9g.2xlarge, r9g.4xlarge, r9gd.2xlarge and r9gd.4xlarge.
  Re-checked against DescribeInstanceTypes in us-west-2 and us-east-1: those
  were the only Graviton families missing.

- **Size words cover every instance size** (#220). `huge` matched a fixed list
  of sizes, so `find "amd huge"` listed c8a.24xlarge but not c8a.metal-24xl
  (the same shape on bare metal), and sizes such as 3xlarge, 6xlarge, 9xlarge,
  10xlarge, 18xlarge, 64xlarge, 96xlarge and 224xlarge were in no size word
  at all. The words now select by size: `tiny` is nano and micro, `small` is
  small and medium, `medium` is large and xlarge, `large` is 2xlarge up to
  6xlarge, and `huge` is 8xlarge and up, including every `metal` and
  `metal-Nxl` size (metal-Nxl counts as Nxlarge). A size EC2 adds later falls
  into the word its position says. `large` now also matches 3xlarge and
  6xlarge, and `huge` 9xlarge and up.

- **SageMaker price lookups retry throttling as hard as EC2 ones** (#219). The
  EC2 on-demand pricer retries a throttled Price List request up to 8 times
  (#175), but the SageMaker pricer used the SDK default of 3. Now that `find
  --service sagemaker` prices several types at once (#210), throttling is more
  likely there, and a throttled lookup shows N/A since SageMaker has no static
  fallback. Both pricers now build their Price List client the same way.

- **Availability-zone lists come out in the same order on every run** (#217).
  `find`, `search` and `az` listed each type's AZs in whatever order EC2
  returned them, which changes between calls, so the same query showed its AZs
  in a different order each time, and because the table shows only the first
  few, a different set of AZs. The lists are now in a fixed order in the
  table, JSON, YAML and CSV output, in `az` and `available`, and in the
  library's `SearchInstanceTypes` results: standard AZs alphabetically, then
  Local and Wavelength Zones, so the table leads with in-region AZs.
  Rows that differ only by AZ are also in a fixed order now: `spot
  --sort-by-price` when several AZs have the same price, and `capacity`
  reservations and blocks.

- **An error is printed once, and the usage text only follows a usage
  mistake** (#218). When a command failed at runtime (for example `find
  --service bogus`, `--local-zones` with `--skip-azs`, a failed search or a
  pricing timeout), truffle printed the error, then about 30 lines of usage,
  then the error again. It now prints `Error: ...` once. A mistyped flag, a
  wrong number of arguments or an unknown command still gets the error
  followed by the usage text (or, for an unknown command, a pointer to
  `--help`).

- **`truffle find` no longer times out pricing a cross-region search, and a
  pricing timeout is now an error** (#210). `find` looked up the on-demand price
  one row at a time, so `find "graviton 8 cores 32gb" --all-regions` (3161 rows)
  ran into the 5-minute `--timeout`, exited 0, and left 516 rows without a price,
  some described only as "may be out of date". Prices are now looked up once per
  type and region, a few at a time, with one Price List request per type across
  every region. Measured against real AWS (us-west-2 configured, 18 regions):

  | query | rows | before | after |
  |---|---|---|---|
  | `amd turin 16 cores 64gb` (configured region) | 28 | 4.9 s | 2.7 s |
  | `amd turin 16 cores 64gb --all-regions` | 184 | 29.6 s | 8.7 s |
  | `nvidia --all-regions` | 641 | 83.7 s | 19.6 s |
  | `graviton 8 cores 32gb --all-regions` | 3161 | 300 s (timed out, 516 unpriced) | ~45–50 s (all priced) |

  The rows and their order are unchanged for the queries that used to finish.
  If the deadline does expire while pricing, `find` still prints the results,
  shows N/A for the rows it never priced (rather than a static-table price), and
  exits 1 with `on-demand pricing timed out after …: no price for N of M rows`.
  Rows AWS publishes no price for get a one-line note instead of nothing.
  `--pick-first` now prices only the types that can still come first once price
  breaks a tie (the graviton query above: 300 s to 16 s), and `--local-zones`
  drops rows before pricing them. The pattern path (`find "m7i*" --show-price`)
  and `truffle search --show-price` use the same lookup, and `--pick-first`
  there no longer prices anything, since their order does not use price.
- **Size words in `truffle find` now apply when the query names no vendor,
  processor, GPU, instruction set or app** (#211). `find "arm64 large"` returned
  every size, from medium to metal (403 types in us-west-2), even though
  `--show-query` printed `Size: large`. It now returns only the 2xlarge and
  4xlarge sizes that "large" means (82 types), the same as `find "graviton
  large"` already did. `"x86_64 small"` drops from 942 types to the 19 small and
  medium ones. Sizes are now also applied when the query names only a GPU:
  `find "t4 medium"` returned all seven g4dn sizes and now returns g4dn.xlarge.
  A GPU that isn't sold in the size you ask for (`"a100 small"`) now returns no
  results, where before it returned every size of that GPU.

- **No spinner frames when output isn't a terminal** (#189). Piping or
  redirecting `truffle find`/`search` (e.g. `2>&1 | cat`, a log file, CI) used
  to write every animation frame — hundreds of `⠋ Finding … ⠙ …` updates ahead
  of the table. Off a terminal, or with `TERM=dumb`, the progress message is now
  printed once as a single plain line. Colour in tables and `truffle quotas` is
  likewise off when stdout isn't a terminal, when `NO_COLOR` is set, or with
  `TERM=dumb`, as well as with `--no-color`.

- **`make lint` is clean on `main` again, and CI now runs it** (#192). Contributor-facing
  only: the ten golangci-lint findings (unchecked writes, two staticcheck
  simplifications) are fixed without changing output, and a new `Lint` CI job runs
  `make lint` with golangci-lint pinned to 2.14.0 and its download verified against a
  pinned SHA256. `make lint` warns if your local version differs from the pin.
- **The inert-flag CI gate now sees flags registered through a local alias, and
  fails instead of passing when it finds no flags at all** (#214).
  Contributor-facing only: `f := cmd.Flags(); f.StringVar(...)` registrations
  were invisible to the gate, so an unread flag registered that way would have
  shipped unnoticed, and a parser regression that discovered nothing would have
  reported the package clean. Ported from lagotto's copy of the gate. No
  existing truffle flag was affected.
- **`truffle find` lists the closest fit first, not the biggest box** (#187).
  Size terms like `16 cores 64gb` are minimums, and the table re-sorted every
  match largest-first, so `truffle find "amd turin 16 cores 64gb"` opened with a
  $15/hr 192-vCPU `r8a.48xlarge` and put the requested `m8a.4xlarge` on the 28th
  row (off-screen across all regions). Results now rank by how far each type
  overshoots the request — each specified dimension (vCPU, memory, physical
  cores) adds |log2(actual/requested)|, so an exact match ranks first and
  doubling either dimension costs the same — then cheapest, then newest
  generation. A type's rows stay together across regions, cheapest region
  first. A one-line hint under the table (stderr, table output only) says
  e.g. "Showing 28 types with ≥16 vCPU and ≥64 GiB memory, closest fit first;
  use --exact for an exact match". JSON/YAML/CSV and `--pick-first` now use the
  same order as the table (`--pick-first` therefore returns the closest fit).
  The table also now honors the `cheap`/`expensive`/`fast`/`newest` sort
  keywords, which it previously overrode with its own largest-first order.
  Queries without size terms keep newest-generation-first.
  Library: `output.TableOptions` gains a `PreserveOrder` field (additive; the
  zero value keeps the historical largest-first table order).
- **`truffle list` now honours `--regions` / `-r` and your configured region**
  (#202). It had its own `--region` flag, defaulting to us-east-1, which hid the
  global one: `truffle list --regions us-west-2` silently listed us-east-1, and
  with no flag it always listed us-east-1 whatever region you had configured.
  Some instance types are region-limited, so the list could include types you
  can't launch where you work. `list` now uses the same region lookup as
  `quotas` and `available`: the first `--region` / `--regions` given, else the
  spore.host config region, then `AWS_REGION` / `AWS_DEFAULT_REGION`, then your
  AWS profile's region, and us-east-1 only when nothing is configured (a note
  on stderr says so). `--region X` still works, now through the global alias.
  A new test fails any command that calls a regional AWS API without reading
  the shared region flag.
- **`truffle az --regions-only` now does what it says** (#199). The flag was
  accepted but ignored, so it printed the same per-instance-type table as
  without it. It now shows only the regions that meet the AZ requirement, one
  row per region with its AZ count, the AZs, and the matching instance types
  (e.g. `truffle az m7i.large --min-az-count 3 --regions-only`). With
  `-o json`/`yaml`/`csv` it emits that region list (`region`, `az_count`,
  `availability_zones`, `instance_types`) instead of per-type records.

- **`truffle --account <id>` now actually guards the account** (#206). The flag
  ("Expected AWS account ID (optional guard)") was parsed and ignored, so a
  command ran against whatever account your credentials belonged to. Now, when
  you pass it, truffle checks the resolved credentials with STS
  `GetCallerIdentity` before doing anything else, and refuses on a mismatch or
  if the account can't be determined. Only the explicit flag is enforced;
  `SPORE_ACCOUNT` and the shared config's `account` are not (see #206).

- **`truffle find` no longer fails with `Missing Region` when no AWS region is
  configured** (#188). Enabled-region discovery (`GetEnabledRegions`) now falls
  back to the us-east-1 endpoint in that case, for every command that uses it.

- **`make check-release-version TAG=…` works again, and `make` no longer warns
  about `nitro-census` on every run** (#197). Contributor-facing only, since the
  release workflow calls the script directly. A bad merge in #180 defined
  `nitro-census` twice and turned the `check-release-version` help comment into a
  rule, so the pre-tag guard stopped with "No rule to make target `Release'".
  `make help` now lists every target, including `nitro-census`, `changelog-fix`,
  `dev` and `release`, and lists only targets. A description containing a colon no
  longer misaligns the help table. A new hygiene test dry-runs every Makefile
  target. It fails on a duplicate target, a `##` help line that isn't
  `## <target>: <description>`, or a target that is missing from `make help`.
- **A throttled Price List no longer refuses a Graviton launch that carries a cost
  limit** (#175). The static fallback required an exact `(region, instanceType)`
  hit in `libs/pricing`, and that table had **no Graviton family in any region** —
  no `c6g`–`c9g`, no `m6g`–`m8g`, no `r6g`–`r9g`. So for the entire Graviton line
  the fallback could not fire, and any `GetProducts` hiccup meant a hard refusal:
  reported from a real eight-way fan-out where `c7g.2xlarge` in us-west-2 with
  `--cost-limit 0.15` died outright.
  That was the opposite of the intended degradation order. Failing closed is
  correct and #114 settled it; the problem was that the thing it fell back to had
  no coverage of the architecture most of this suite's users run. Fixed by bumping
  `libs` to **v0.51.0**, which adds **405 real Graviton rates** fetched from the
  Price List API across all eight regions the table covers.
  Guarded here rather than only upstream, since this is where the refusal
  happened: nine mature families × five sizes × eight regions are now asserted
  present, the reported throttling error is asserted to degrade to the static
  table *and* to report `PriceSourceStatic` so a caller can still tell a degraded
  price from a live one, and `c9g`/`r9g` — which AWS does not yet offer
  everywhere — are held to a coverage floor plus a no-partial-ladder rule instead
  of a hardcoded region list, so a normal AWS rollout cannot turn the build red
  while a coverage regression still does.

### Security

- **Rebuilt against Go 1.26.9 and `golang.org/x/net` v0.60.0** for the Go
  vulnerabilities disclosed 2026-10-09: GO-2026-6605, 6607, 6608, 6610, 6611,
  6612, 6613 and 6617, across `net/http`, `crypto/tls`, `mime/multipart`,
  `net/http/httputil`, `net/textproto` and x/net's HTTP/2 implementation. The CI
  Go pin moves 1.26.8 → 1.26.9 in all workflows.

## [0.58.0] - 2026-10-07

### Added

- **`Capabilities` now reports the hypervisor and the Nitro System generation.**
  Two new fields, and they answer different questions from different sources.
  `Hypervisor` is EC2's own `nitro`/`xen` answer from `DescribeInstanceTypes` —
  authoritative, free, and per instance type. `NitroGeneration` is the Nitro
  version (2–6), which the **API does not expose at all**: EC2's `Hypervisor`
  field distinguishes Nitro from Xen but carries no version, and AWS publishes the
  version only in documentation. So that one comes from a table, and `0` means
  "this family is not classified" rather than "not Nitro" — `Hypervisor` answers
  the latter.
  The version is worth having because the generations differ in ways that bite: v4
  added ENA Express and RDMA, v5 raised the per-network-card ceiling to 200 Gbps,
  and v6 raised it to 400 Gbps *and* cut the default idle TCP timeout from 432,000
  seconds to 350 — which silently breaks long-lived idle connections.
- **`make nitro-census` keeps that table honest against AWS.** A hand-maintained
  table whose staleness is invisible is the thing this project keeps getting
  burned by, so the table ships with two networked gates.
  **Contradiction**: every family the table classifies must be reported as `nitro`
  by EC2 — which validates the one dimension the API *can* validate, automatically.
  **Coverage**: every Nitro family EC2 offers must be classified, or the check
  fails and names the ones that are not. That second gate is the new-Nitro-card
  detector: a new card always arrives carried by new instance families, so an
  unclassified family is exactly the signal to go read the docs. It cannot tell you
  the new card is v7 — nothing can, from the API — but it fires precisely when
  someone needs to look, which is the difference between a stale table and a table
  that says it is stale.
  Both gates found real problems on their first run against AWS. Nine Mac families
  were flagged because bare-metal-only types report no hypervisor at all, which is
  the field not applying rather than disagreement; and `i3` was classified Nitro v2
  because the source lists "I3" under bare *metal* only, while every virtualized
  `i3` size reports `xen`. `i3` is now unclassified, which is correct: a
  family-level key cannot express "metal sizes only", and it does not need to,
  because `Hypervisor` is per-type.
  The table records its source URL and an as-of date, so its age is a fact in the
  file rather than a guess by whoever reads it next.

## [0.57.2] - 2026-10-06

### Added

- **The changelog policy is now enforced in CI rather than by habit.** A PR that changes
  non-test Go source without touching `CHANGELOG.md` fails, and `changelog_test.go`
  checks `[Unreleased]` for duplicate group headings, unknown group names, entries
  outside a group, and releases missing a compare link.
  This policy has been suite-wide for a while but only `spawn` enforced it — where it
  immediately earned its keep, catching a duplicate `### Fixed` **four times in one
  session** and a PR that had merged with no entry at all (found only at the next
  release, against an empty `[Unreleased]`, with the entries reconstructed from the diff
  at tag time).
  `scripts/changelog-consolidate.py` (and `make changelog-fix` where there's a Makefile)
  merges duplicate groups mechanically, because two PRs each adding their own
  `### Fixed` is a routine conflict that merges cleanly for git and badly for the format
  — not a mistake worth hand-fixing each time.

### Changed

- **Dependencies**: `aws-sdk-go-v2/service/ec2` 1.332.0 → 1.335.0, `config` 1.33.4 →
  1.33.5, `credentials` 1.20.4 → 1.20.5, `smithy-go` 1.28.1 → 1.28.2, `tablewriter`
  1.1.4 → 1.1.5, and the substrate test dependency 0.114.0 → **0.120.0**.

### Fixed

- **A throttled Price List call refused any Graviton launch with a cost limit**
  (#175). `GetProducts` ran on the SDK's default 3-attempt retry budget, which is
  not enough for this API under fan-out: `spawn task run` is **one process per
  launch**, so eight concurrent launches are eight cold caches making eight
  `GetProducts` calls at once against a famously low rate limit. The 24-hour
  in-process cache is the right design for a long-lived process and can never
  amortise the case that actually throttles.
  With `--cost-limit` set, truffle correctly fails closed when no price resolves
  (#114), so one `ThrottlingException: Rate exceeded` refused the launch outright.
  The budget is now 8 attempts, using the SDK's existing exponential backoff with
  jitter — tens of seconds at worst, against a launch about to run for minutes.
  Fixed by making the live lookup succeed rather than by widening the static
  fallback: a hand-maintained price substituted into a spend cap is stale by
  construction and its staleness is invisible at the cap, which is the same
  objection #114 raised against a fabricated price.

- **Three tests asserted on gaps in the test emulator rather than on truffle's own
  behaviour**, and failed on a routine dependency bump with nothing wrong in truffle
  (#174). substrate 0.120.0 implements `DescribeCapacityReservations` and
  `GetSpotPlacementScores` (the latter seedably, substrate#892) — operations it
  previously answered `InvalidAction` for.
  Two capacity-reservation tests checked the #63 contract that a total failure must
  surface as an error instead of reading as "zero results", but produced that failure by
  relying on substrate rejecting the operation. They now force it with
  `newUnreachableClient` (a closed port), which fails deterministically and will keep
  doing so: a contract about *what happens when a call fails* should not depend on which
  calls an emulator has gotten around to implementing. `GetCapacityBlocks` was converted
  with them — it still passes, but was one emulator release from the same spurious
  failure.
  The third asserted that a spot placement score was absent, which described the
  emulator rather than truffle. The partial-answer contract it exists to protect ("a
  missing signal must never be silent") is kept, but no longer names which signal is
  missing. Its inverse is now covered too: that an available score actually reaches the
  caller, on the documented 1-10 scale and naming its AZ ID — which nothing had ever
  checked.

## [0.57.1] - 2026-09-25

### Fixed
- **`truffle quotas --request` now generates a command for a quota that is zero**
  (#171) — previously the most common reason to need an increase produced no
  output at all. A new account typically defaults the GPU families (P/G/DL/Trn)
  to **0**, so the person asking "why can't I launch any p5?" was exactly the
  person who got nothing: `quota == 0` sat in the *skip* condition even though the
  comment above it said it generated requests "for quotas that are zero or nearly
  full". The per-family starting values that were meant to handle this (192 vCPUs
  for P — one p5.48xlarge — 128 for G, 32 otherwise) were unreachable dead code
  and now apply. Each suggestion also says why it's there: either
  `quota is 0 — no P instance can launch until this is raised`, or how much of the
  quota is currently in use.

## [0.57.0] - 2026-09-25

### Fixed
- **Quota-increase commands no longer tell you to raise the wrong quota** (#167).
  `truffle quotas`' copy-pasteable
  `aws service-quotas request-service-quota-increase` block only knew the codes
  for a handful of instance families and silently fell back to the **Standard
  On-Demand** code (`L-1216C47A`) for the rest — so an X, DL or F on-demand
  shortfall, and **every** Spot shortfall outside G and P, produced a command that
  raised an unrelated limit while looking authoritative. Worse, a Spot problem was
  answered with an On-Demand quota labelled "Standard On-Demand". You would submit
  the request, wait a day or two for approval, and still be blocked by the quota
  you actually needed. All eight families are now mapped for both On-Demand and
  Spot, the family/lifecycle label matches the code, and the mapping is shared
  with the code that *reads* your quotas so the two can't drift apart again. A
  family the tool has no code for now prints a Service Quotas console pointer
  instead of a confidently wrong command.
- **`truffle quotas --request` now covers all eight families** (#167). Its
  suggestion loop stopped at Trn, so an X, DL or F shortfall produced no
  quota-increase command at all; it now walks the same eight families the quota
  table displays. It also skips families whose quota could not be read, instead of
  reading the missing value as 0.
- **A quota truffle couldn't read is no longer reported as "your quota is 0"**
  (#167). Per-family quota lookups that failed — most often a missing
  `servicequotas:GetServiceQuota` permission, but also throttling or a region that
  doesn't offer the quota — were discarded without even a log line, leaving
  "limit unknown" indistinguishable from a genuine zero limit. `CanLaunch` would
  then advise "quota for G instances is 0 (request quota increase)" when the real
  fix was an IAM grant. Failed lookups are now logged and reported as
  *undetermined*, naming the underlying error; a genuine zero still gets the
  quota-increase advice. **User-visible message change:** the explanation string
  for a family whose quota could not be read is now "could not determine the …
  vCPU quota … (limit unknown, not zero)" rather than "… is 0 (request quota
  increase)". The launch/no-launch verdict itself is unchanged.

### Added
- **Per-family quota-lookup errors on `QuotaInfo`** (#167, library API):
  `OnDemandErrors` / `SpotErrors` record which families' quota reads failed and
  why, with `QuotaInfo.LookupError(family, spot)` and
  `QuotaInfo.MissingFamilies(spot)` helpers. A failed family's key is still absent
  from `OnDemand` / `Spot`, so a two-value map read keeps distinguishing "unknown"
  from a real zero — these fields add the *reason*. Existing fields and behavior
  are unchanged.
- **`quotas.VCPUsForType(instanceType) (int32, bool)`** (#167, library API): the
  exported form of the internal size-suffix → vCPU heuristic, for converting a
  per-family vCPU quota into an instance count. `ok=false` (with a 0 count that
  must not be read as "zero vCPUs") marks input the heuristic can't count — a
  wildcard like `g6e.*`, a family-only pattern, an empty string, or an
  unrecognized size — which keeps the heuristic's limits visible at the call site.
  As the doc comment notes, `DescribeInstanceTypes` remains authoritative.
- **`quotas.QuotaCodeFor(family, spot) (string, bool)`** (#167, library API): the
  single source of truth for the (family, lifecycle) → Service Quotas code
  mapping, with `ok=false` for an unmapped pair so callers can point at the
  console instead of substituting another family's code.

## [0.56.0] - 2026-09-16

### Added
- **`--local-zones` targeting filter on `find` and `az`** (#164). When set, the
  flag restricts availability-zone results to Local Zones and Wavelength Zones —
  "show me what's available at the edge" — keeping only the AZs whose authoritative
  `ZoneType` is `local-zone` or `wavelength-zone` and dropping any instance type
  with no edge-zone availability. It reuses the existing zone classifier, so no
  extra `DescribeAvailabilityZones` calls are added. The flag is opt-in and
  non-breaking: without it, both commands behave exactly as before (all AZs, with
  Local/Wavelength entries labeled by the `†` marker). On `find`, `--local-zones`
  is mutually exclusive with `--skip-azs` — filtering to edge zones needs AZ data,
  so combining the two is rejected with a clear error. This is the inverse of the
  existing `truffle spot --local-zones`, which by default *excludes* edge zones.

## [0.55.0] - 2026-09-15

### Added
- **Real Local Zone awareness** built on `DescribeAvailabilityZones` (#164).
  truffle now reads each zone's authoritative `ZoneType`
  (availability-zone / local-zone / wavelength-zone) along with its opt-in
  status, parent zone, and network-border group, cached per region. `find`,
  `search`, and `az` now label Local Zone and Wavelength Zone entries in the
  availability-zone column with a `†` marker plus a footer legend, so an edge
  location is no longer indistinguishable from a standard in-region AZ. The
  classification is also exported for library consumers (`Client.ZoneInfos`,
  `Client.LookupZone`, `Client.IsLocalZone`, and the `ZoneInfo` type), and is
  queried with `AllAvailabilityZones=true` so Local/Wavelength zones the
  account hasn't opted into are visible too.

### Fixed
- **`truffle spot` now classifies Local Zones by their real `ZoneType`**
  instead of a brittle zone-name-length heuristic (#163). A zone is excluded
  from spot results (unless `--local-zones` is set) if and only if
  `DescribeAvailabilityZones` reports its `ZoneType` as `local-zone` or
  `wavelength-zone` — not because its name happened to be longer than the
  region name plus one letter. The `--local-zones` flag and its default-off
  behavior are unchanged; only the classification mechanism is now accurate.
  Zone lookups degrade safely: if the API call fails, the zone is treated as a
  standard AZ (kept), matching the previous offline behavior.

## [0.54.0] - 2026-09-09

### Added
- **`truffle available` now reports the on-demand price** for the instance
  type alongside its obtainability signals, labeled with its source
  (`$0.7838/hr on-demand (live)` from the live AWS Price List vs
  `(static fallback)` from truffle's embedded table), so the rate `spawn`'s
  dry-run resolves is also visible at discovery time. When the price can't be
  resolved it reads `unavailable` rather than showing a fabricated number, and
  the JSON/YAML output carries `on_demand_price`/`on_demand_price_source` too
  (#159). (`truffle find` already surfaces on-demand price via `--show-price`,
  and by default for natural-language queries.)

## [0.53.3] - 2026-09-04

## [0.53.2] - 2026-09-03

### Security
- Routine minor/patch dependency updates via Dependabot: AWS SDK Go v2 core,
  `config`, `credentials`, and the `ec2`, `pricing`, `servicequotas` service
  clients (all patch bumps), `smithy-go` (patch bump), and
  `github.com/scttfrdmn/substrate` (0.97.0 to 0.109.0) (#157). Also bumps
  `google.golang.org/grpc` (an indirect dependency pulled in via
  `pkg/testutil` → substrate's test emulator → OpenTelemetry's OTLP/gRPC
  exporter) from v1.83.0 to v1.83.2 to pick up the fix for CVE-2026-84304
  (HIGH), which the post-merge Trivy scan flagged.

## [0.53.1] - 2026-08-29

### Security
- Routine minor/patch dependency updates via Dependabot: AWS SDK Go v2 core,
  `config`, `credentials`, and the `ec2`, `pricing`, `servicequotas` service
  clients (all patch bumps), `smithy-go` (patch bump), and
  `github.com/scttfrdmn/substrate` (0.97.0 to 0.102.0) (#156).

## [0.53.0] - 2026-08-19

### Changed
- **`find` and `spot` accept multiple explicit instance types/patterns as
  separate arguments and render them as rows in one comparison table**
  instead of requiring one invocation per type — `truffle find g5.2xlarge
  g5.4xlarge g5.12xlarge` or `truffle spot g5.2xlarge g5.4xlarge` now runs a
  single search instead of erroring (`spot` previously rejected anything
  beyond one argument outright) or misinterpreting the extra types as a
  natural-language query. Multi-word NL queries whose words simply arrived
  as separate argv entries (`truffle find 8 cores 32gb`) are unaffected —
  the multi-type path only activates when EVERY argument individually
  resolves as an instance-type pattern (#52).

### Added
- **`Client.SearchInstanceTypesByRegion`**, a library-oriented sibling of
  `SearchInstanceTypes` that returns a `[]RegionResult` (region, error, count)
  alongside the combined results (#117). `SearchInstanceTypes` previously
  signaled a partial region failure only via an unconditional stderr warning —
  unobservable to an in-process (library) caller, which had no way to tell
  "this region has no matches" from "this region's query failed" without
  intercepting a process-global fd and parsing an emoji-prefixed string.
  `SearchInstanceTypes` itself is now a thin wrapper over the new method and
  keeps its original signature and CLI warning, so existing callers are
  unaffected.

### Fixed
- **`truffle version` reported a hardcoded `0.1.0` on any build made from
  source**, regardless of how many releases had shipped, because the
  fallback default was a hand-maintained constant nothing updated (#121).
  It also meant every source build's on-demand update check compared against
  that stale number and claimed an upgrade was always available. `cmd.Version`
  now defaults to empty and falls back through a new `pkg/buildinfo` package
  (ported from spawn's `pkg/buildinfo`, spore-host/spawn#483) to the Go
  toolchain's own build-stamp metadata, landing on an explicit `dev` sentinel
  only when neither is available — never a plausible-looking number. A new
  `scripts/check-release-version.sh` (wired into the release workflow and
  runnable via `make check-release-version TAG=vX.Y.Z`) builds with the real
  release ldflag and asserts the binary reports the tag, since `-X` targeting
  a renamed/missing symbol fails silently at link time.
- **`aws.ErrCapacityBlockIneligible`** — `GetCapacityBlockOfferings` now
  distinguishes "this instance type is not a Capacity Block type here" from a
  genuine query failure (AccessDenied, throttling) instead of collapsing both
  into the same opaque "all region queries failed" message. Callers can check
  for it with `errors.Is` (#110).
- **`GetCapacityBlockOfferings` dropped every per-region error**, printing
  them only under `--verbose` and always returning `(results, nil)` — the
  same bug class already fixed in #63/#109 for other discovery paths, but
  reintroduced here. Since "no Capacity Block offerings" is a common,
  legitimate answer for this query, a failed query (expired credentials, an
  SCP denial, a throttle) was indistinguishable from genuinely sold-out
  inventory. Now returns an error when every queried region fails, matching
  `SearchInstanceTypes`/`GetSpotPricing` (#110).
- **`GetCapacityReservations` and `GetCapacityBlocks` had the same bug** —
  both discarded every per-region error and always returned `(results, nil)`,
  so a total failure across all regions read as "zero reservations"/"zero
  blocks" instead of surfacing the failure. Fixed to match the same contract
  (#110).
- **`find`/`search`/`ResolveCard` didn't recognize hyphenated or `!`/`+`-suffixed
  GPU spec strings** — Modal's documented GPU naming convention
  (`RTX-PRO-6000`, `A100-80GB`, `H100!`, `B200+`) arrived as a single
  whitespace-delimited token and matched no vocabulary entry, since the
  tokenizer only split on spaces. `pkg/find.ParseQuery` now re-tokenizes a
  word that fails every other classification by splitting on hyphens and
  stripping a trailing `!`/`+` first, so e.g. `"rtx-pro-6000"` resolves
  exactly like the already-working `"rtx pro 6000"`. A second, independent
  bug in the CLI's pattern-vs-natural-language routing heuristic
  (`looksLikePattern`) also needed fixing: `+` is a regex indicator so
  `"B200+"` never reached the vocabulary check at all, and the check itself
  compared against lowercase-keyed tables without lowercasing its input
  first (silently masked for most terms by an accidental last-resort digit
  regex, but not for a hyphenated query like `"A100-80GB"`). Both are fixed
  via a new exported `find.IsRecognizedTerm`, checked case-insensitively
  before any regex-indicator check (#130).

### Added
- **`find`/`search --show-gpu-ratios`** — adds `vCPU/GPU` and `RAM/GPU`
  columns for GPU instance types, computed from the same specs already in
  the table (`vcpus / gpus`, `memory / gpus`). For CPU/IO-bound ML workloads
  (data prep or I/O-heavy inference, where the GPU sits idle waiting on
  input) this ratio decides whether a bigger, more-GPU instance actually
  helps — e.g. `g5.4xlarge` (16 vCPU / 1 GPU = 16.0) vs `g5.12xlarge` (48
  vCPU / 4 GPUs = 12.0): the naive "4 GPUs → 4x throughput" reading is
  backwards here, since the bigger box feeds each GPU worse, not better.
  A fractional GPU (e.g. `g6f.large`, a time-sliced L4 partition) shows `-`
  for both columns rather than a number computed against a shared,
  caller-uncontrolled slice (#51).

### Fixed
- **`find`/`search` table output: region-name alignment and numeric-column
  justification.** A spawn-supported region's `✓ ` marker shifted its region
  name two characters right of an unsupported region's name in the same
  column, reading as ragged rather than tabular — an unsupported row now gets
  an equal-width blank pad instead. Numeric columns (vCPUs, Memory, GPUs,
  VRAM, price, Train Quota) are now right-justified instead of left-aligned,
  so values of differing digit width line up on their ones place.
- **`find`/`search` table output: instance-type groups and within-group rows
  had no defined order.** Groups (one per matched instance type) rendered in
  Go's randomized map-iteration order — rerunning the identical query could
  print the results in a different order each time. Groups now render
  largest-to-smallest by vCPU, then Memory; within a group, rows (one per
  region/AZ) now sort by price descending, highest first.

## [0.52.1] - 2026-08-17

## [0.52.0] - 2026-08-17

### Changed
- **`find`/`search` multi-constraint queries now combine dimensions (vendor,
  processor, GPU, instruction set, EFA, MIG, network speed) with AND by
  default instead of OR** — e.g. `truffle find "h100 efa"` now means "H100
  instances that also have EFA" (family `p5` only), not "any H100 OR any
  EFA-capable family" (previously 50+ families). Add the literal word `or`
  anywhere in the query to restore the old union behavior (`truffle find
  "h100 or efa"`); `and` is also accepted explicitly (a no-op, since it's
  the default). Multiple values within a single dimension are unaffected
  and always still mean OR (`"a100 h100"` still means A100-or-H100). This is
  a genuine behavior change: some existing multi-dimension queries will now
  return fewer (but correct) results (#144).

### Fixed
- **`find`'s GPU queries silently discarded every other constraint** — a
  query naming a GPU (e.g. `"h100 efa"`) took an exact-instance-type
  shortcut straight from the GPU term and never even evaluated EFA, MIG,
  vendor, processor, instruction-set, or network-speed constraints in the
  same query. Now the shortcut is only taken when GPU is the sole
  constraint present; otherwise GPU's family (not exact instance type)
  combines with the other active constraints like every other dimension
  (#144).

## [0.51.0] - 2026-08-16

### Added
- **`find`/`search mig`** — matches only GPU instances supporting NVIDIA
  Multi-Instance GPU partitioning (A100, H100, H200, B200, RTX PRO
  6000/4500 — confirmed against NVIDIA's own MIG User Guide "Supported
  GPUs" table; NOT L4/L40S/T4/A10G/V100/B300). `find/search mps` matches
  ANY NVIDIA GPU, since MPS (Multi-Process Service) is a CUDA runtime
  daemon rather than a fixed hardware capability — resolves to the same
  family list as the existing `nvidia` vendor entry rather than being
  unrecognized (#143).
- **`find`/`search` instruction-set query terms**: `avx2`, `avx-512` (aliases
  `avx512`, `avx-512f`, `avx 512`), `sve`, `sve2` — for finding instances by
  CPU capability (e.g. `truffle find avx-512`, `truffle find "sve2 graviton"`)
  without needing to know which processor generation or AWS instance family
  provides it. AWS's API exposes no instruction-set data, so this is a
  hand-maintained table (`pkg/metadata/instructionsets.go`) derived from
  `ProcessorDatabase`'s own (now-corrected, see below) family lists (#140).

### Fixed
- **`ProcessorDatabase`'s family lists were missing most of each processor
  generation's real AWS instance families** — every AZ/storage/network
  variant suffix (`-d`, `-n`, `-dn`, `-flex`, `-b`, `-in`, `-ine`, `-zn`, ...)
  was absent, only the bare family name was listed. Also added the entirely
  missing **Granite Rapids** (`m8i`/`c8i`/`r8i`/`x8i` — previously
  mis-attributed to `emerald rapids`, which is actually just `i7i`/`i7ie`)
  and **Graviton5** (`m9g`/`m9gd`/`c9g`/`c9gd`) generations. Verified against
  live `DescribeInstanceTypes` output cross-referenced with each family's
  documented processor. This directly affects `find`'s processor/vendor
  search (e.g. `truffle find "ice lake"` previously missed most Ice
  Lake–based instance types) and is what the new instruction-set terms above
  are built on.
- **`GPUDatabase`'s `l4` entry was missing `gr6`, `g6f`, and `gr6f`** — the
  graphics-optimized and fractional/time-sliced L4 partition families (the
  same fractional-GPU family the VRAM display fix in #116 covers). `h200`
  was missing `p5en`. `truffle find "nvidia l4"` previously never surfaced
  these.
- **`GPUDatabase`'s `b200`/`b300`/`nvidia` entries listed the instance family
  as bare `p6`**, which is not a real AWS family prefix — the actual prefix
  is `p6-b200`/`p6e-gb200`/`p6-b300`. This meant the family-based fuzzy-match
  path (used by `mig`/`mps`/`nvidia` above, and by size-constrained queries
  like `"b200 large"`) silently never matched any B200/B300 instance; only
  an exact-instance-type match (`b200`/`b300` typed alone) worked (#143).
- **`find`'s single-word routing heuristic (`looksLikePattern`) misrouted
  vocabulary terms ending in a digit** (e.g. `a100`, `h100`, `avx2`, `sve2`)
  to the literal instance-type pattern matcher instead of the natural-language
  parser, since they match the same shape as a real instance family +
  generation number (`c6i`, `trn1`). This silently returned zero results for
  `truffle find a100`/`h100`/`v100`/`t4` and any of this PR's new instruction
  sets ending in a digit. Fixed by checking the GPU/processor/instruction-set
  vocabulary tables before applying the digit-suffix heuristic.
- **`find`'s `--skip-azs` flag didn't actually skip the search-time
  availability-zone lookup** — it only suppressed the AZ column in table
  output. `pkg/find.BuildCriteria` hardcoded `FilterOptions.IncludeAZs: true`
  regardless of the flag, so every matched instance type still triggered a
  separate, serial `DescribeInstanceTypeOfferings` API call during the
  search itself. For a broad query (e.g. `avx2`, resolving to ~90 families)
  this took minutes and looked hung — confirmed via `time` that AWS's own
  API returns the full instance-type catalog in ~9s, so the multi-minute
  wall clock was almost entirely truffle-side serial I/O wait, not AWS
  latency. `BuildCriteria` now takes an explicit `includeAZs` parameter
  threaded from `--skip-azs` (#141 — the AZ-lookup loop itself is still
  serial, not parallelized; a broad query is now correct and much faster,
  but not yet as fast as it could be, tracked as a follow-up).

## [0.50.0] - 2026-08-16

### Added
- **`find`/`search --show-real-cores`** — vCPU column also shows the
  physical CPU core count (format `vCPU/CPU`, e.g. `96/48`), for comparing
  compute density across families with different threads-per-core
  (Graviton: 1, most x86: 2) (#136).
- **`find`/`search --show-mem-per-cpu`** — memory column appends a
  per-physical-core figure, e.g. `384.0 (8.0/core)`, dividing by physical
  cores rather than vCPUs (#137).
- **`find`/`search --price-unit hour|minute|second`** (default `hour`) —
  displays the on-demand price column per minute or per second instead of
  per hour; the underlying `$/hr` rate and CSV/JSON/YAML output are
  unchanged (#138).
- **`aws.InstanceTypeResult.GPUMemoryPerMiB`, `GPUPartitionSize`,
  `LogicalGPUs`** — per-GPU VRAM and fractional-GPU metadata, populated
  directly from each GPU device's own `MemoryInfo`/`GpuPartitionSize`
  fields rather than derived by dividing the aggregate (which broke for
  fractional-GPU types like `g6f.large` reporting `GPUs == 0`). `find`/
  `search`'s VRAM column now shows per-GPU VRAM alongside the existing
  total (e.g. `96 (24/gpu)`), and fractional-GPU rows now get GPU columns
  at all instead of being silently excluded (Closes #116).
- **`output.TableOptions`, `Printer.PrintTableWithOptions`** — a new
  options struct supersedes ad hoc positional bool parameters for `find`/
  `search`'s table renderer, now that this PR's toggle count made that
  unwieldy; `PrintTable`/`PrintTableWithQuota` remain as unchanged
  convenience wrappers.

### Changed
- **`find`/`search` table**: the spawn-supported `✓` indicator now renders
  *before* the region name (`✓ us-east-1`) instead of after it
  (`us-east-1 ✓`), for easier scanning (#135).

## [0.49.1] - 2026-08-11

### Changed
- Dependency maintenance: bumped 8 minor/patch Go module dependencies and 5
  GitHub Actions to their current releases (no API or behavior change).

## [0.49.0] - 2026-08-10

### Added
- **`Capabilities.VCPUs`** — the instance type's default vCPU count
  (`DescribeInstanceTypes`' `VCpuInfo.DefaultVCpus`), so a caller can convert a
  per-family vCPU quota (`pkg/quotas.QuotaInfo`) into an instance-count ceiling
  without re-parsing the type's size suffix — the same name-based guessing
  `pkg/quotas.getVCPUCount` already falls back to, which `obtainability.go`'s
  own comment flags as unreliable for non-linear sizes (spawn#492).

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

[Unreleased]: https://github.com/spore-host/truffle/compare/v0.61.0...HEAD
[0.61.0]: https://github.com/spore-host/truffle/compare/v0.60.0...v0.61.0
[0.60.0]: https://github.com/spore-host/truffle/compare/v0.59.0...v0.60.0
[0.59.0]: https://github.com/spore-host/truffle/compare/v0.58.0...v0.59.0
[0.58.0]: https://github.com/spore-host/truffle/compare/v0.57.2...v0.58.0
[0.57.2]: https://github.com/spore-host/truffle/compare/v0.57.1...v0.57.2
[0.57.1]: https://github.com/spore-host/truffle/compare/v0.57.0...v0.57.1
[0.57.0]: https://github.com/spore-host/truffle/compare/v0.56.0...v0.57.0
[0.56.0]: https://github.com/spore-host/truffle/compare/v0.55.0...v0.56.0
[0.55.0]: https://github.com/spore-host/truffle/compare/v0.54.0...v0.55.0
[0.54.0]: https://github.com/spore-host/truffle/compare/v0.53.3...v0.54.0
[0.53.3]: https://github.com/spore-host/truffle/compare/v0.53.2...v0.53.3
[0.53.2]: https://github.com/spore-host/truffle/compare/v0.53.1...v0.53.2
[0.53.1]: https://github.com/spore-host/truffle/compare/v0.53.0...v0.53.1
[0.53.0]: https://github.com/spore-host/truffle/compare/v0.52.1...v0.53.0
[0.52.1]: https://github.com/spore-host/truffle/compare/v0.52.0...v0.52.1
[0.52.0]: https://github.com/spore-host/truffle/compare/v0.51.0...v0.52.0
[0.51.0]: https://github.com/spore-host/truffle/compare/v0.50.0...v0.51.0
[0.50.0]: https://github.com/spore-host/truffle/compare/v0.49.1...v0.50.0
[0.49.1]: https://github.com/spore-host/truffle/compare/v0.49.0...v0.49.1
[0.49.0]: https://github.com/spore-host/truffle/compare/v0.48.1...v0.49.0
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
