// Package render turns diff reports and budget results into the three
// output formats: aligned text for terminals, GitHub-flavored Markdown
// for PR comments, and stable JSON for machines.
package render

import (
	"fmt"
	"strconv"
	"strings"
)

// Value renders v in a human scale chosen by the profile unit:
// durations for time units, IEC sizes for bytes, and plain integers for
// counts. The output is deterministic and locale-free.
func Value(v int64, unit string) string {
	neg := ""
	if v < 0 {
		neg = "-"
		v = -v
	}
	switch unit {
	case "nanoseconds", "microseconds", "milliseconds", "seconds":
		return neg + formatTime(toNanos(v, unit))
	case "bytes":
		return neg + formatBytes(v)
	default:
		return neg + strconv.FormatInt(v, 10)
	}
}

// Delta renders a signed difference ("+30ms", "-1.5MiB", "0").
func Delta(v int64, unit string) string {
	if v > 0 {
		return "+" + Value(v, unit)
	}
	return Value(v, unit)
}

// Pct renders the relative change from base to head. A value appearing
// out of nowhere reads "new" rather than a division-by-zero artifact.
func Pct(base, head int64) string {
	if base == 0 {
		if head == 0 {
			return "0.0%"
		}
		return "new"
	}
	return fmt.Sprintf("%+.1f%%", float64(head-base)/float64(base)*100)
}

// toNanos converts a value in the profile's time unit to nanoseconds.
func toNanos(v int64, unit string) int64 {
	switch unit {
	case "microseconds":
		return v * 1e3
	case "milliseconds":
		return v * 1e6
	case "seconds":
		return v * 1e9
	default:
		return v
	}
}

func formatTime(ns int64) string {
	switch {
	case ns == 0:
		return "0"
	case ns < 1e3:
		return strconv.FormatInt(ns, 10) + "ns"
	case ns < 1e6:
		return trimFloat(float64(ns)/1e3) + "µs"
	case ns < 1e9:
		return trimFloat(float64(ns)/1e6) + "ms"
	default:
		return trimFloat(float64(ns)/1e9) + "s"
	}
}

func formatBytes(b int64) string {
	switch {
	case b == 0:
		return "0"
	case b < 1024:
		return strconv.FormatInt(b, 10) + "B"
	case b < 1024*1024:
		return trimFloat(float64(b)/1024) + "KiB"
	case b < 1024*1024*1024:
		return trimFloat(float64(b)/(1024*1024)) + "MiB"
	default:
		return trimFloat(float64(b)/(1024*1024*1024)) + "GiB"
	}
}

// plural renders a count with the right noun form ("1 breach",
// "4 breaches") so reports never resort to "(s)" hacks.
func plural(n int, singular, pluralForm string) string {
	if n == 1 {
		return "1 " + singular
	}
	return strconv.Itoa(n) + " " + pluralForm
}

// trimFloat prints with two decimals, then drops trailing zeros so
// "30.00" reads "30" and "1.50" reads "1.5".
func trimFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}
