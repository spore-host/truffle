# SageMaker (`ml.*`) instance discovery

truffle can discover **SageMaker `ml.*` instance types** — the managed-ML
namespace — in addition to raw EC2. SageMaker `ml.*` types (e.g.
`ml.g5.2xlarge`) run on the same hardware as their EC2 counterparts
(`g5.2xlarge`) but are a **distinct namespace** with their own pricing (a
management premium) and their own service quotas.

Discovery is **opt-in**: pass `--service sagemaker` to `find` or `search`.
Without it, both commands behave exactly as before (EC2 only).

```bash
# Discover ml.* types (glob, regex, or natural language)
truffle find "ml.g5.*"      --service sagemaker --regions us-east-1
truffle search "ml.g5.*"    --service sagemaker --regions us-east-1 --show-price
truffle find "nvidia a10g"  --service sagemaker --regions us-east-1

# Include the per-type training-job quota
truffle find "ml.g5.*" --service sagemaker --show-quota --regions us-east-1
```

Example output:

```
┌────────────────┬───────────┬───────┬──────────────┬──────────────┬──────┬─────────────┬────────────┬───────────────┬──────────┬─────────────┐
│ Instance Type  │ Region    │ vCPUs │ Memory (GiB) │ Architecture │ GPUs │ GPU Model   │ VRAM (GiB) │ Spot-Eligible │ $/hr     │ Train Quota │
├────────────────┼───────────┼───────┼──────────────┼──────────────┼──────┼─────────────┼────────────┼───────────────┼──────────┼─────────────┤
│ ml.g5.2xlarge  │ us-east-1 │ 8     │ 32.0         │ x86_64       │ 1    │ NVIDIA A10G │ 22         │ ✓             │ $1.5150  │ 1           │
│ ml.g5.24xlarge │ us-east-1 │ 96    │ 384.0        │ x86_64       │ 4    │ NVIDIA A10G │ 89         │ ✓             │ $10.1800 │ 0           │
└────────────────┴───────────┴───────┴──────────────┴──────────────┴──────┴─────────────┴────────────┴───────────────┴──────────┴─────────────┘

  🤖 SageMaker ml.* types: specs from the underlying EC2 type; $/hr is the SageMaker rate (includes the management premium)
  ✓ Spot-Eligible: usable with managed spot training — a billed-time discount of up to 90% (no separate spot price; savings depend on your job)
```

## How it works

SageMaker has **no equivalent of EC2's `DescribeInstanceTypes`** — there is no
API that enumerates the `ml.*` types offered in a region. truffle therefore uses
**Service Quotas** as the authoritative source: a region exposes one quota per
offered `ml.*` type (per job kind), so the set of quota names *is* the offered
set.

For each matched `ml.*` type, truffle:

1. **Finds the offered set** from the region's SageMaker service quotas.
2. **Enriches specs** (vCPU / memory / GPU / architecture) from the underlying
   EC2 type — it strips the `ml.` prefix (`ml.g5.2xlarge` → `g5.2xlarge`) and
   reads that type's specs from the EC2 catalog. A type with no EC2 counterpart
   still appears (its availability is real) but with blank specs.
3. **Prices** it from the SageMaker Price List offer (see below).
4. **Folds in quota data** — managed-spot eligibility and the training-job limit.

Patterns match against the `ml.`-prefixed name, so `ml.g5.*` works directly.
Natural-language queries (`nvidia a10g`, `48 vcpu`) also work — they match the
underlying EC2 specs.

## Pricing

`ml.*` types are billed under the **`AmazonSageMaker`** Price List offer, which
carries a **management premium** over the equivalent EC2 rate. For example, in
`us-east-1`:

| Type | SageMaker `$/hr` | EC2 `$/hr` |
|------|------------------|------------|
| `ml.g5.2xlarge` | ~$1.515 | ~$1.212 |

`find` shows and sorts by this rate automatically; `search` shows it with
`--show-price`. Rates are cached per type/region, like EC2 pricing. A type with
no published SageMaker rate shows `N/A`.

## Managed spot training

SageMaker **managed spot training** can cut training cost by **up to 90%**. It is
*not* a spot market with a published per-instance price — it is a **billed-time
discount**: you are billed only for `BillableTimeInSeconds` out of the job's
`TrainingTimeInSeconds`, so the savings depend on your job, not on a quoted rate.

Because there is no per-type spot price to report, truffle instead marks which
types are **eligible** for managed spot training (the `Spot-Eligible` column,
shown automatically when any result is eligible). Eligibility reflects whether
the region exposes a `spot training job usage` service quota for the type.

## Per-type quota (`--show-quota`)

`--show-quota` adds a **Train Quota** column: the account's limit for
`training job usage` of each type. A value of `0` means you must
[request a quota increase](https://console.aws.amazon.com/servicequotas/) before
you can launch that type — a common reason a SageMaker launch fails despite the
type being "available". This reuses the quota data already fetched for
discovery; it adds no extra API calls.

For a full quota view (all job kinds, not just training), use
`truffle quotas --service sagemaker`.

## Machine-readable output

JSON/YAML (`-o json` / `-o yaml`) include the SageMaker fields:

```json
{
  "instance_type": "ml.g5.2xlarge",
  "region": "us-east-1",
  "vcpus": 8,
  "memory_mib": 32768,
  "gpus": 1,
  "gpu_model": "A10G",
  "on_demand_price": 1.515,
  "service": "sagemaker",
  "managed_spot_eligible": true,
  "training_job_quota": 1
}
```

- `service` is `"sagemaker"` for these rows (absent/`"ec2"` otherwise).
- `managed_spot_eligible` — usable with managed spot training.
- `training_job_quota` — the training-job limit; omitted when the region exposes
  no such quota for the type.

## Notes & limitations

- **Credentials required.** Like all discovery commands, this needs AWS
  credentials with `servicequotas:ListServiceQuotas`, `ec2:DescribeInstanceTypes`,
  and `pricing:GetProducts`.
- **`spot` is EC2-only.** `truffle spot` covers the EC2 Spot market; SageMaker
  managed spot is a billed-time discount, not a market, so it is surfaced through
  the `Spot-Eligible` marker on `find`/`search` rather than as a spot price.
- **`SpawnSupported` is always false** for `ml.*` rows — [spawn](https://github.com/spore-host/spawn)
  launches EC2 instances, not SageMaker resources.
