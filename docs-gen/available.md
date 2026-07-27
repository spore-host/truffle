## `truffle available`

Report whether you can actually GET an instance type — as opposed to whether it
exists and what it costs, which 'truffle find' already answers.

For scarce accelerator types the price is often not the deciding number: a type can
be listed, priced, and completely unobtainable. This gathers the four signals AWS
exposes and reports each one separately, because they fail independently and call
for different remedies:

  Spot placement    a 1-10 likelihood score per AZ (relative, not a guarantee)
  Offered AZs       where the type is offered at all, with the region's AZ count
  Quota headroom    vCPUs left under your On-Demand quota, and the quota to raise
  Capacity blocks   purchasable Capacity Block for ML offerings (24h window)

Each signal is best-effort: one being unavailable (commonly GetSpotPlacementScores,
which SCPs often deny) is reported as a warning, not a failure — an absent signal is
never presented as a good one.

Examples:
  truffle available p6-b200.48xlarge --region us-east-1
  truffle available p5.48xlarge --region us-west-2 --output json

```
truffle available <instance-type> [flags]
```

**Flags:**

| Flag | Short | Type | Default | Description |
|------|-------|------|---------|-------------|
| `--timeout` |  | duration | `5m0s` | Timeout for AWS API calls |

