// Aggregation semantics tests: flat vs cum attribution, recursion,
// inlining, unsymbolized frames, and deterministic ordering. These
// encode the pprof conventions profgate promises to match.
package aggregate

import (
	"reflect"
	"testing"

	"github.com/JaydenCJ/profgate/internal/pprof"
)

func frame(name string) pprof.Frame {
	return pprof.Frame{Name: name, File: name + ".go", Line: 1}
}

// build parses a synthetic single-type profile from (value, stack) pairs.
func build(t *testing.T, samples ...struct {
	v     int64
	stack []pprof.Frame
}) *pprof.Profile {
	t.Helper()
	b := pprof.NewBuilder(pprof.ValueType{Type: "cpu", Unit: "nanoseconds"})
	for _, s := range samples {
		b.AddSample([]int64{s.v}, s.stack...)
	}
	p, err := pprof.Parse(b.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

type sample = struct {
	v     int64
	stack []pprof.Frame
}

func TestFlatAttributedToLeafOnly(t *testing.T) {
	p := build(t, sample{100, []pprof.Frame{frame("leaf"), frame("mid"), frame("root")}})
	stats, total := Functions(p, 0)
	if total != 100 {
		t.Fatalf("total: got %d, want 100", total)
	}
	if stats["leaf"].Flat != 100 || stats["mid"].Flat != 0 || stats["root"].Flat != 0 {
		t.Fatalf("flat misattributed: leaf=%d mid=%d root=%d",
			stats["leaf"].Flat, stats["mid"].Flat, stats["root"].Flat)
	}
}

func TestCumCountsEveryFrameInStack(t *testing.T) {
	p := build(t, sample{100, []pprof.Frame{frame("leaf"), frame("mid"), frame("root")}})
	stats, _ := Functions(p, 0)
	for _, name := range []string{"leaf", "mid", "root"} {
		if stats[name].Cum != 100 {
			t.Errorf("%s cum: got %d, want 100", name, stats[name].Cum)
		}
	}
}

func TestRecursiveFunctionCountedOncePerSample(t *testing.T) {
	// f appears three times in one stack; naive summing would give
	// cum=300 and make recursive code look three times hotter.
	f := frame("f")
	p := build(t, sample{100, []pprof.Frame{f, f, f, frame("root")}})
	stats, _ := Functions(p, 0)
	if stats["f"].Cum != 100 {
		t.Fatalf("recursive cum: got %d, want 100", stats["f"].Cum)
	}
	if stats["f"].Flat != 100 {
		t.Fatalf("recursive flat: got %d, want 100", stats["f"].Flat)
	}
}

func TestValuesAccumulateAcrossSamples(t *testing.T) {
	p := build(t,
		sample{60, []pprof.Frame{frame("hot"), frame("root")}},
		sample{40, []pprof.Frame{frame("hot"), frame("root")}},
		sample{25, []pprof.Frame{frame("cold"), frame("root")}},
	)
	stats, total := Functions(p, 0)
	if total != 125 {
		t.Fatalf("total: got %d, want 125", total)
	}
	if stats["hot"].Flat != 100 || stats["root"].Cum != 125 {
		t.Fatalf("accumulation wrong: hot flat=%d root cum=%d", stats["hot"].Flat, stats["root"].Cum)
	}
}

func TestInlinedFramesShareLocation(t *testing.T) {
	// Fabricate a location that carries two lines (an inlined call):
	// line[0] is the innermost frame and owns the flat value; both
	// frames receive cum.
	b := pprof.NewBuilder(pprof.ValueType{Type: "cpu", Unit: "nanoseconds"})
	b.AddSample([]int64{50}, frame("inner"))
	p, err := pprof.Parse(b.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	p.Functions[99] = &pprof.Function{ID: 99, Name: "outer", Filename: "outer.go"}
	loc := p.Locations[1]
	loc.Lines = append(loc.Lines, pprof.Line{FunctionID: 99, Line: 7})

	stats, _ := Functions(p, 0)
	if stats["inner"].Flat != 50 || stats["inner"].Cum != 50 {
		t.Fatalf("inner: flat=%d cum=%d, want 50/50", stats["inner"].Flat, stats["inner"].Cum)
	}
	if stats["outer"].Flat != 0 || stats["outer"].Cum != 50 {
		t.Fatalf("outer: flat=%d cum=%d, want 0/50", stats["outer"].Flat, stats["outer"].Cum)
	}
}

func TestUnresolvedFramesStillAggregate(t *testing.T) {
	// A sample pointing at a location id that does not exist (corrupt or
	// filtered profile) must still land somewhere visible.
	p := build(t, sample{10, []pprof.Frame{frame("f")}})
	p.Samples[0].LocationIDs[0] = 424242
	stats, total := Functions(p, 0)
	if total != 10 {
		t.Fatalf("total: got %d, want 10", total)
	}
	if stats["[unknown]"] == nil || stats["[unknown]"].Flat != 10 {
		t.Fatalf("unknown location not aggregated: %+v", stats)
	}
	// A location that exists but has no line info (stripped binary)
	// aggregates under a stable address placeholder instead.
	p2 := build(t, sample{10, []pprof.Frame{frame("f")}})
	p2.Locations[1].Lines = nil // strip symbols
	stats2, _ := Functions(p2, 0)
	found := false
	for name, s := range stats2 {
		if len(name) > 3 && name[:3] == "[0x" && s.Flat == 10 {
			found = true
		}
	}
	if !found {
		t.Fatalf("no [0x...] placeholder in %v", keys(stats2))
	}
}

func TestOutOfRangeValueIndexIsIgnored(t *testing.T) {
	// Asking for value index 5 of a one-type profile must not panic and
	// must produce an empty aggregation.
	p := build(t, sample{10, []pprof.Frame{frame("f")}})
	stats, total := Functions(p, 5)
	if total != 0 || len(stats) != 0 {
		t.Fatalf("out-of-range index aggregated: total=%d stats=%d", total, len(stats))
	}
}

func TestSortedOrderIsDeterministic(t *testing.T) {
	stats := map[string]*Stat{
		"b": {Name: "b", Flat: 10, Cum: 20},
		"a": {Name: "a", Flat: 10, Cum: 20}, // full tie with b → name order
		"c": {Name: "c", Flat: 10, Cum: 30}, // flat tie → higher cum first
		"d": {Name: "d", Flat: 99, Cum: 99}, // highest flat first
	}
	got := Sorted(stats)
	want := []string{"d", "c", "a", "b"}
	var names []string
	for _, s := range got {
		names = append(names, s.Name)
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("sort order: got %v, want %v", names, want)
	}
}

func keys(m map[string]*Stat) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
