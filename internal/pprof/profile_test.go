// Profile-level tests: gzip handling, string-table resolution, model
// invariants, sample-type selection, and Builder determinism.
package pprof

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// demoBuilder fabricates a small two-type CPU-style profile.
func demoBuilder() *Builder {
	b := NewBuilder(
		ValueType{Type: "samples", Unit: "count"},
		ValueType{Type: "cpu", Unit: "nanoseconds"},
	)
	b.SetDefaultSampleType("cpu")
	b.SetPeriod(ValueType{Type: "cpu", Unit: "nanoseconds"}, 10_000_000)
	b.SetDuration(1_000_000_000)
	leaf := Frame{Name: "app.Leaf", File: "app/leaf.go", Line: 10}
	root := Frame{Name: "app.Root", File: "app/root.go", Line: 3}
	b.AddSample([]int64{3, 30_000_000}, leaf, root)
	b.AddSample([]int64{1, 10_000_000}, root)
	return b
}

func TestParseGzippedRoundTrip(t *testing.T) {
	data := demoBuilder().MarshalGzipped()
	if !bytes.HasPrefix(data, gzipMagic) {
		t.Fatal("MarshalGzipped output is not gzip")
	}
	p, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.SampleTypes) != 2 || p.SampleTypes[1].String() != "cpu/nanoseconds" {
		t.Fatalf("sample types wrong: %v", p.SampleTypes)
	}
	if len(p.Samples) != 2 {
		t.Fatalf("got %d samples, want 2", len(p.Samples))
	}
	if p.DefaultSampleType != "cpu" {
		t.Fatalf("default sample type: got %q, want cpu", p.DefaultSampleType)
	}
	if p.Period != 10_000_000 || p.DurationNanos != 1_000_000_000 {
		t.Fatalf("period/duration lost: %d / %d", p.Period, p.DurationNanos)
	}
}

func TestParseUncompressedRoundTrip(t *testing.T) {
	p, err := Parse(demoBuilder().Marshal())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Functions) != 2 {
		t.Fatalf("got %d functions, want 2", len(p.Functions))
	}
	// The leaf sample's first location must resolve to app.Leaf.
	if name := p.FunctionName(p.Samples[0].LocationIDs[0]); name != "app.Leaf" {
		t.Fatalf("leaf function: got %q, want app.Leaf", name)
	}
}

func TestParseRejectsGarbageAndTruncation(t *testing.T) {
	valid := demoBuilder().MarshalGzipped()
	for name, data := range map[string][]byte{
		"text":            []byte("this is not a profile"),
		"empty":           {},
		"gzip magic only": {0x1f, 0x8b},
		"truncated gzip":  valid[:len(valid)/2],
	} {
		if _, err := Parse(data); err == nil {
			t.Errorf("%s: garbage parsed without error", name)
		}
	}
	// And unreadable paths surface as errors, not panics.
	if _, err := ParseFile(filepath.Join(t.TempDir(), "missing.pb.gz")); err == nil {
		t.Fatal("missing file parsed without error")
	}
}

func TestParseRejectsProfileWithoutSampleTypes(t *testing.T) {
	// A message that only carries a string table is structurally valid
	// protobuf but not a usable profile.
	var e encoder
	e.bytesField(6, []byte(""))
	if _, err := Parse(e.buf); err == nil || !strings.Contains(err.Error(), "sample type") {
		t.Fatalf("want sample-type error, got %v", err)
	}
}

func TestParseValidatesStringTable(t *testing.T) {
	// profile.proto requires string_table[0] == "".
	var e encoder
	e.bytesField(6, []byte("oops"))
	var vt encoder
	vt.int64Field(1, 0)
	vt.int64Field(2, 0)
	e.bytesField(1, vt.buf)
	if _, err := Parse(e.buf); err == nil || !strings.Contains(err.Error(), "entry 0") {
		t.Fatalf("want string-table error, got %v", err)
	}
	// And every string index must be in range.
	var e2 encoder
	e2.bytesField(6, []byte(""))
	var vt2 encoder
	vt2.int64Field(1, 42) // index 42 does not exist
	e2.bytesField(1, vt2.buf)
	if _, err := Parse(e2.buf); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("want out-of-range error, got %v", err)
	}
}

func TestParseValidatesSampleValueArity(t *testing.T) {
	// One sample type but a two-value sample: silently mispairing values
	// with types would corrupt every downstream number.
	b := NewBuilder(ValueType{Type: "cpu", Unit: "nanoseconds"})
	b.AddSample([]int64{1, 2}, Frame{Name: "f", File: "f.go", Line: 1})
	if _, err := Parse(b.Marshal()); err == nil || !strings.Contains(err.Error(), "values") {
		t.Fatalf("want arity error, got %v", err)
	}
}

func TestSampleTypeIndexEmptySpecUsesDefaultThenLast(t *testing.T) {
	p, err := Parse(demoBuilder().Marshal())
	if err != nil {
		t.Fatal(err)
	}
	i, err := p.SampleTypeIndex("")
	if err != nil || i != 1 {
		t.Fatalf("default index: got %d (%v), want 1 (cpu)", i, err)
	}
	// Without default_sample_type, pprof convention is the last type.
	b := NewBuilder(
		ValueType{Type: "alloc_objects", Unit: "count"},
		ValueType{Type: "alloc_space", Unit: "bytes"},
	)
	b.AddSample([]int64{1, 64}, Frame{Name: "f", File: "f.go", Line: 1})
	p, err = Parse(b.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	i, err = p.SampleTypeIndex("")
	if err != nil || i != 1 {
		t.Fatalf("fallback index: got %d (%v), want 1 (alloc_space)", i, err)
	}
}

func TestSampleTypeIndexByBareNameAndQualified(t *testing.T) {
	p, err := Parse(demoBuilder().Marshal())
	if err != nil {
		t.Fatal(err)
	}
	for spec, want := range map[string]int{
		"samples":         0,
		"cpu":             1,
		"cpu/nanoseconds": 1,
		"samples/count":   0,
	} {
		i, err := p.SampleTypeIndex(spec)
		if err != nil || i != want {
			t.Errorf("SampleTypeIndex(%q): got %d (%v), want %d", spec, i, err, want)
		}
	}
}

func TestSampleTypeIndexUnknownListsAvailableTypes(t *testing.T) {
	p, err := Parse(demoBuilder().Marshal())
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.SampleTypeIndex("alloc_space")
	if err == nil || !strings.Contains(err.Error(), "cpu/nanoseconds") {
		t.Fatalf("error should list available types, got %v", err)
	}
	// A right type with the wrong unit must not match.
	if _, err := p.SampleTypeIndex("cpu/bytes"); err == nil {
		t.Fatal("cpu/bytes matched a nanoseconds profile")
	}
}

func TestFunctionNameFallsBackToAddress(t *testing.T) {
	p, err := Parse(demoBuilder().Marshal())
	if err != nil {
		t.Fatal(err)
	}
	// Strip the line info from one location to simulate an
	// unsymbolized frame.
	loc := p.Locations[1]
	loc.Lines = nil
	got := p.FunctionName(1)
	if !strings.HasPrefix(got, "[0x") {
		t.Fatalf("unsymbolized name: got %q, want [0x...] placeholder", got)
	}
	if p.FunctionName(999) != "[unknown]" {
		t.Fatalf("missing location should name [unknown], got %q", p.FunctionName(999))
	}
}

func TestBuilderOutputIsByteStable(t *testing.T) {
	// Two identical build sequences must produce identical bytes — the
	// smoke script and README outputs depend on it.
	a := demoBuilder().MarshalGzipped()
	b := demoBuilder().MarshalGzipped()
	if !bytes.Equal(a, b) {
		t.Fatal("MarshalGzipped is not deterministic")
	}
}

func TestBuilderInternsFramesAndStrings(t *testing.T) {
	b := NewBuilder(ValueType{Type: "cpu", Unit: "nanoseconds"})
	f := Frame{Name: "app.F", File: "app/f.go", Line: 1}
	b.AddSample([]int64{1}, f)
	b.AddSample([]int64{2}, f) // same frame again
	p, err := Parse(b.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Locations) != 1 || len(p.Functions) != 1 {
		t.Fatalf("frame not interned: %d locations, %d functions", len(p.Locations), len(p.Functions))
	}
	if p.Samples[0].LocationIDs[0] != p.Samples[1].LocationIDs[0] {
		t.Fatal("identical frames got different location ids")
	}
}
