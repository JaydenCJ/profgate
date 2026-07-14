package render

import (
	"fmt"
	"io"

	"github.com/JaydenCJ/profgate/internal/aggregate"
	"github.com/JaydenCJ/profgate/internal/budget"
	"github.com/JaydenCJ/profgate/internal/diff"
)

// Options controls how much of a report is shown.
type Options struct {
	Top int  // maximum entries to print; <= 0 means no limit
	All bool // include entries whose flat and cum deltas are both zero
}

// visible applies the All/Top filters to a report's entries.
func visible(r *diff.Report, o Options) []diff.Entry {
	out := make([]diff.Entry, 0, len(r.Entries))
	for _, e := range r.Entries {
		if !o.All && e.FlatDelta() == 0 && e.CumDelta() == 0 {
			continue
		}
		out = append(out, e)
	}
	if o.Top > 0 && len(out) > o.Top {
		out = out[:o.Top]
	}
	return out
}

// deltaCell renders "Δ (pct)" for one before/after pair.
func deltaCell(base, head int64, unit string) string {
	return fmt.Sprintf("%s (%s)", Delta(head-base, unit), Pct(base, head))
}

// rangeCell renders "base → head".
func rangeCell(base, head int64, unit string) string {
	return fmt.Sprintf("%s → %s", Value(base, unit), Value(head, unit))
}

// TextDiff writes the terminal diff report.
func TextDiff(w io.Writer, r *diff.Report, o Options) {
	u := r.SampleType.Unit
	fmt.Fprintf(w, "profgate diff — %s\n", r.SampleType)
	fmt.Fprintf(w, "base: %s   head: %s\n", r.BaseLabel, r.HeadLabel)
	fmt.Fprintf(w, "total: %s   Δ %s\n\n",
		rangeCell(r.TotalBase, r.TotalHead, u), deltaCell(r.TotalBase, r.TotalHead, u))

	rows := visible(r, o)
	if len(rows) == 0 {
		fmt.Fprintln(w, "no per-function changes")
		return
	}
	fmt.Fprintf(w, "%-20s %-20s %-20s %-20s %s\n",
		"Δ FLAT", "FLAT (BASE→HEAD)", "Δ CUM", "CUM (BASE→HEAD)", "FUNCTION")
	for _, e := range rows {
		fmt.Fprintf(w, "%-20s %-20s %-20s %-20s %s\n",
			deltaCell(e.FlatBase, e.FlatHead, u),
			rangeCell(e.FlatBase, e.FlatHead, u),
			deltaCell(e.CumBase, e.CumHead, u),
			rangeCell(e.CumBase, e.CumHead, u),
			e.Name)
	}
	if hidden := len(r.Entries) - len(rows); hidden > 0 {
		fmt.Fprintf(w, "… %s; use --top 0 --all to list every one\n",
			plural(hidden, "more function", "more functions"))
	}
}

// TextCheck writes the terminal check report and verdict.
func TextCheck(w io.Writer, r *diff.Report, res *budget.Result) {
	u := r.SampleType.Unit
	fmt.Fprintf(w, "profgate check — %s\n", r.SampleType)
	fmt.Fprintf(w, "total: %s   Δ %s\n",
		rangeCell(r.TotalBase, r.TotalHead, u), deltaCell(r.TotalBase, r.TotalHead, u))
	fmt.Fprintf(w, "budget checks: %d   functions matched: %d\n\n", res.Checked, res.Matched)

	for _, v := range res.Violations {
		fmt.Fprintf(w, "BREACH  %-32s %-6s %s  exceeds %s=%s (%s)\n",
			v.Function, v.Metric,
			deltaLine(v, u), v.Key, v.LimitRaw, ruleRef(v.RuleLine))
	}
	if res.Failed() {
		fmt.Fprintf(w, "\ncheck: FAIL (%s)\n", plural(len(res.Violations), "breach", "breaches"))
	} else {
		fmt.Fprintf(w, "check: PASS\n")
	}
}

// deltaLine renders the measured side of a verdict: what the value was,
// what it became, and what the budget allowed.
func deltaLine(v budget.Verdict, unit string) string {
	if v.Growth {
		return fmt.Sprintf("%s, allowed Δ %s", rangeCell(v.Base, v.Head, unit), Delta(v.Allowed, unit))
	}
	return fmt.Sprintf("%s, allowed %s", rangeCell(v.Base, v.Head, unit), Value(v.Allowed, unit))
}

func ruleRef(line int) string {
	if line == 0 {
		return "--budget flag"
	}
	return fmt.Sprintf("budgets line %d", line)
}

// TextShow writes the single-profile top table.
func TextShow(w io.Writer, label string, vt string, unit string, stats []aggregate.Stat, total int64, top int) {
	fmt.Fprintf(w, "profgate show — %s\n", vt)
	fmt.Fprintf(w, "profile: %s   total: %s\n\n", label, Value(total, unit))
	if top > 0 && len(stats) > top {
		stats = stats[:top]
	}
	fmt.Fprintf(w, "%-12s %-8s %-12s %-8s %s\n", "FLAT", "FLAT%", "CUM", "CUM%", "FUNCTION")
	for _, s := range stats {
		fmt.Fprintf(w, "%-12s %-8s %-12s %-8s %s\n",
			Value(s.Flat, unit), share(s.Flat, total),
			Value(s.Cum, unit), share(s.Cum, total), s.Name)
	}
}

func share(v, total int64) string {
	if total == 0 {
		return "0.0%"
	}
	return fmt.Sprintf("%.1f%%", float64(v)/float64(total)*100)
}
