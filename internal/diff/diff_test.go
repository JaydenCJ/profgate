// Diff tests: joining by function name, new/removed functions, totals,
// deterministic ordering, and sample-type resolution across profiles
// whose type tables differ.
package diff

import (
	"strings"
	"testing"

	"github.com/JaydenCJ/profgate/internal/pprof"
)

func frame(name string) pprof.Frame {
	return pprof.Frame{Name: name, File: name + ".go", Line: 1}
}

// cpuProfile builds a single-type CPU profile from name→(value, parent) rows.
func cpuProfile(t *testing.T, flat map[string]int64) *pprof.Profile {
	t.Helper()
	b := pprof.NewBuilder(pprof.ValueType{Type: "cpu", Unit: "nanoseconds"})
	// Deterministic sample order: sorted names.
	names := make([]string, 0, len(flat))
	for n := range flat {
		names = append(names, n)
	}
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	for _, n := range names {
		b.AddSample([]int64{flat[n]}, frame(n), frame("root"))
	}
	p, err := pprof.Parse(b.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func find(t *testing.T, r *Report, name string) Entry {
	t.Helper()
	for _, e := range r.Entries {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("function %q not in report (%d entries)", name, len(r.Entries))
	return Entry{}
}

func TestComputeJoinsByFunctionName(t *testing.T) {
	base := cpuProfile(t, map[string]int64{"hot": 100, "steady": 50})
	head := cpuProfile(t, map[string]int64{"hot": 160, "steady": 50})
	r, err := Compute(base, head, "")
	if err != nil {
		t.Fatal(err)
	}
	e := find(t, r, "hot")
	if e.FlatBase != 100 || e.FlatHead != 160 || e.FlatDelta() != 60 {
		t.Fatalf("hot: base=%d head=%d delta=%d", e.FlatBase, e.FlatHead, e.FlatDelta())
	}
	if !e.InBase || !e.InHead {
		t.Fatal("hot should be present in both profiles")
	}
}

func TestNewFunctionIsMarkedAbsentFromBase(t *testing.T) {
	base := cpuProfile(t, map[string]int64{"old": 100})
	head := cpuProfile(t, map[string]int64{"old": 100, "shiny": 40})
	r, err := Compute(base, head, "")
	if err != nil {
		t.Fatal(err)
	}
	e := find(t, r, "shiny")
	if e.InBase || !e.InHead {
		t.Fatalf("shiny presence: InBase=%v InHead=%v", e.InBase, e.InHead)
	}
	if e.FlatBase != 0 || e.FlatDelta() != 40 {
		t.Fatalf("shiny: base=%d delta=%d, want 0/+40", e.FlatBase, e.FlatDelta())
	}
}

func TestRemovedFunctionIsMarkedAbsentFromHead(t *testing.T) {
	base := cpuProfile(t, map[string]int64{"old": 100, "gone": 30})
	head := cpuProfile(t, map[string]int64{"old": 100})
	r, err := Compute(base, head, "")
	if err != nil {
		t.Fatal(err)
	}
	e := find(t, r, "gone")
	if !e.InBase || e.InHead {
		t.Fatalf("gone presence: InBase=%v InHead=%v", e.InBase, e.InHead)
	}
	if e.FlatDelta() != -30 {
		t.Fatalf("gone delta: got %d, want -30", e.FlatDelta())
	}
}

func TestTotalsAndTotalDelta(t *testing.T) {
	base := cpuProfile(t, map[string]int64{"a": 70, "b": 30})
	head := cpuProfile(t, map[string]int64{"a": 90, "b": 30})
	r, err := Compute(base, head, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.TotalBase != 100 || r.TotalHead != 120 || r.TotalDelta() != 20 {
		t.Fatalf("totals: base=%d head=%d delta=%d", r.TotalBase, r.TotalHead, r.TotalDelta())
	}
}

func TestEntriesSortedByAbsoluteFlatDelta(t *testing.T) {
	base := cpuProfile(t, map[string]int64{"up": 10, "down": 100, "flat": 50})
	head := cpuProfile(t, map[string]int64{"up": 40, "down": 20, "flat": 50})
	r, err := Compute(base, head, "")
	if err != nil {
		t.Fatal(err)
	}
	// |−80| for down beats |+30| for up; regressions and improvements
	// rank together by magnitude.
	if r.Entries[0].Name != "down" || r.Entries[1].Name != "up" {
		t.Fatalf("order: got %s, %s", r.Entries[0].Name, r.Entries[1].Name)
	}
}

func TestSortTieBreaksByCumThenName(t *testing.T) {
	// aa and bb have identical flat deltas (0) and identical cum deltas;
	// the final tiebreak is the name, so the order is stable run-to-run.
	base := cpuProfile(t, map[string]int64{"aa": 10, "bb": 10})
	head := cpuProfile(t, map[string]int64{"aa": 10, "bb": 10})
	r, err := Compute(base, head, "")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range r.Entries {
		names = append(names, e.Name)
	}
	want := "aa,bb,root"
	if strings.Join(names, ",") != want {
		t.Fatalf("tie order: got %v, want %s", names, want)
	}
}

func TestSampleTypeResolvedOnHeadThenRequiredInBase(t *testing.T) {
	// Base orders its types (cpu, samples); head orders them (samples,
	// cpu) with cpu as default. An index-based join would silently
	// compare counts against nanoseconds; a name-based one must not.
	bb := pprof.NewBuilder(
		pprof.ValueType{Type: "cpu", Unit: "nanoseconds"},
		pprof.ValueType{Type: "samples", Unit: "count"},
	)
	bb.AddSample([]int64{1000, 1}, frame("f"))
	base, err := pprof.Parse(bb.Marshal())
	if err != nil {
		t.Fatal(err)
	}

	hb := pprof.NewBuilder(
		pprof.ValueType{Type: "samples", Unit: "count"},
		pprof.ValueType{Type: "cpu", Unit: "nanoseconds"},
	)
	hb.SetDefaultSampleType("cpu")
	hb.AddSample([]int64{2, 3000}, frame("f"))
	head, err := pprof.Parse(hb.Marshal())
	if err != nil {
		t.Fatal(err)
	}

	r, err := Compute(base, head, "")
	if err != nil {
		t.Fatal(err)
	}
	if r.SampleType.String() != "cpu/nanoseconds" {
		t.Fatalf("sample type: got %s, want cpu/nanoseconds", r.SampleType)
	}
	if r.TotalBase != 1000 || r.TotalHead != 3000 {
		t.Fatalf("cross-order totals: base=%d head=%d, want 1000/3000", r.TotalBase, r.TotalHead)
	}
}

func TestSampleTypeErrorsNameTheOffendingProfile(t *testing.T) {
	// Diffing a CPU profile against a heap profile is a user mistake
	// that must fail loudly, never produce a nonsense report — and the
	// error must say which side lacks the type.
	bb := pprof.NewBuilder(pprof.ValueType{Type: "alloc_space", Unit: "bytes"})
	bb.AddSample([]int64{64}, frame("f"))
	base, err := pprof.Parse(bb.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	head := cpuProfile(t, map[string]int64{"f": 10})
	_, err = Compute(base, head, "")
	if err == nil || !strings.Contains(err.Error(), "base profile") {
		t.Fatalf("want base-profile error, got %v", err)
	}
	// A spec absent from head is reported against head.
	cpuBase := cpuProfile(t, map[string]int64{"f": 10})
	_, err = Compute(cpuBase, head, "inuse_space")
	if err == nil || !strings.Contains(err.Error(), "head profile") {
		t.Fatalf("want head-profile error, got %v", err)
	}
}
