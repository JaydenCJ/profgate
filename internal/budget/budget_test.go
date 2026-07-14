// Budgets-file parsing tests: amounts with units, rule lines, comments,
// line-numbered errors, and the anchored glob matcher.
package budget

import (
	"strings"
	"testing"
)

func amount(t *testing.T, s string) Amount {
	t.Helper()
	a, err := ParseAmount(s)
	if err != nil {
		t.Fatalf("ParseAmount(%q): %v", s, err)
	}
	return a
}

func TestParseAmountTimeUnits(t *testing.T) {
	cases := map[string]float64{
		"5ns":   5,
		"3us":   3_000,
		"3µs":   3_000,
		"25ms":  25e6,
		"1.5s":  1.5e9,
		"0.5ms": 5e5,
	}
	for in, wantNs := range cases {
		a := amount(t, in)
		if a.Class != ClassTime || a.Value != wantNs {
			t.Errorf("%q: got (%v, %v), want (%v ns, time)", in, a.Value, a.Class, wantNs)
		}
		if a.Raw != in {
			t.Errorf("%q: raw spelling lost: %q", in, a.Raw)
		}
	}
}

func TestParseAmountByteUnits(t *testing.T) {
	cases := map[string]float64{
		"512b": 512,
		"4kb":  4_000,    // SI: decimal
		"4KiB": 4 * 1024, // IEC: binary
		"2MB":  2e6,
		"2MiB": 2 * 1024 * 1024,
		"1gb":  1e9,
		"1GiB": 1 << 30,
	}
	for in, want := range cases {
		a := amount(t, in)
		if a.Class != ClassBytes || a.Value != want {
			t.Errorf("%q: got (%v, %v), want (%v bytes)", in, a.Value, a.Class, want)
		}
	}
}

func TestParseAmountPercentAndBareCount(t *testing.T) {
	p := amount(t, "12.5%")
	if p.Class != ClassPercent || p.Value != 12.5 {
		t.Fatalf("percent: got (%v, %v)", p.Value, p.Class)
	}
	c := amount(t, "1500")
	if c.Class != ClassCount || c.Value != 1500 {
		t.Fatalf("count: got (%v, %v)", c.Value, c.Class)
	}
}

func TestParseAmountRejectsInvalidInput(t *testing.T) {
	for _, in := range []string{"", "fast", "-5ms", "-10%", "12xx", "%", "ms"} {
		if _, err := ParseAmount(in); err == nil {
			t.Errorf("ParseAmount(%q) accepted", in)
		}
	}
}

func TestParseRuleSingleLimit(t *testing.T) {
	r, err := ParseRule("app/render.* max-flat=25ms", 3)
	if err != nil {
		t.Fatal(err)
	}
	if r.Pattern != "app/render.*" || r.Total || r.Line != 3 {
		t.Fatalf("rule head wrong: %+v", r)
	}
	if len(r.Limits) != 1 {
		t.Fatalf("got %d limits, want 1", len(r.Limits))
	}
	l := r.Limits[0]
	if l.Key != "max-flat" || l.Metric != "flat" || l.Growth || l.Amount.Value != 25e6 {
		t.Fatalf("limit wrong: %+v", l)
	}
}

func TestParseRuleMultipleLimitsKeepOrder(t *testing.T) {
	r, err := ParseRule("f max-flat=10ms max-cum-growth=15% max-flat-growth=1ms", 1)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{r.Limits[0].Key, r.Limits[1].Key, r.Limits[2].Key}
	if keys[0] != "max-flat" || keys[1] != "max-cum-growth" || keys[2] != "max-flat-growth" {
		t.Fatalf("limit order lost: %v", keys)
	}
	if !r.Limits[1].Growth || r.Limits[1].Metric != "cum" {
		t.Fatalf("max-cum-growth parsed wrong: %+v", r.Limits[1])
	}
}

func TestParseRuleTotalPattern(t *testing.T) {
	r, err := ParseRule("@total max-growth=10% max=2s", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Total || len(r.Limits) != 2 {
		t.Fatalf("total rule wrong: %+v", r)
	}
	if !r.Limits[0].Growth || r.Limits[0].Metric != "total" {
		t.Fatalf("max-growth wrong: %+v", r.Limits[0])
	}
}

func TestParseRuleRejectsInvalidLines(t *testing.T) {
	// Unknown keys carry the line number and list the valid keys.
	_, err := ParseRule("f max-speed=10ms", 7)
	if err == nil || !strings.Contains(err.Error(), "line 7") ||
		!strings.Contains(err.Error(), "max-flat-growth") {
		t.Fatalf("want line-numbered error listing valid keys, got %v", err)
	}
	// Keys are scoped: max-flat has no meaning on @total and max-growth
	// none on functions; catching that early beats a silently ignored rule.
	if _, err := ParseRule("@total max-flat=10ms", 1); err == nil {
		t.Fatal("@total accepted max-flat")
	}
	if _, err := ParseRule("f max-growth=10%", 1); err == nil {
		t.Fatal("function pattern accepted @total-only key max-growth")
	}
	// Malformed pairs and bare lines.
	for _, line := range []string{"f", "f max-flat", "f max-flat=", "f =10ms"} {
		if _, err := ParseRule(line, 1); err == nil {
			t.Errorf("ParseRule(%q) accepted", line)
		}
	}
}

func TestParseFileSkipsCommentsAndBlanks(t *testing.T) {
	src := `
# team performance budgets
@total max-growth=10%    # inline comment

app.*  max-flat=25ms
`
	rules, err := Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 {
		t.Fatalf("got %d rules, want 2", len(rules))
	}
	if rules[0].Line != 3 || rules[1].Line != 5 {
		t.Fatalf("line numbers wrong: %d, %d", rules[0].Line, rules[1].Line)
	}
}

func TestParseFileErrorsCarryLineNumber(t *testing.T) {
	src := "@total max-growth=10%\napp.* max-flat=verybad\n"
	_, err := Parse(strings.NewReader(src))
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("want line 2 error, got %v", err)
	}
}

func TestParseFileRejectsEmptyRuleSet(t *testing.T) {
	// A budgets file with no rules would make `check` pass vacuously —
	// almost certainly a misconfigured path or a fully commented file.
	if _, err := Parse(strings.NewReader("# nothing here\n\n")); err == nil {
		t.Fatal("empty budgets file accepted")
	}
}

func TestMatchGlob(t *testing.T) {
	type tc struct {
		pattern, name string
		want          bool
	}
	cases := []tc{
		{"*", "anything/at.All", true},
		{"", "", true},
		{"", "x", false},
		{"app.Render", "app.Render", true},
		{"app.Render", "app.render", false}, // case-sensitive
		{"app/render.*", "app/render.Table", true},
		{"app/render.*", "app/renderer.T", false}, // '.' is literal
		{"*.Marshal", "encoding/json.Marshal", true},
		{"*mallocgc*", "runtime.mallocgc", true},
		{"app.?un", "app.Run", true},
		{"app.?un", "app.RRun", false}, // anchored: no partial match
		{"a*b*c", "a-xx-b-yy-c", true},
		{"a*b*c", "a-xx-b-yy-d", false},
		{"**", "x", true},
		{"a*", "", false},
	}
	// Backtracking: the first '*' must be able to give characters back
	// so the later literal 'ab' can match the second occurrence.
	cases = append(cases, tc{"*ab*z", "ab-ab-z", true}, tc{"*ab*z", "ab-ab-y", false})
	for _, c := range cases {
		if got := Match(c.pattern, c.name); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}
