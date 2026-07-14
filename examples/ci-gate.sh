#!/usr/bin/env bash
# Example CI gate: compare a stored baseline profile against the profile
# captured on this build, enforce the team budgets, and emit a Markdown
# summary you can post as a PR comment or write to a job summary file.
#
# Runnable offline as-is — it fabricates demo profiles first. In a real
# pipeline, replace the two paths with your captured pprof files, e.g.
# the artifact from `go test -cpuprofile` or a /debug/pprof/profile
# snapshot fetched from a staging host on 127.0.0.1.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

# 0. Build the gate binary and demo inputs (stand-ins for your artifacts).
go build -o "$WORKDIR/profgate" "$ROOT/cmd/profgate"
go run "$ROOT/examples/make-demo-profiles" "$WORKDIR/profiles" >/dev/null
BASE="$WORKDIR/profiles/base.cpu.pb.gz"   # e.g. downloaded main-branch baseline
HEAD="$WORKDIR/profiles/head.cpu.pb.gz"   # e.g. profile captured on this PR

# 1. Enforce the budgets; keep the Markdown regardless of the verdict.
set +e
"$WORKDIR/profgate" check \
  --format markdown \
  --budgets "$ROOT/examples/budgets.txt" \
  "$BASE" "$HEAD" > "$WORKDIR/summary.md"
CODE=$?
set -e

# 2. Publish the summary (here: stdout; in CI: a PR comment or summary file).
cat "$WORKDIR/summary.md"

# 3. Propagate the verdict: 1 means a budget was breached — fail the job.
echo "profgate exit code: $CODE"
exit "$CODE"
