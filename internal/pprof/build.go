package pprof

import (
	"bytes"
	"compress/gzip"
)

// This file implements a deterministic profile *writer*. profgate itself
// only reads profiles; the writer exists so tests, the smoke script, and
// examples/make-demo-profiles can fabricate byte-stable pprof inputs
// without depending on wall-clock CPU sampling (which is inherently
// flaky) or on a protobuf runtime.

// encoder appends protobuf wire data to a buffer.
type encoder struct{ buf []byte }

func (e *encoder) varint(v uint64) {
	for v >= 0x80 {
		e.buf = append(e.buf, byte(v)|0x80)
		v >>= 7
	}
	e.buf = append(e.buf, byte(v))
}

func (e *encoder) tag(field, wire int) { e.varint(uint64(field)<<3 | uint64(wire)) }

// int64Field emits a varint field, omitting zero values like proto3.
func (e *encoder) int64Field(field int, v int64) {
	if v == 0 {
		return
	}
	e.tag(field, wireVarint)
	e.varint(uint64(v))
}

// packedField emits a packed repeated varint field.
func (e *encoder) packedField(field int, vs []int64) {
	if len(vs) == 0 {
		return
	}
	var body encoder
	for _, v := range vs {
		body.varint(uint64(v))
	}
	e.bytesField(field, body.buf)
}

func (e *encoder) bytesField(field int, b []byte) {
	e.tag(field, wireBytes)
	e.varint(uint64(len(b)))
	e.buf = append(e.buf, b...)
}

// Frame is one stack frame handed to the Builder, leaf-first.
type Frame struct {
	Name string
	File string
	Line int64
}

// Builder assembles a synthetic pprof profile. All IDs and string-table
// indexes are assigned in first-use order, so identical call sequences
// produce byte-identical output.
type Builder struct {
	sampleTypes []ValueType
	defaultType string
	period      int64
	periodType  ValueType
	duration    int64

	strings   []string
	stringIdx map[string]int64

	functions []protoFunction
	funcIdx   map[Frame]uint64

	locations []protoLocation
	locIdx    map[Frame]uint64

	samples []protoSample
}

// NewBuilder starts a profile with the given sample types (at least one).
func NewBuilder(types ...ValueType) *Builder {
	b := &Builder{
		sampleTypes: types,
		stringIdx:   map[string]int64{},
		funcIdx:     map[Frame]uint64{},
		locIdx:      map[Frame]uint64{},
	}
	b.str("") // index 0 must be the empty string
	return b
}

// SetDefaultSampleType records which type `--sample-type ""` resolves to.
func (b *Builder) SetDefaultSampleType(t string) { b.defaultType = t }

// SetPeriod records the sampling period, e.g. 10ms for the Go CPU profiler.
func (b *Builder) SetPeriod(vt ValueType, period int64) {
	b.periodType, b.period = vt, period
}

// SetDuration records the profile's wall-clock duration in nanoseconds.
func (b *Builder) SetDuration(ns int64) { b.duration = ns }

func (b *Builder) str(s string) int64 {
	if i, ok := b.stringIdx[s]; ok {
		return i
	}
	i := int64(len(b.strings))
	b.strings = append(b.strings, s)
	b.stringIdx[s] = i
	return i
}

func (b *Builder) location(f Frame) uint64 {
	if id, ok := b.locIdx[f]; ok {
		return id
	}
	fnID, ok := b.funcIdx[f]
	if !ok {
		fnID = uint64(len(b.functions) + 1)
		b.functions = append(b.functions, protoFunction{
			id:        fnID,
			name:      b.str(f.Name),
			filename:  b.str(f.File),
			startLine: f.Line,
		})
		b.funcIdx[f] = fnID
	}
	id := uint64(len(b.locations) + 1)
	b.locations = append(b.locations, protoLocation{
		id:      id,
		address: 0x1000 + id*0x10, // synthetic but stable
		lines:   []protoLine{{functionID: fnID, line: f.Line}},
	})
	b.locIdx[f] = id
	return id
}

// AddSample records one stack (leaf-first) with one value per sample type.
func (b *Builder) AddSample(values []int64, stack ...Frame) {
	s := protoSample{values: append([]int64(nil), values...)}
	for _, f := range stack {
		s.locationIDs = append(s.locationIDs, b.location(f))
	}
	b.samples = append(b.samples, s)
}

// Marshal encodes the profile as an uncompressed profile.proto message.
func (b *Builder) Marshal() []byte {
	var e encoder
	for _, vt := range b.sampleTypes {
		var m encoder
		m.int64Field(1, b.str(vt.Type))
		m.int64Field(2, b.str(vt.Unit))
		e.bytesField(1, m.buf)
	}
	for _, s := range b.samples {
		var m encoder
		ids := make([]int64, len(s.locationIDs))
		for i, id := range s.locationIDs {
			ids[i] = int64(id)
		}
		m.packedField(1, ids)
		m.packedField(2, s.values)
		e.bytesField(2, m.buf)
	}
	for _, loc := range b.locations {
		var m encoder
		m.int64Field(1, int64(loc.id))
		m.int64Field(3, int64(loc.address))
		for _, ln := range loc.lines {
			var lm encoder
			lm.int64Field(1, int64(ln.functionID))
			lm.int64Field(2, ln.line)
			m.bytesField(4, lm.buf)
		}
		e.bytesField(4, m.buf)
	}
	for _, fn := range b.functions {
		var m encoder
		m.int64Field(1, int64(fn.id))
		m.int64Field(2, fn.name)
		m.int64Field(4, fn.filename)
		m.int64Field(5, fn.startLine)
		e.bytesField(5, m.buf)
	}
	for _, s := range b.strings {
		e.bytesField(6, []byte(s))
	}
	e.int64Field(10, b.duration)
	if b.periodType != (ValueType{}) {
		var m encoder
		m.int64Field(1, b.str(b.periodType.Type))
		m.int64Field(2, b.str(b.periodType.Unit))
		e.bytesField(11, m.buf)
	}
	e.int64Field(12, b.period)
	if b.defaultType != "" {
		e.int64Field(14, b.str(b.defaultType))
	}
	return e.buf
}

// MarshalGzipped encodes the profile gzip-compressed, the way the Go
// runtime writes pprof files. Compression is deterministic: no mod-time
// header, fixed compression level.
func (b *Builder) MarshalGzipped() []byte {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	_, _ = zw.Write(b.Marshal())
	_ = zw.Close()
	return buf.Bytes()
}
