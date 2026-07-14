#!/usr/bin/env bash
# End-to-end smoke test for profgate: builds the binary, fabricates a
# deterministic base/head pair of pprof profiles (CPU and heap), and
# asserts on real CLI output and exit codes for every subcommand.
# No network, idempotent, finishes in seconds.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

fail() {
  echo "SMOKE FAIL: $*" >&2
  exit 1
}

BIN="$WORKDIR/profgate"

echo "1. build"
(cd "$ROOT" && go build -o "$BIN" ./cmd/profgate) || fail "go build failed"

echo "2. version matches manifest"
VER="$("$BIN" --version)"
[ "$VER" = "profgate 0.1.0" ] || fail "--version mismatch: got '$VER'"

echo "3. fabricate deterministic base/head profiles"
(cd "$ROOT" && go run ./examples/make-demo-profiles "$WORKDIR/profiles" >/dev/null) \
  || fail "make-demo-profiles failed"
BASE="$WORKDIR/profiles/base.cpu.pb.gz"
HEAD="$WORKDIR/profiles/head.cpu.pb.gz"
[ -s "$BASE" ] && [ -s "$HEAD" ] || fail "profiles missing"

echo "4. diff finds the regression"
OUT="$("$BIN" diff "$BASE" "$HEAD")"
echo "$OUT" | grep -q "profgate diff — cpu/nanoseconds" || fail "missing diff header"
echo "$OUT" | grep -q "demoapp/render.Table" || fail "regressed function missing"
echo "$OUT" | grep -q "+32ms (+320.0%)" || fail "flat delta wrong"
echo "$OUT" | grep -q "100ms → 134ms" || fail "totals wrong"

echo "5. markdown diff is PR-ready"
# Capture first: piping straight into grep -q under pipefail races —
# grep exits on the first match and the writer dies with SIGPIPE (141).
MD="$("$BIN" diff --format markdown "$BASE" "$HEAD")"
echo "$MD" | grep -q '| `demoapp/render.Table` | 10ms | 42ms |' || fail "markdown table row missing"

echo "6. JSON diff is machine-readable"
JSON="$("$BIN" diff --format json "$BASE" "$HEAD")"
echo "$JSON" | grep -q '"tool": "profgate"' || fail "json envelope missing"
echo "$JSON" | grep -q '"flat_delta": 32000000' || fail "json delta wrong"

echo "7. check passes under a generous budget (exit 0)"
"$BIN" check --budget 'demoapp/* max-flat-growth=400%' "$BASE" "$HEAD" >/dev/null \
  || fail "check should pass at 400% growth"

echo "8. check breaches the budgets file (exit 1)"
set +e
"$BIN" check --budgets "$ROOT/examples/budgets.txt" "$BASE" "$HEAD" > "$WORKDIR/check.txt"
CODE=$?
set -e
[ "$CODE" -eq 1 ] || fail "check should exit 1 on breach, got $CODE"
grep -q "BREACH" "$WORKDIR/check.txt" || fail "breach line missing"
grep -q "demoapp/render.Table" "$WORKDIR/check.txt" || fail "breaching function missing"

echo "9. markdown check verdict"
set +e
"$BIN" check --format markdown --budgets "$ROOT/examples/budgets.txt" "$BASE" "$HEAD" \
  > "$WORKDIR/check.md"
set -e
grep -q "## profgate check — ❌ FAIL" "$WORKDIR/check.md" || fail "markdown verdict missing"
grep -q "### Budget breaches" "$WORKDIR/check.md" || fail "markdown breach table missing"

echo "10. heap profiles gate on byte budgets"
set +e
"$BIN" check --budget 'demoapp/cache.* max-flat-growth=1MiB' \
  "$WORKDIR/profiles/base.heap.pb.gz" "$WORKDIR/profiles/head.heap.pb.gz" >/dev/null
CODE=$?
set -e
[ "$CODE" -eq 1 ] || fail "heap growth (+3MiB) should breach 1MiB budget, got $CODE"

echo "11. show summarizes a single profile"
SHOW="$("$BIN" show "$HEAD")"
echo "$SHOW" | grep -q "total: 134ms" || fail "show total wrong"

echo "12. usage errors exit 2"
set +e
"$BIN" diff --format yaml "$BASE" "$HEAD" >/dev/null 2>&1
[ $? -eq 2 ] || fail "bad --format should exit 2"
"$BIN" check "$BASE" "$HEAD" >/dev/null 2>&1
[ $? -eq 2 ] || fail "check without budgets should exit 2"
set -e

echo "13. corrupt profiles exit 3"
echo "not a profile" > "$WORKDIR/corrupt.pb.gz"
set +e
"$BIN" diff "$BASE" "$WORKDIR/corrupt.pb.gz" >/dev/null 2>&1
[ $? -eq 3 ] || fail "corrupt profile should exit 3"
set -e

echo "SMOKE OK"
