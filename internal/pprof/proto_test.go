// Wire-level tests for the minimal protobuf decoder: varint edge cases,
// truncation, unknown fields, and packed/unpacked repeated integers.
// These matter because profgate must never crash on a corrupt or
// foreign-generated profile — it must fail with a clear error instead.
package pprof

import (
	"bytes"
	"testing"
)

func TestVarintRoundTripBoundaryValues(t *testing.T) {
	// Each value sits on a 7-bit group boundary where off-by-one shift
	// bugs hide.
	values := []uint64{0, 1, 127, 128, 16383, 16384, 1<<32 - 1, 1 << 32, 1<<64 - 1}
	for _, want := range values {
		var e encoder
		e.varint(want)
		r := reader{data: e.buf}
		got, err := r.varint()
		if err != nil {
			t.Fatalf("varint(%d): %v", want, err)
		}
		if got != want {
			t.Errorf("varint round trip: got %d, want %d", got, want)
		}
		if !r.done() {
			t.Errorf("varint(%d): %d trailing bytes", want, len(e.buf)-r.pos)
		}
	}
}

func TestVarintRejectsTruncatedAndOverlongInput(t *testing.T) {
	// A continuation bit set on the final byte promises more data.
	r := reader{data: []byte{0x80}}
	if _, err := r.varint(); err == nil {
		t.Fatal("truncated varint decoded without error")
	}
	// Eleven continuation bytes exceed 64 bits.
	r = reader{data: bytes.Repeat([]byte{0xff}, 11)}
	if _, err := r.varint(); err == nil {
		t.Fatal("overlong varint decoded without error")
	}
}

func TestMalformedFieldsAreRejected(t *testing.T) {
	// Tag 0 (field 0, wire 0) is invalid protobuf and typically means
	// the input is not a protobuf at all.
	r := reader{data: []byte{0x00}}
	if _, _, err := r.field(); err == nil {
		t.Fatal("field number 0 accepted")
	}
	// A bytes field declaring 100 bytes of payload but supplying 2.
	var e encoder
	e.varint(100)
	e.buf = append(e.buf, 0x01, 0x02)
	r = reader{data: e.buf}
	if _, err := r.bytesField(); err == nil {
		t.Fatal("truncated bytes field decoded without error")
	}
}

func TestSkipHandlesAllKnownWireTypes(t *testing.T) {
	var e encoder
	e.varint(7)                               // wire 0 payload
	fixed64 := []byte{1, 2, 3, 4, 5, 6, 7, 8} // wire 1 payload
	fixed32 := []byte{9, 10, 11, 12}          // wire 5 payload
	data := append(append(append([]byte{}, e.buf...), fixed64...), fixed32...)
	r := reader{data: data}
	for _, wire := range []int{wireVarint, wireFixed64, wireFixed32} {
		if err := r.skip(wire); err != nil {
			t.Fatalf("skip(wire %d): %v", wire, err)
		}
	}
	if !r.done() {
		t.Fatalf("skip left %d bytes", len(data)-r.pos)
	}
	// Wire types 3/4 (deprecated groups) are not valid in profile.proto.
	r = reader{data: []byte{0x00}}
	if err := r.skip(3); err == nil {
		t.Fatal("group wire type skipped without error")
	}
}

func TestRepeatedIntsAcceptPackedAndUnpacked(t *testing.T) {
	// profile.proto declares Sample.location_id packed, but decoders
	// must accept the unpacked encoding too (older writers used it).
	var packed encoder
	packed.packedField(1, []int64{3, 1, 2})

	var unpacked encoder
	for _, v := range []int64{3, 1, 2} {
		unpacked.tag(1, wireVarint)
		unpacked.varint(uint64(v))
	}

	for name, buf := range map[string][]byte{"packed": packed.buf, "unpacked": unpacked.buf} {
		s, err := decodeSample(buf)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(s.locationIDs) != 3 || s.locationIDs[0] != 3 || s.locationIDs[2] != 2 {
			t.Errorf("%s: got location ids %v, want [3 1 2]", name, s.locationIDs)
		}
	}
}

func TestDecodeProfileSkipsUnknownFields(t *testing.T) {
	// Splice a fictitious field 99 (bytes) between real fields; the
	// decoder must ignore it for forward compatibility.
	b := NewBuilder(ValueType{Type: "cpu", Unit: "nanoseconds"})
	b.AddSample([]int64{5}, Frame{Name: "f", File: "f.go", Line: 1})
	var e encoder
	e.bytesField(99, []byte("future extension"))
	data := append(e.buf, b.Marshal()...)

	p, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse with unknown field: %v", err)
	}
	if len(p.Samples) != 1 {
		t.Fatalf("got %d samples, want 1", len(p.Samples))
	}
}

func TestNegativeValuesSurviveRoundTrip(t *testing.T) {
	// Heap deltas can be negative; int64 values are encoded as their
	// two's-complement uint64 varint form and must come back intact.
	b := NewBuilder(ValueType{Type: "delta", Unit: "bytes"})
	b.AddSample([]int64{-4096}, Frame{Name: "f", File: "f.go", Line: 1})
	p, err := Parse(b.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Samples[0].Values[0]; got != -4096 {
		t.Fatalf("negative value round trip: got %d, want -4096", got)
	}
}
