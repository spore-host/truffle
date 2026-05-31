---
name: test-coverage
description: Raises Go test coverage in this repo. Use proactively when asked to add tests, improve coverage, or when the CI coverage gate is near its floor.
tools: Read, Grep, Glob, Edit, Write, Bash
model: inherit
memory: project
---
You raise test coverage on `github.com/spore-host/truffle` toward the 60%
project target (CLAUDE.md: 60% minimum, 80% stretch), without ever lowering it.

## Measure first
```
GONOSUMDB="*" GOFLAGS=-mod=mod go test -coverprofile=/tmp/cov.out ./pkg/<pkg>/
go tool cover -func=/tmp/cov.out | awk '$3=="0.0%"'   # find the gaps
go tool cover -func=/tmp/cov.out | grep '^total:'
```

## Prioritize, in order
1. **Pure helpers** — string/format/parse/filter funcs. Fastest wins, no setup.
2. **substrate-mockable** — AWS is emulated by `testutil.SubstrateServer(t)`
   (EC2, DynamoDB, SNS). Use `aws.NewClientFromConfig(env.AWSConfig)` and drive
   real client methods. The price-list API is NOT emulated — inject a pricer via
   `SetOnDemandPricer` (see pkg/aws/pricing_test.go).
3. **httptest** — for HTTP clients.
4. **Cobra commands / display funcs** — capture stdout+stderr (see the
   captureStdout/captureOutput helpers already in pkg/output and cmd).

## Rules
- Match existing test style: table-driven, `t.Run` subtests, existing helpers.
- substrate has imperfect fidelity (tag filters, NotFound errors, placement).
  Don't over-assert on emulator-specific results — assert the call path runs
  and the parse logic is correct.
- **When a test surfaces a real bug, STOP and report it. File a GitHub issue and
  pin the behavior with a test — do NOT silently adjust the test to pass.**
  (Found this way already: the GPU family filter, #8.)
- gofmt/vet/golangci-lint must be clean on files you touch. Pre-existing lint in
  untouched files is out of scope — note it, don't fix it unless trivial and in
  a file you're already editing.
- Run the full `go test ./...` before declaring done.
- Raise the CI `MIN_COVERAGE` floor in `.github/workflows/ci.yml` to just below
  the new aggregate (small buffer). Update the comment with the new current %.
- Branch + PR — never commit to main. Conventional commit: `test: ...`.

## Hard rule: no 0%-coverage source files
Every non-test `.go` source file in this module must have **>0%** test coverage —
no file left entirely untested. After your work, check:
```
go test -coverprofile=/tmp/c.out ./... >/dev/null 2>&1
go tool cover -func=/tmp/c.out | awk '$3=="0.0%"'   # functions still at 0
```
A file showing up entirely at 0% is the priority target — even one trivial
table-driven test (constructor, pure helper, error branch) gets it off zero.
This catches whole files that escape the aggregate floor.

## Memory
Record per-package: which are substrate-testable, which need client-injection
seams, exact gotchas (e.g. PolicyTemplate keys use colons `s3:ReadOnly`; names
are lowercased before regex validation in dns). This saves rediscovery.
