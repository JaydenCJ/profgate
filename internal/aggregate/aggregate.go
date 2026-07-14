// Package aggregate folds pprof samples into per-function flat and
// cumulative totals — the same numbers `go tool pprof -top` prints, but
// as a plain data structure the diff and budget layers can reason about.
package aggregate

import (
	"sort"

	"github.com/JaydenCJ/profgate/internal/pprof"
)

// Stat is one function's totals for a single sample-type index.
//
// Flat is the value attributed to samples where the function is the leaf
// frame (it was on-CPU / doing the allocation itself). Cum is the value
// of every sample where the function appears anywhere in the stack.
type Stat struct {
	Name string
	Flat int64
	Cum  int64
}

// Functions aggregates the profile's samples at value index vi and
// returns per-function stats plus the profile total.
//
// Semantics match pprof: the leaf is location_id[0], the innermost
// inlined frame is line[0], recursive frames are counted once per sample
// for Cum, and unsymbolized locations aggregate under a stable
// "[0xADDR]" placeholder.
func Functions(p *pprof.Profile, vi int) (map[string]*Stat, int64) {
	stats := make(map[string]*Stat)
	get := func(name string) *Stat {
		s, ok := stats[name]
		if !ok {
			s = &Stat{Name: name}
			stats[name] = s
		}
		return s
	}

	var total int64
	for _, sample := range p.Samples {
		if vi < 0 || vi >= len(sample.Values) {
			continue
		}
		v := sample.Values[vi]
		total += v

		// Cum: each distinct function once per sample, even if it appears
		// in several frames (recursion or repeated inlining).
		seen := make(map[string]bool)
		for i, locID := range sample.LocationIDs {
			names := frameNames(p, locID)
			for j, name := range names {
				if !seen[name] {
					seen[name] = true
					get(name).Cum += v
				}
				// Flat: the innermost frame of the leaf location only.
				if i == 0 && j == 0 {
					get(name).Flat += v
				}
			}
		}
	}
	return stats, total
}

// frameNames returns the function names at one location, innermost
// (inlined leaf) first. A location with no line info yields a single
// address placeholder so stripped binaries still aggregate somewhere.
func frameNames(p *pprof.Profile, locID uint64) []string {
	loc, ok := p.Locations[locID]
	if !ok {
		return []string{"[unknown]"}
	}
	if len(loc.Lines) == 0 {
		return []string{p.FunctionName(locID)}
	}
	names := make([]string, 0, len(loc.Lines))
	for _, ln := range loc.Lines {
		if fn, ok := p.Functions[ln.FunctionID]; ok && fn.Name != "" {
			names = append(names, fn.Name)
		} else {
			names = append(names, p.FunctionName(locID))
		}
	}
	return names
}

// Sorted returns the stats ordered by Flat descending, ties broken by
// Cum descending then name ascending — a fully deterministic order.
func Sorted(stats map[string]*Stat) []Stat {
	out := make([]Stat, 0, len(stats))
	for _, s := range stats {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Flat != out[j].Flat {
			return out[i].Flat > out[j].Flat
		}
		if out[i].Cum != out[j].Cum {
			return out[i].Cum > out[j].Cum
		}
		return out[i].Name < out[j].Name
	})
	return out
}
