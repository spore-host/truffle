# truffle

Find EC2 instance types, compare spot prices, check quotas.

Most commands work **without AWS credentials** — only `truffle quotas` and `truffle capacity` require them.

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
# Search instance types
truffle search "m7i.*"

# Compare spot prices (no credentials needed)
truffle spot c6a.xlarge c7g.xlarge --sort-by-price --active-only

# Find by natural language
truffle find "h100 8gpu efa"
truffle find graviton

# Check EC2 quotas (requires credentials)
truffle quotas --regions us-east-1 --family P

# Check SageMaker quotas
truffle quotas --service sagemaker --family g5 --regions us-west-2

# Find available capacity reservations
truffle capacity --gpu-only
```

## Commands

| Command | Description |
|---------|-------------|
| `search <pattern>` | Search instance types by pattern |
| `find [query...]` | Natural language instance search |
| `spot <pattern>` | Spot prices and availability |
| `az <pattern>` | AZ-first availability view |
| `capacity` | On-demand capacity reservations |
| `list` | List instance families or sizes |
| `quotas` | EC2 and SageMaker service quotas |
| `app list` | Browse application catalog |

## Go Library

```go
import "github.com/spore-host/truffle/pkg/aws"

client, _ := aws.NewClient(ctx)
results, _ := client.SearchInstanceTypes(ctx, regions, matcher, opts)
```

## Python Bindings

CGO bindings for Python are available in [`bindings/python/`](bindings/python/).

## Documentation

Full reference at **[spore.host/docs](https://spore.host/docs/tools/truffle)**.

## License

Apache 2.0 — Copyright 2025-2026 Scott Friedman.
