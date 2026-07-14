# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-07-13

### Added

- Standard-library pprof reader: a minimal protobuf wire decoder for
  profile.proto (varint + length-delimited fields, packed and unpacked
  repeated integers, unknown fields skipped), transparent gzip handling,
  string-table validation, and clear errors on corrupt input.
- Deterministic profile writer (`pprof.Builder`) used by tests, examples
  and the smoke script to fabricate byte-stable profiles without flaky
  wall-clock sampling.
- pprof-faithful aggregation: per-function flat and cumulative totals
  with leaf-first location semantics, inline frames, once-per-sample
  recursion counting, and stable placeholders for unsymbolized frames.
- `diff` subcommand joining two profiles by function name for any shared
  sample type (`--sample-type cpu`, `alloc_space`, `type/unit`, …), with
  new/removed function tracking and largest-|Δ flat|-first ordering.
- Budgets engine: line-based rules with anchored globs, `@total`
  whole-profile limits, absolute and growth thresholds in durations,
  byte sizes, percentages, or raw counts, unit-compatibility checking,
  and strict deterministic verdict ordering.
- `check` subcommand enforcing budgets from a file (`--budgets`) and/or
  inline flags (`--budget`), exit code 1 on breach, 2 on configuration
  errors, 3 on unreadable profiles.
- Three output formats for every report: aligned text, PR-ready GitHub
  Markdown (verdict header, breach table, top movers), and stable JSON
  (`schema_version: 1`).
- `show` subcommand summarizing a single profile's top functions.
- Runnable examples (`examples/make-demo-profiles`, `examples/ci-gate.sh`,
  an annotated `examples/budgets.txt`) and a budgets-file reference
  (`docs/budgets.md`).
- 90 deterministic offline tests (unit + in-process CLI integration
  against fabricated profiles) and `scripts/smoke.sh`.

[0.1.0]: https://github.com/JaydenCJ/profgate/releases/tag/v0.1.0
