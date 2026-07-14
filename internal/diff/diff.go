// Package diff joins two aggregated profiles by function name and
// computes per-function deltas — the core data model behind
// `profgate diff` and `profgate check`.
package diff

import (
	"fmt"
	"sort"

	"github.com/JaydenCJ/profgate/internal/aggregate"
	"github.com/JaydenCJ/profgate/internal/pprof"
)

// Entry is one function's before/after numbers.
type Entry struct {
	Name     string
	FlatBase int64
	FlatHead int64
	CumBase  int64
	CumHead  int64
	InBase   bool // function appeared in the base profile
	InHead   bool // function appeared in the head profile
}

// FlatDelta is head minus base flat value.
func (e Entry) FlatDelta() int64 { return e.FlatHead - e.FlatBase }

// CumDelta is head minus base cumulative value.
func (e Entry) CumDelta() int64 { return e.CumHead - e.CumBase }

// Report is the full comparison of two profiles for one sample type.
type Report struct {
	SampleType pprof.ValueType
	BaseLabel  string // display name of the base input (usually the path)
	HeadLabel  string
	TotalBase  int64
	TotalHead  int64
	Entries    []Entry // sorted: largest |flat delta| first
}

// TotalDelta is head minus base profile total.
func (r *Report) TotalDelta() int64 { return r.TotalHead - r.TotalBase }

// Compute aggregates both profiles at the sample type named by spec and
// joins them by function name.
//
// The type is resolved on the head profile first (so "" follows head's
// default), then the same fully-qualified type/unit is required in base —
// comparing nanoseconds against bytes is always a hard error, never a
// silent garbage diff.
func Compute(base, head *pprof.Profile, spec string) (*Report, error) {
	hi, err := head.SampleTypeIndex(spec)
	if err != nil {
		return nil, fmt.Errorf("head profile: %w", err)
	}
	vt := head.SampleTypes[hi]
	bi, err := base.SampleTypeIndex(vt.String())
	if err != nil {
		return nil, fmt.Errorf("base profile: %w", err)
	}

	baseStats, baseTotal := aggregate.Functions(base, bi)
	headStats, headTotal := aggregate.Functions(head, hi)

	merged := make(map[string]*Entry, len(baseStats)+len(headStats))
	for name, s := range baseStats {
		merged[name] = &Entry{
			Name: name, FlatBase: s.Flat, CumBase: s.Cum, InBase: true,
		}
	}
	for name, s := range headStats {
		e, ok := merged[name]
		if !ok {
			e = &Entry{Name: name}
			merged[name] = e
		}
		e.FlatHead, e.CumHead, e.InHead = s.Flat, s.Cum, true
	}

	entries := make([]Entry, 0, len(merged))
	for _, e := range merged {
		entries = append(entries, *e)
	}
	sort.Slice(entries, func(i, j int) bool {
		fi, fj := abs(entries[i].FlatDelta()), abs(entries[j].FlatDelta())
		if fi != fj {
			return fi > fj
		}
		ci, cj := abs(entries[i].CumDelta()), abs(entries[j].CumDelta())
		if ci != cj {
			return ci > cj
		}
		return entries[i].Name < entries[j].Name
	})

	return &Report{
		SampleType: vt,
		TotalBase:  baseTotal,
		TotalHead:  headTotal,
		Entries:    entries,
	}, nil
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
