// Budget evaluation tests: value vs growth semantics, percent
// references, unit-mismatch rejection, new-function handling, and
// deterministic verdict ordering.
package budget

import (
	"strings"
	"testing"

	"github.com/JaydenCJ/profgate/internal/diff"
	"github.com/JaydenCJ/profgate/internal/pprof"
)

// report fabricates a diff report directly — evaluation only reads the
// aggregated numbers, so tests state them explicitly.
func report(unit string, totalBase, totalHead int64, entries ...diff.Entry) *diff.Report {
	return &diff.Report{
		SampleType: pprof.ValueType{Type: "cpu", Unit: unit},
		TotalBase:  totalBase,
		TotalHead:  totalHead,
		Entries:    entries,
	}
}

func entry(name string, flatBase, flatHead, cumBase, cumHead int64) diff.Entry {
	return diff.Entry{
		Name: name, FlatBase: flatBase, FlatHead: flatHead,
		CumBase: cumBase, CumHead: cumHead, InBase: true, InHead: true,
	}
}

func rules(t *testing.T, lines ...string) []Rule {
	t.Helper()
	rs, err := Parse(strings.NewReader(strings.Join(lines, "\n")))
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

func TestValueLimitBreachesOnlyWhenStrictlyOver(t *testing.T) {
	rep := report("nanoseconds", 100e6, 100e6,
		entry("at.Limit", 0, 25e6, 0, 25e6),
		entry("over.Limit", 0, 25e6+1, 0, 25e6+1),
	)
	res, err := Evaluate(rules(t, "*Limit max-flat=25ms"), rep)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Violations) != 1 || res.Violations[0].Function != "over.Limit" {
		t.Fatalf("violations: %+v", res.Violations)
	}
	if res.Checked != 2 || res.Matched != 2 {
		t.Fatalf("checked=%d matched=%d, want 2/2", res.Checked, res.Matched)
	}
}

func TestValueLimitPercentIsOfHeadTotal(t *testing.T) {
	// 10% of the 200ms head total = 20ms; 21ms breaches, 19ms passes.
	rep := report("nanoseconds", 100e6, 200e6,
		entry("hot", 0, 21e6, 0, 21e6),
		entry("warm", 0, 19e6, 0, 19e6),
	)
	res, err := Evaluate(rules(t, "* max-flat=10%"), rep)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Violations) != 1 || res.Violations[0].Function != "hot" {
		t.Fatalf("violations: %+v", res.Violations)
	}
	if res.Violations[0].Allowed != 20e6 {
		t.Fatalf("allowed: got %d, want 20ms", res.Violations[0].Allowed)
	}
}

func TestGrowthLimitAbsoluteDelta(t *testing.T) {
	rep := report("nanoseconds", 0, 0,
		entry("grew", 10e6, 16e6, 0, 0), // +6ms
		entry("held", 10e6, 14e6, 0, 0), // +4ms
	)
	res, err := Evaluate(rules(t, "* max-flat-growth=5ms"), rep)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Violations) != 1 || res.Violations[0].Function != "grew" {
		t.Fatalf("violations: %+v", res.Violations)
	}
}

func TestGrowthLimitPercentIsOfBaseValue(t *testing.T) {
	// 50% growth allowance on a 10ms base = +5ms allowed.
	rep := report("nanoseconds", 0, 0,
		entry("regressed", 10e6, 15e6+1, 0, 0),
		entry("withinBudget", 10e6, 15e6, 0, 0),
	)
	res, err := Evaluate(rules(t, "* max-flat-growth=50%"), rep)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Violations) != 1 || res.Violations[0].Function != "regressed" {
		t.Fatalf("violations: %+v", res.Violations)
	}
	if res.Violations[0].Allowed != 5e6 {
		t.Fatalf("allowed delta: got %d, want 5ms", res.Violations[0].Allowed)
	}
}

func TestNewFunctionBreachesPercentGrowthCap(t *testing.T) {
	// base == 0 means any nonzero head is infinite relative growth; a
	// brand-new hot function must not sneak past a percent cap.
	rep := report("nanoseconds", 0, 0,
		diff.Entry{Name: "brand.New", FlatHead: 1e6, InHead: true},
	)
	res, err := Evaluate(rules(t, "* max-flat-growth=1000%"), rep)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Violations) != 1 {
		t.Fatalf("new function passed a percent growth cap: %+v", res.Violations)
	}
}

func TestGrowthCapEdgeCases(t *testing.T) {
	// 0 → 0 passes: nothing grew.
	rep := report("nanoseconds", 0, 0, entry("idle", 0, 0, 0, 0))
	res, err := Evaluate(rules(t, "* max-flat-growth=10%"), rep)
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed() {
		t.Fatalf("0→0 breached: %+v", res.Violations)
	}
	// Improvements never breach, even under a zero-growth cap.
	rep = report("nanoseconds", 0, 0, entry("faster", 20e6, 5e6, 0, 0))
	res, err = Evaluate(rules(t, "* max-flat-growth=0%"), rep)
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed() {
		t.Fatalf("an improvement breached max-flat-growth=0%%: %+v", res.Violations)
	}
	// A zero absolute cap blocks any increase, however small.
	rep = report("nanoseconds", 0, 0, entry("crept", 10e6, 10e6+1, 0, 0))
	res, err = Evaluate(rules(t, "* max-flat-growth=0ns"), rep)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Failed() {
		t.Fatal("max-flat-growth=0ns allowed an increase")
	}
}

func TestTotalRulesGateTheWholeProfile(t *testing.T) {
	rep := report("nanoseconds", 100e6, 121e6)
	res, err := Evaluate(rules(t, "@total max-growth=20%"), rep)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Violations) != 1 || res.Violations[0].Function != "@total" {
		t.Fatalf("total verdict: %+v", res.Violations)
	}
	// And the absolute form.
	res, err = Evaluate(rules(t, "@total max=120ms"), rep)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Failed() {
		t.Fatal("121ms total passed max=120ms")
	}
}

func TestCumMetricReadsCumulativeValues(t *testing.T) {
	// Flat is stable but cumulative doubled: only max-cum-growth fires.
	rep := report("nanoseconds", 0, 0, entry("wrap", 1e6, 1e6, 30e6, 60e6))
	res, err := Evaluate(rules(t, "wrap max-flat-growth=10% max-cum-growth=50%"), rep)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Violations) != 1 || res.Violations[0].Metric != "cum" {
		t.Fatalf("violations: %+v", res.Violations)
	}
}

func TestUnitCompatibilityChecks(t *testing.T) {
	// Time budgets on a bytes profile are rejected before evaluation…
	rep := report("bytes", 0, 0, entry("f", 0, 1, 0, 1))
	_, err := Evaluate(rules(t, "* max-flat=10ms"), rep)
	if err == nil || !strings.Contains(err.Error(), "bytes") {
		t.Fatalf("want unit-mismatch error naming the profile unit, got %v", err)
	}
	// …and so are byte budgets on a time profile.
	rep2 := report("nanoseconds", 0, 0, entry("f", 0, 1, 0, 1))
	if _, err := Evaluate(rules(t, "* max-flat=4MiB"), rep2); err == nil {
		t.Fatal("byte budget accepted against a nanoseconds profile")
	}
	// Percent and bare-count budgets are unit-agnostic: 1024 > 25% of
	// 2048 (=512) and growth 1024 > 100, so both breach.
	rep3 := report("bytes", 0, 2048, entry("f", 0, 1024, 0, 1024))
	res, err := Evaluate(rules(t, "* max-flat=25% max-flat-growth=100"), rep3)
	if err != nil {
		t.Fatalf("unit-agnostic budgets rejected: %v", err)
	}
	if len(res.Violations) != 2 {
		t.Fatalf("got %d violations, want 2: %+v", len(res.Violations), res.Violations)
	}
}

func TestUnitMismatchNamesTheRuleSource(t *testing.T) {
	// File rules cite their 1-based line; inline rules (Line 0) must say
	// "--budget flag" instead of the nonsensical "budgets line 0".
	rep := report("bytes", 0, 0, entry("f", 0, 1, 0, 1))
	_, err := Evaluate(rules(t, "# comment", "* max-flat=10ms"), rep)
	if err == nil || !strings.Contains(err.Error(), "budgets line 2") {
		t.Fatalf("want error citing budgets line 2, got %v", err)
	}
	inline, perr := ParseRule("* max-flat=10ms", 0)
	if perr != nil {
		t.Fatal(perr)
	}
	_, err = Evaluate([]Rule{inline}, rep)
	if err == nil || !strings.Contains(err.Error(), "--budget flag") {
		t.Fatalf("want error citing --budget flag, got %v", err)
	}
}

func TestVerdictOrderIsDeterministic(t *testing.T) {
	rep := report("nanoseconds", 100e6, 200e6,
		entry("zeta", 1e6, 90e6, 0, 0),
		entry("alpha", 1e6, 80e6, 0, 0),
	)
	rs := rules(t,
		"@total max-growth=10%",
		"* max-flat-growth=10%",
		"* max-flat=20ms",
	)
	res, err := Evaluate(rs, rep)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range res.Violations {
		got = append(got, v.Function+":"+v.Key)
	}
	// @total first, then entries in report order, rules in file order.
	want := []string{
		"@total:max-growth",
		"zeta:max-flat-growth", "zeta:max-flat",
		"alpha:max-flat-growth", "alpha:max-flat",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("verdict order:\n got %v\nwant %v", got, want)
	}
}

func TestUnmatchedFunctionsAreNotCounted(t *testing.T) {
	rep := report("nanoseconds", 0, 0,
		entry("app.Hot", 0, 1e6, 0, 1e6),
		entry("lib.Cold", 0, 1e6, 0, 1e6),
	)
	res, err := Evaluate(rules(t, "app.* max-flat=1s"), rep)
	if err != nil {
		t.Fatal(err)
	}
	if res.Matched != 1 || res.Checked != 1 {
		t.Fatalf("matched=%d checked=%d, want 1/1", res.Matched, res.Checked)
	}
}
