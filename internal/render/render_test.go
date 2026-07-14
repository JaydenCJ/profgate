// Rendering tests: unit formatting, delta/percent helpers, and the
// text/Markdown/JSON reports. Output shape is asserted on substrings and
// decoded JSON, not byte-golden files, so cosmetic column tweaks don't
// require regenerating fixtures.
package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/JaydenCJ/profgate/internal/aggregate"
	"github.com/JaydenCJ/profgate/internal/budget"
	"github.com/JaydenCJ/profgate/internal/diff"
	"github.com/JaydenCJ/profgate/internal/pprof"
)

func TestValueTimeScales(t *testing.T) {
	cases := map[int64]string{
		0:             "0",
		999:           "999ns",
		1_000:         "1µs",
		1_500:         "1.5µs",
		30_000_000:    "30ms",
		42_250_000:    "42.25ms",
		1_500_000_000: "1.5s",
	}
	for in, want := range cases {
		if got := Value(in, "nanoseconds"); got != want {
			t.Errorf("Value(%d, ns): got %q, want %q", in, got, want)
		}
	}
	// Profiles measured in coarser time units normalize the same way.
	if got := Value(1500, "milliseconds"); got != "1.5s" {
		t.Errorf("milliseconds: got %q, want 1.5s", got)
	}
	if got := Value(250, "microseconds"); got != "250µs" {
		t.Errorf("microseconds: got %q, want 250µs", got)
	}
}

func TestValueByteScales(t *testing.T) {
	cases := map[int64]string{
		0:               "0",
		512:             "512B",
		1024:            "1KiB",
		1536:            "1.5KiB",
		5 * 1024 * 1024: "5MiB",
		3 << 30:         "3GiB",
	}
	for in, want := range cases {
		if got := Value(in, "bytes"); got != want {
			t.Errorf("Value(%d, bytes): got %q, want %q", in, got, want)
		}
	}
}

func TestValueCountUnknownUnitsAndNegatives(t *testing.T) {
	if got := Value(123456, "count"); got != "123456" {
		t.Fatalf("count: got %q", got)
	}
	if got := Value(7, "widgets"); got != "7" {
		t.Fatalf("unknown unit: got %q", got)
	}
	if got := Value(-30_000_000, "nanoseconds"); got != "-30ms" {
		t.Fatalf("negative: got %q, want -30ms", got)
	}
}

func TestDeltaAndPctHelpers(t *testing.T) {
	if got := Delta(1024, "bytes"); got != "+1KiB" {
		t.Fatalf("positive delta: got %q", got)
	}
	if got := Delta(-1024, "bytes"); got != "-1KiB" {
		t.Fatalf("negative delta: got %q", got)
	}
	if got := Delta(0, "bytes"); got != "0" {
		t.Fatalf("zero delta: got %q", got)
	}
	if got := Pct(100, 125); got != "+25.0%" {
		t.Fatalf("pct: got %q", got)
	}
	if got := Pct(100, 75); got != "-25.0%" {
		t.Fatalf("pct: got %q", got)
	}
	if got := Pct(0, 50); got != "new" {
		t.Fatalf("zero base: got %q, want new", got)
	}
	if got := Pct(0, 0); got != "0.0%" {
		t.Fatalf("zero/zero: got %q", got)
	}
}

func TestPluralPicksTheRightNounForm(t *testing.T) {
	// Reports must read "1 breach" / "4 breaches", never "(s)" hacks.
	if got := plural(1, "breach", "breaches"); got != "1 breach" {
		t.Fatalf("singular: got %q", got)
	}
	if got := plural(4, "breach", "breaches"); got != "4 breaches" {
		t.Fatalf("plural: got %q", got)
	}
	if got := plural(0, "matched function", "matched functions"); got != "0 matched functions" {
		t.Fatalf("zero takes the plural form: got %q", got)
	}
}

// demoReport builds a small report used by the renderer tests.
func demoReport() *diff.Report {
	return &diff.Report{
		SampleType: pprof.ValueType{Type: "cpu", Unit: "nanoseconds"},
		BaseLabel:  "base.pb.gz",
		HeadLabel:  "head.pb.gz",
		TotalBase:  100e6,
		TotalHead:  134e6,
		Entries: []diff.Entry{
			{Name: "app/render.Table", FlatBase: 10e6, FlatHead: 42e6, CumBase: 10e6, CumHead: 42e6, InBase: true, InHead: true},
			{Name: "app/store.Query", FlatBase: 30e6, FlatHead: 28e6, CumBase: 30e6, CumHead: 28e6, InBase: true, InHead: true},
			{Name: "encoding/json.Marshal", FlatBase: 20e6, FlatHead: 20e6, CumBase: 20e6, CumHead: 20e6, InBase: true, InHead: true},
		},
	}
}

func TestTextDiffShowsTotalsAndRows(t *testing.T) {
	var buf bytes.Buffer
	TextDiff(&buf, demoReport(), Options{Top: 20})
	out := buf.String()
	for _, want := range []string{
		"profgate diff — cpu/nanoseconds",
		"100ms → 134ms",
		"+34ms (+34.0%)",
		"app/render.Table",
		"+32ms (+320.0%)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text diff missing %q in:\n%s", want, out)
		}
	}
}

func TestTextDiffAllAndTopFilters(t *testing.T) {
	var buf bytes.Buffer
	TextDiff(&buf, demoReport(), Options{Top: 20})
	if strings.Contains(buf.String(), "encoding/json.Marshal") {
		t.Fatal("unchanged function shown without --all")
	}
	buf.Reset()
	TextDiff(&buf, demoReport(), Options{Top: 20, All: true})
	if !strings.Contains(buf.String(), "encoding/json.Marshal") {
		t.Fatal("--all did not include the unchanged function")
	}
	buf.Reset()
	TextDiff(&buf, demoReport(), Options{Top: 1})
	out := buf.String()
	if strings.Contains(out, "app/store.Query") {
		t.Fatal("--top 1 printed a second row")
	}
	// The demo report hides two entries at --top 1, so the note must
	// use the plural form — no "(s)" hacks.
	if !strings.Contains(out, "more functions; use --top 0 --all") {
		t.Fatalf("truncation note missing or misworded:\n%s", out)
	}
}

func TestMarkdownDiffProducesTableWithBacktickedNames(t *testing.T) {
	var buf bytes.Buffer
	MarkdownDiff(&buf, demoReport(), Options{Top: 20})
	out := buf.String()
	for _, want := range []string{
		"## profgate diff",
		"`base.pb.gz` → `head.pb.gz`",
		"| Function | Flat (base) | Flat (head) | Δ Flat |",
		"| `app/render.Table` | 10ms | 42ms | +32ms (+320.0%) |",
		"<sub>Generated by [profgate]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown diff missing %q in:\n%s", want, out)
		}
	}
}

func failResult() *budget.Result {
	return &budget.Result{
		Checked: 4,
		Matched: 2,
		Violations: []budget.Verdict{{
			Function: "app/render.Table", Metric: "flat",
			Key: "max-flat-growth", LimitRaw: "50%", RuleLine: 3,
			Base: 10e6, Head: 42e6, Allowed: 5e6, Growth: true, Breached: true,
		}},
	}
}

func TestMarkdownCheckFailLeadsWithBreachTable(t *testing.T) {
	var buf bytes.Buffer
	MarkdownCheck(&buf, demoReport(), failResult(), Options{Top: 5})
	out := buf.String()
	for _, want := range []string{
		"## profgate check — ❌ FAIL",
		"### Budget breaches (1)",
		"| `app/render.Table` | flat | 10ms | 42ms | **+32ms (+320.0%)** | `max-flat-growth=50%` | budgets line 3 |",
		"### Top movers",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown check missing %q in:\n%s", want, out)
		}
	}
	if strings.Index(out, "Budget breaches") > strings.Index(out, "Top movers") {
		t.Fatal("breaches must come before movers")
	}
}

func TestMarkdownCheckPassSummarizes(t *testing.T) {
	var buf bytes.Buffer
	MarkdownCheck(&buf, demoReport(), &budget.Result{Checked: 4, Matched: 2}, Options{Top: 5})
	out := buf.String()
	if !strings.Contains(out, "✅ PASS") {
		t.Fatalf("pass header missing:\n%s", out)
	}
	if !strings.Contains(out, "4 budget checks passed across 2 matched functions.") {
		t.Fatalf("pass summary missing:\n%s", out)
	}
}

func TestTextCheckPrintsBreachesAndVerdict(t *testing.T) {
	var buf bytes.Buffer
	TextCheck(&buf, demoReport(), failResult())
	out := buf.String()
	for _, want := range []string{
		"BREACH", "app/render.Table", "max-flat-growth=50%",
		// Exactly one violation: the verdict must use the singular form.
		"allowed Δ +5ms", "budgets line 3", "check: FAIL (1 breach)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text check missing %q in:\n%s", want, out)
		}
	}
	buf.Reset()
	TextCheck(&buf, demoReport(), &budget.Result{Checked: 4, Matched: 2})
	if !strings.Contains(buf.String(), "check: PASS") {
		t.Fatalf("pass verdict missing:\n%s", buf.String())
	}
}

func TestJSONDiffDecodesWithStableEnvelope(t *testing.T) {
	var buf bytes.Buffer
	JSONDiff(&buf, demoReport())
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got["tool"] != "profgate" || got["schema_version"] != float64(1) || got["command"] != "diff" {
		t.Fatalf("envelope wrong: %v", got)
	}
	fns := got["functions"].([]any)
	if len(fns) != 3 {
		t.Fatalf("got %d functions, want 3 (JSON always carries the full list)", len(fns))
	}
	first := fns[0].(map[string]any)
	if first["name"] != "app/render.Table" || first["flat_delta"] != float64(32e6) {
		t.Fatalf("first function wrong: %v", first)
	}
}

func TestJSONCheckCarriesVerdictAndViolations(t *testing.T) {
	var buf bytes.Buffer
	JSONCheck(&buf, demoReport(), failResult())
	var got struct {
		Verdict    string `json:"verdict"`
		Checks     int    `json:"checks"`
		Violations []struct {
			Function string `json:"function"`
			Limit    string `json:"limit"`
			RuleLine int    `json:"rule_line"`
		} `json:"violations"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Verdict != "fail" || got.Checks != 4 {
		t.Fatalf("verdict/checks: %+v", got)
	}
	if len(got.Violations) != 1 || got.Violations[0].Limit != "max-flat-growth=50%" || got.Violations[0].RuleLine != 3 {
		t.Fatalf("violations: %+v", got.Violations)
	}
	// A passing result must say "pass" and keep violations as [].
	buf.Reset()
	JSONCheck(&buf, demoReport(), &budget.Result{})
	if !strings.Contains(buf.String(), `"verdict": "pass"`) || !strings.Contains(buf.String(), `"violations": []`) {
		t.Fatalf("pass JSON wrong:\n%s", buf.String())
	}
}

func TestShowRenderers(t *testing.T) {
	stats := []aggregate.Stat{
		{Name: "hot", Flat: 90e6, Cum: 90e6},
		{Name: "root", Flat: 0, Cum: 100e6},
	}
	var buf bytes.Buffer
	TextShow(&buf, "p.pb.gz", "cpu/nanoseconds", "nanoseconds", stats, 100e6, 1)
	out := buf.String()
	if !strings.Contains(out, "90ms") || !strings.Contains(out, "90.0%") {
		t.Fatalf("text show missing values:\n%s", out)
	}
	if strings.Contains(out, "root") {
		t.Fatal("--top 1 printed a second row")
	}

	buf.Reset()
	JSONShow(&buf, "p.pb.gz", "cpu", "nanoseconds", stats, 100e6)
	var got struct {
		Command   string `json:"command"`
		Total     int64  `json:"total"`
		Functions []struct {
			Name string `json:"name"`
			Flat int64  `json:"flat"`
		} `json:"functions"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Command != "show" || got.Total != 100e6 || len(got.Functions) != 2 {
		t.Fatalf("json show wrong: %+v", got)
	}
}
