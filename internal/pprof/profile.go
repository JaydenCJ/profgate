package pprof

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"strings"
)

// ValueType names one measured dimension of a profile, e.g.
// {Type: "cpu", Unit: "nanoseconds"} or {Type: "alloc_space", Unit: "bytes"}.
type ValueType struct {
	Type string
	Unit string
}

// String renders the canonical "type/unit" spelling used in CLI flags.
func (vt ValueType) String() string { return vt.Type + "/" + vt.Unit }

// Sample is one measured stack: LocationIDs are leaf-first, Values are
// parallel to Profile.SampleTypes.
type Sample struct {
	LocationIDs []uint64
	Values      []int64
}

// Line is one (possibly inlined) source position inside a Location.
type Line struct {
	FunctionID uint64
	Line       int64
}

// Location is one resolved program address. Lines lists the inlined call
// chain at that address, innermost frame first.
type Location struct {
	ID      uint64
	Address uint64
	Lines   []Line
}

// Function is one symbolized function.
type Function struct {
	ID       uint64
	Name     string
	Filename string
}

// Profile is a fully string-resolved pprof profile.
type Profile struct {
	SampleTypes       []ValueType
	Samples           []Sample
	Locations         map[uint64]*Location
	Functions         map[uint64]*Function
	PeriodType        ValueType
	Period            int64
	TimeNanos         int64
	DurationNanos     int64
	DefaultSampleType string
}

// gzipMagic is the two-byte header every gzip stream starts with; pprof
// files written by the Go runtime are always gzip-compressed, but the
// format also allows raw protobuf, so Parse accepts both.
var gzipMagic = []byte{0x1f, 0x8b}

// ParseFile reads and parses a pprof profile from disk.
func ParseFile(path string) (*Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	p, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

// Parse decodes a pprof profile from raw bytes, transparently
// decompressing gzip.
func Parse(data []byte) (*Profile, error) {
	if bytes.HasPrefix(data, gzipMagic) {
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("not a pprof profile: %w", err)
		}
		data, err = io.ReadAll(zr)
		if err != nil {
			return nil, fmt.Errorf("not a pprof profile: %w", err)
		}
	}
	raw, err := decodeProfile(data)
	if err != nil {
		return nil, fmt.Errorf("not a pprof profile: %w", err)
	}
	return resolve(raw)
}

// resolve turns the raw wire message into the string-resolved model,
// validating the invariants profgate relies on.
func resolve(raw *protoProfile) (*Profile, error) {
	str := func(i int64) (string, error) {
		if i < 0 || i >= int64(len(raw.stringTable)) {
			return "", fmt.Errorf("string table index %d out of range (table has %d entries)", i, len(raw.stringTable))
		}
		return raw.stringTable[i], nil
	}
	if len(raw.stringTable) > 0 && raw.stringTable[0] != "" {
		return nil, fmt.Errorf("string table entry 0 must be empty, got %q", raw.stringTable[0])
	}
	if len(raw.sampleType) == 0 {
		return nil, fmt.Errorf("profile declares no sample types")
	}

	p := &Profile{
		Locations:     make(map[uint64]*Location, len(raw.location)),
		Functions:     make(map[uint64]*Function, len(raw.function)),
		Period:        raw.period,
		TimeNanos:     raw.timeNanos,
		DurationNanos: raw.durationNanos,
	}
	for _, vt := range raw.sampleType {
		t, err := str(vt.typ)
		if err != nil {
			return nil, err
		}
		u, err := str(vt.unit)
		if err != nil {
			return nil, err
		}
		p.SampleTypes = append(p.SampleTypes, ValueType{Type: t, Unit: u})
	}
	if t, err := str(raw.periodType.typ); err == nil {
		if u, err2 := str(raw.periodType.unit); err2 == nil {
			p.PeriodType = ValueType{Type: t, Unit: u}
		}
	}
	if raw.defaultSampleType != 0 {
		dt, err := str(raw.defaultSampleType)
		if err != nil {
			return nil, err
		}
		p.DefaultSampleType = dt
	}
	for i, s := range raw.sample {
		if len(s.values) != len(p.SampleTypes) {
			return nil, fmt.Errorf("sample %d has %d values, want %d (one per sample type)", i, len(s.values), len(p.SampleTypes))
		}
		p.Samples = append(p.Samples, Sample{LocationIDs: s.locationIDs, Values: s.values})
	}
	for _, loc := range raw.location {
		l := &Location{ID: loc.id, Address: loc.address}
		for _, ln := range loc.lines {
			l.Lines = append(l.Lines, Line{FunctionID: ln.functionID, Line: ln.line})
		}
		p.Locations[loc.id] = l
	}
	for _, fn := range raw.function {
		name, err := str(fn.name)
		if err != nil {
			return nil, err
		}
		file, err := str(fn.filename)
		if err != nil {
			return nil, err
		}
		p.Functions[fn.id] = &Function{ID: fn.id, Name: name, Filename: file}
	}
	return p, nil
}

// SampleTypeIndex resolves a --sample-type spec to a value index.
//
// spec may be "" (use the profile's default_sample_type if set, else the
// last type — matching `go tool pprof` behavior), a bare type name like
// "cpu" or "alloc_space", or a fully-qualified "type/unit".
func (p *Profile) SampleTypeIndex(spec string) (int, error) {
	if spec == "" {
		if p.DefaultSampleType != "" {
			for i, vt := range p.SampleTypes {
				if vt.Type == p.DefaultSampleType {
					return i, nil
				}
			}
		}
		return len(p.SampleTypes) - 1, nil
	}
	typ, unit, qualified := strings.Cut(spec, "/")
	for i, vt := range p.SampleTypes {
		if vt.Type == typ && (!qualified || vt.Unit == unit) {
			return i, nil
		}
	}
	var have []string
	for _, vt := range p.SampleTypes {
		have = append(have, vt.String())
	}
	return 0, fmt.Errorf("sample type %q not in profile (have: %s)", spec, strings.Join(have, ", "))
}

// FunctionName returns the display name for a location's innermost frame:
// the symbolized function name when available, otherwise a stable
// hex-address placeholder for stripped or unsymbolized frames.
func (p *Profile) FunctionName(locID uint64) string {
	loc, ok := p.Locations[locID]
	if !ok {
		return "[unknown]"
	}
	if len(loc.Lines) > 0 {
		if fn, ok := p.Functions[loc.Lines[0].FunctionID]; ok && fn.Name != "" {
			return fn.Name
		}
	}
	return fmt.Sprintf("[0x%x]", loc.Address)
}
