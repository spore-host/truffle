# truffle

[![CI](https://github.com/spore-host/truffle/actions/workflows/ci.yml/badge.svg)](https://github.com/spore-host/truffle/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/spore-host/truffle)](https://goreportcard.com/report/github.com/spore-host/truffle)
[![codecov](https://codecov.io/gh/spore-host/truffle/branch/main/graph/badge.svg)](https://codecov.io/gh/spore-host/truffle)
[![Go Reference](https://pkg.go.dev/badge/github.com/spore-host/truffle.svg)](https://pkg.go.dev/github.com/spore-host/truffle)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

Find EC2 instance types, compare spot prices, check quotas.

All discovery commands require **AWS credentials** (configured via `aws configure`, environment variables, or IAM role). Only `truffle app list` and `truffle version` work without credentials.

## Installation

**macOS / Linux (Homebrew)**
```bash
brew install spore-host/tap/truffle
```

**Windows (Scoop)**
```powershell
scoop bucket add spore-host https://github.com/spore-host/scoop-bucket
scoop install truffle
```

**Debian / Ubuntu**
```bash
curl -LO https://github.com/spore-host/truffle/releases/latest/download/truffle_linux_amd64.deb
sudo dpkg -i truffle_linux_amd64.deb
```

**RHEL / Fedora**
```bash
sudo rpm -i https://github.com/spore-host/truffle/releases/latest/download/truffle_linux_amd64.rpm
```

**Direct download** — pre-built binaries for Linux, macOS, and Windows (amd64/arm64) on the [releases page](https://github.com/spore-host/truffle/releases/latest).

**Build from source**
```bash
git clone https://github.com/spore-host/truffle
cd truffle && make build && sudo make install
```

## Quick Start

```bash
# Search instance types (glob or regex)
truffle search "m7i*"                         # glob: all m7i sizes
truffle search "c[6-8]i\.large"               # regex: c6i/c7i/c8i large

# Compare spot prices
truffle spot m7i.large --regions us-east-1 --show-savings
truffle spot "c6a*" --sort-by-price --lookback-hours 24

# Find by natural language
truffle find "h200 8gpu efa"
truffle find "amd turin 32 cores 64gb" --exact
truffle find graviton --regions us-east-1

# Check EC2 quotas
truffle quotas --regions us-east-1 --family P
truffle quotas --regions us-east-1 -o json

# Check SageMaker quotas
truffle quotas --service sagemaker --family g5 --regions us-west-2

# Find available capacity reservations
truffle capacity --gpu-only

# Output formats (all commands)
truffle search "m7i*" -o json
truffle find "intel 8 vcpu" -o csv
```

## Commands

| Command | Description |
|---------|-------------|
| `search <pattern>` | Search by glob (`m7i*`) or regex (`c[6-8]i\.large`) |
| `find <query>` | Natural language search (`amd epyc 32 cores 64gb`) |
| `spot <pattern>` | Spot pricing with savings vs on-demand |
| `az <pattern>` | AZ-first availability view |
| `capacity` | On-demand capacity reservations (ODCRs) |
| `list` | List instance families or sizes |
| `quotas` | EC2 and SageMaker service quotas |
| `app list` | Browse application catalog (no credentials needed) |
| `version` | Show version, build info, and project URL |

### Global Flags

| Flag | Description |
|------|-------------|
| `-o, --output` | Output format: `table`, `json`, `yaml`, `csv` |
| `-r, --regions` | Filter by regions (comma-separated) |
| `--no-emoji` | Disable emoji in output |
| `--no-color` | Disable colorized output |
| `--lang` | Language: `en`, `es`, `pt`, `ja`, `de` |

## Go Library

```go
import "github.com/spore-host/truffle/pkg/aws"

client, _ := aws.NewClient(ctx)
results, _ := client.SearchInstanceTypes(ctx, regions, matcher, opts)

// On-demand rate for one type (live AWS Price List, cached)
rate, _ := client.HourlyRate(ctx, "c6i.4xlarge", "us-east-1", "on-demand")

// Spot prices with on-demand comparison populated
prices, _ := client.GetSpotPricing(ctx, results, aws.SpotOptions{ShowSavings: true})
```

### Testing with awsmock

The `aws.Finder` interface allows downstream projects to mock truffle's discovery layer:

```go
import (
    "github.com/spore-host/truffle/pkg/aws"
    "github.com/spore-host/truffle/pkg/aws/awsmock"
)

mock := awsmock.New(
    awsmock.WithRegions([]string{"us-east-1"}),
    awsmock.WithInstances([]aws.InstanceTypeResult{
        {InstanceType: "m7i.large", Region: "us-east-1", VCPUs: 2, MemoryMiB: 8192},
    }),
    awsmock.WithOnDemandPrices(map[string]float64{"m7i.large/us-east-1": 0.1008}),
)
// Use mock anywhere an aws.Finder is accepted.
```

## Python Bindings

CGO bindings for Python are available in [`bindings/python/`](bindings/python/).

## Documentation

Full reference at **[spore.host/docs](https://spore.host/docs/tools/truffle)**.

## License

Apache 2.0 — Copyright 2025-2026 Scott Friedman.
