package budget

import (
	"fmt"
	"math"

	"github.com/JaydenCJ/profgate/internal/diff"
)

// Verdict is one evaluated (function, limit) pair.
type Verdict struct {
	Function string // "@total" for whole-profile limits
	Metric   string // "flat", "cum", or "total"
	Key      string // "max-flat-growth", ...
	LimitRaw string // the threshold as written, e.g. "25ms" or "10%"
	RuleLine int    // budgets-file line, 0 for --budget flags
	Base     int64  // base value in profile units
	Head     int64  // head value in profile units
	Allowed  int64  // resolved threshold: max head value, or max delta for growth limits
	Growth   bool
	Breached bool
}

// Result is the outcome of evaluating all rules against a diff report.
type Result struct {
	Checked    int       // number of (function, limit) evaluations performed
	Matched    int       // number of functions matched by at least one rule
	Violations []Verdict // breached verdicts only, in deterministic order
}

// Failed reports whether any budget was breached.
func (r *Result) Failed() bool { return len(r.Violations) > 0 }

// profileClass maps a pprof unit string to the amount class it can be
// budgeted with. Unknown units budget as raw counts.
func profileClass(unit string) Class {
	switch unit {
	case "nanoseconds", "microseconds", "milliseconds", "seconds":
		return ClassTime
	case "bytes":
		return ClassBytes
	default:
		return ClassCount
	}
}

// unitScale converts a canonical amount (ns / bytes) into profile units.
func unitScale(unit string) float64 {
	switch unit {
	case "nanoseconds", "bytes":
		return 1
	case "microseconds":
		return 1e3
	case "milliseconds":
		return 1e6
	case "seconds":
		return 1e9
	default:
		return 1
	}
}

// resolve converts a limit amount into an absolute threshold in profile
// units. For percent limits, relTo is the reference value (head total for
// value limits, the base value for growth limits).
func resolve(a Amount, unit string, relTo int64) int64 {
	if a.Class == ClassPercent {
		return int64(math.Round(a.Value / 100 * float64(relTo)))
	}
	return int64(math.Round(a.Value / unitScale(unit)))
}

// checkUnits rejects budgets whose unit class contradicts the profile's,
// e.g. "max-flat=10ms" against an alloc_space (bytes) profile. Percent
// and bare-count limits are unit-agnostic and always allowed.
func checkUnits(rules []Rule, unit string) error {
	pc := profileClass(unit)
	for _, r := range rules {
		for _, lim := range r.Limits {
			c := lim.Amount.Class
			if c == ClassPercent || c == ClassCount || c == pc {
				continue
			}
			src := fmt.Sprintf("budgets line %d", r.Line)
			if r.Line == 0 {
				src = "--budget flag"
			}
			return fmt.Errorf("%s: %s=%s uses %s units but the profile measures %s (%s)",
				src, lim.Key, lim.Amount.Raw, c, profileClass(unit), unit)
		}
	}
	return nil
}

// Evaluate runs every rule against the diff report.
//
// Semantics:
//   - value limits (max-flat, max-cum, max): breach when head > threshold;
//     percent thresholds are relative to the head profile total.
//   - growth limits (max-*-growth): breach when head-base > allowance;
//     percent allowances are relative to the base value, and a function
//     that is new in head (base == 0) breaches any percent growth cap the
//     moment it has a nonzero value.
//
// Verdict order is deterministic: @total rules first (file order), then
// functions in report order with rules in file order.
func Evaluate(rules []Rule, rep *diff.Report) (*Result, error) {
	if err := checkUnits(rules, rep.SampleType.Unit); err != nil {
		return nil, err
	}
	res := &Result{}
	record := func(v Verdict) {
		res.Checked++
		if v.Breached {
			res.Violations = append(res.Violations, v)
		}
	}

	for _, r := range rules {
		if !r.Total {
			continue
		}
		for _, lim := range r.Limits {
			record(verdict("@total", lim, r.Line, rep.TotalBase, rep.TotalHead, rep, rep.SampleType.Unit))
		}
	}

	for _, e := range rep.Entries {
		matched := false
		for _, r := range rules {
			if r.Total || !Match(r.Pattern, e.Name) {
				continue
			}
			matched = true
			for _, lim := range r.Limits {
				base, head := e.FlatBase, e.FlatHead
				if lim.Metric == "cum" {
					base, head = e.CumBase, e.CumHead
				}
				record(verdict(e.Name, lim, r.Line, base, head, rep, rep.SampleType.Unit))
			}
		}
		if matched {
			res.Matched++
		}
	}
	return res, nil
}

// verdict evaluates one limit against a base/head pair.
func verdict(name string, lim Limit, line int, base, head int64, rep *diff.Report, unit string) Verdict {
	v := Verdict{
		Function: name,
		Metric:   lim.Metric,
		Key:      lim.Key,
		LimitRaw: lim.Amount.Raw,
		RuleLine: line,
		Base:     base,
		Head:     head,
		Growth:   lim.Growth,
	}
	if lim.Growth {
		v.Allowed = resolve(lim.Amount, unit, base)
		v.Breached = head-base > v.Allowed
		return v
	}
	v.Allowed = resolve(lim.Amount, unit, rep.TotalHead)
	v.Breached = head > v.Allowed
	return v
}
