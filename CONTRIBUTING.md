# Contributing to profgate

Issues, discussions and pull requests are all welcome.

## Getting started

You need Go ≥1.22; nothing else — no protobuf toolchain, no C compiler.

```bash
git clone https://github.com/JaydenCJ/profgate && cd profgate
go build ./...
go test ./...
bash scripts/smoke.sh
```

`scripts/smoke.sh` builds the binary, fabricates deterministic base/head
pprof profiles (CPU and heap) in a temp dir, and asserts on real CLI
output and exit codes for every subcommand; it must finish by printing
`SMOKE OK`.

## Before you open a pull request

1. `gofmt -l .` reports nothing (formatting is enforced).
2. `go vet ./...` passes with no findings.
3. `go test ./...` passes (90 deterministic tests, no network).
4. `bash scripts/smoke.sh` prints `SMOKE OK`.
5. Add tests for behavior changes; keep logic in pure, unit-testable
   modules (parsing, aggregation, diffing and budgeting never touch the
   filesystem — only the CLI layer does).

## Ground rules

- Keep dependencies at zero. The in-tree protobuf wire decoder exists
  precisely so profgate has no runtime dependencies; adding one needs
  strong justification in the PR.
- No network calls, ever — profgate only reads the profile files it is
  given. No telemetry.
- Determinism first: identical inputs must produce byte-identical
  reports, including all orderings. Fabricate test profiles with
  `pprof.Builder` rather than sampling real CPU time, which is flaky.
- The JSON output is a contract: shape changes bump `schema_version`.
- Code comments and doc comments are written in English.

## Reporting bugs

Include the output of `profgate version`, the full command you ran, and
— for parsing or attribution problems — the profile itself if you can
share it, or the output of `go tool pprof -raw <profile>` for the
affected function, since that is exactly what profgate's decoder sees.

## Security

Please do not open public issues for security problems; use GitHub's
private vulnerability reporting on this repository instead.
