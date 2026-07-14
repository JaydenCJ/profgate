// Package budget parses and evaluates performance budgets: the rules
// that turn a profile diff into a pass/fail CI verdict.
//
// A budgets file is line-based. Each non-comment line is a function
// pattern followed by one or more limits:
//
//	# comments and blank lines are ignored
//	@total                 max-growth=10%
//	demoapp/render.*       max-flat=25ms  max-flat-growth=50%
//	encoding/json.*        max-cum=15%
//	*                      max-flat-growth=200%
//
// Patterns are anchored globs over the full function name (`*` matches
// any run of characters, `?` exactly one). The special pattern `@total`
// budgets the whole profile.
package budget

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Class categorizes the unit of a parsed amount.
type Class int

const (
	// ClassCount is a bare number, interpreted in the profile's own unit.
	ClassCount Class = iota
	// ClassTime is a duration, canonicalized to nanoseconds.
	ClassTime
	// ClassBytes is a byte size.
	ClassBytes
	// ClassPercent is relative: to the head total for value limits, to
	// the base value for growth limits.
	ClassPercent
)

// String names the class for error messages.
func (c Class) String() string {
	switch c {
	case ClassTime:
		return "time"
	case ClassBytes:
		return "bytes"
	case ClassPercent:
		return "percent"
	default:
		return "count"
	}
}

// Amount is one parsed threshold value.
type Amount struct {
	Value float64 // canonical: ns for time, bytes for bytes, percent points for percent
	Class Class
	Raw   string // original spelling, kept for reports
}

// timeSuffixes maps duration suffixes to nanoseconds. Longest-match wins,
// so "ms" is tried before "s".
var timeSuffixes = []struct {
	suffix string
	ns     float64
}{
	{"ns", 1},
	{"us", 1e3},
	{"µs", 1e3},
	{"ms", 1e6},
	{"s", 1e9},
}

// byteSuffixes maps size suffixes to bytes. SI prefixes are decimal
// (kB = 1000), IEC prefixes are binary (KiB = 1024).
var byteSuffixes = []struct {
	suffix string
	bytes  float64
}{
	{"kib", 1024},
	{"mib", 1024 * 1024},
	{"gib", 1024 * 1024 * 1024},
	{"kb", 1e3},
	{"mb", 1e6},
	{"gb", 1e9},
	{"b", 1},
}

// ParseAmount parses a threshold like "25ms", "4MiB", "10%", or "1500".
func ParseAmount(s string) (Amount, error) {
	orig := s
	lower := strings.ToLower(strings.TrimSpace(s))
	if lower == "" {
		return Amount{}, fmt.Errorf("empty amount")
	}
	parse := func(num string, mult float64, class Class) (Amount, error) {
		v, err := strconv.ParseFloat(num, 64)
		if err != nil {
			return Amount{}, fmt.Errorf("invalid amount %q", orig)
		}
		if v < 0 {
			return Amount{}, fmt.Errorf("amount %q must not be negative", orig)
		}
		return Amount{Value: v * mult, Class: class, Raw: orig}, nil
	}
	if strings.HasSuffix(lower, "%") {
		return parse(strings.TrimSuffix(lower, "%"), 1, ClassPercent)
	}
	for _, t := range timeSuffixes {
		if strings.HasSuffix(lower, t.suffix) {
			num := strings.TrimSuffix(lower, t.suffix)
			// A bare "s" suffix must not swallow byte sizes ("40 bits"
			// isn't a thing here, but "4gbs" typos are) — require the
			// remainder to be numeric, which parse() enforces anyway.
			return parse(num, t.ns, ClassTime)
		}
	}
	for _, b := range byteSuffixes {
		if strings.HasSuffix(lower, b.suffix) {
			return parse(strings.TrimSuffix(lower, b.suffix), b.bytes, ClassBytes)
		}
	}
	return parse(lower, 1, ClassCount)
}

// Limit is one parsed constraint from a rule line.
type Limit struct {
	Key    string // as written: "max-flat", "max-cum-growth", ...
	Metric string // "flat", "cum", or "total"
	Growth bool   // limits the head-minus-base delta instead of the head value
	Amount Amount
}

// Rule is one budgets-file line: a pattern plus its limits.
type Rule struct {
	Pattern string
	Total   bool // pattern was @total
	Limits  []Limit
	Line    int // 1-based line number in the source, 0 for --budget flags
}

// functionKeys are the limit keys valid on a function pattern.
var functionKeys = map[string]Limit{
	"max-flat":        {Metric: "flat"},
	"max-cum":         {Metric: "cum"},
	"max-flat-growth": {Metric: "flat", Growth: true},
	"max-cum-growth":  {Metric: "cum", Growth: true},
}

// totalKeys are the limit keys valid on the @total pattern.
var totalKeys = map[string]Limit{
	"max":        {Metric: "total"},
	"max-growth": {Metric: "total", Growth: true},
}

// ParseRule parses one rule line ("pattern key=value ...").
func ParseRule(line string, lineno int) (Rule, error) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return Rule{}, fmt.Errorf("line %d: want \"<pattern> key=value ...\", got %q", lineno, strings.TrimSpace(line))
	}
	r := Rule{Pattern: fields[0], Total: fields[0] == "@total", Line: lineno}
	keys := functionKeys
	if r.Total {
		keys = totalKeys
	}
	for _, f := range fields[1:] {
		key, val, ok := strings.Cut(f, "=")
		if !ok {
			return Rule{}, fmt.Errorf("line %d: %q is not key=value", lineno, f)
		}
		tmpl, ok := keys[key]
		if !ok {
			return Rule{}, fmt.Errorf("line %d: unknown limit %q for pattern %q (valid: %s)", lineno, key, r.Pattern, validKeys(keys))
		}
		amt, err := ParseAmount(val)
		if err != nil {
			return Rule{}, fmt.Errorf("line %d: %s: %w", lineno, key, err)
		}
		lim := tmpl
		lim.Key = key
		lim.Amount = amt
		r.Limits = append(r.Limits, lim)
	}
	return r, nil
}

func validKeys(keys map[string]Limit) string {
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	// Deterministic error text.
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	return strings.Join(names, ", ")
}

// Parse reads a whole budgets file.
func Parse(r io.Reader) ([]Rule, error) {
	var rules []Rule
	sc := bufio.NewScanner(r)
	lineno := 0
	for sc.Scan() {
		lineno++
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		rule, err := ParseRule(line, lineno)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(rules) == 0 {
		return nil, fmt.Errorf("budgets file contains no rules")
	}
	return rules, nil
}

// Match reports whether an anchored glob pattern matches a function name.
// `*` matches any (possibly empty) run of characters, `?` any single
// character; everything else is literal. Iterative with backtracking, so
// pathological patterns cannot blow the stack.
func Match(pattern, name string) bool {
	p, n := 0, 0
	star, mark := -1, 0
	for n < len(name) {
		switch {
		case p < len(pattern) && (pattern[p] == '?' || pattern[p] == name[n]):
			p++
			n++
		case p < len(pattern) && pattern[p] == '*':
			star, mark = p, n
			p++
		case star >= 0:
			p = star + 1
			mark++
			n = mark
		default:
			return false
		}
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}
