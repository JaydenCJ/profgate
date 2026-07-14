// Package pprof reads and synthesizes profiles in the pprof
// profile.proto wire format using only the Go standard library.
//
// Rather than pulling in a protobuf runtime (and its transitive tree),
// this file implements exactly the subset of the protobuf wire format
// that profile.proto uses: varints (wire type 0) and length-delimited
// fields (wire type 2), with repeated integers accepted in both packed
// and unpacked encodings. Unknown fields and the unused fixed32/fixed64
// wire types are skipped, so profiles produced by future runtimes still
// parse.
package pprof

import (
	"errors"
	"fmt"
)

// Wire types from the protobuf encoding spec. pprof profiles only emit
// wireVarint and wireBytes, but the decoder tolerates the rest.
const (
	wireVarint  = 0
	wireFixed64 = 1
	wireBytes   = 2
	wireFixed32 = 5
)

var errTruncated = errors.New("truncated protobuf data")

// reader is a cursor over a raw protobuf message body.
type reader struct {
	data []byte
	pos  int
}

func (r *reader) done() bool { return r.pos >= len(r.data) }

// varint decodes one base-128 varint at the cursor.
func (r *reader) varint() (uint64, error) {
	var v uint64
	for shift := uint(0); shift < 64; shift += 7 {
		if r.pos >= len(r.data) {
			return 0, errTruncated
		}
		b := r.data[r.pos]
		r.pos++
		v |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return v, nil
		}
	}
	return 0, errors.New("varint overflows 64 bits")
}

// field decodes the next field tag, returning field number and wire type.
func (r *reader) field() (num int, wire int, err error) {
	tag, err := r.varint()
	if err != nil {
		return 0, 0, err
	}
	num = int(tag >> 3)
	wire = int(tag & 7)
	if num == 0 {
		return 0, 0, errors.New("field number 0 is invalid")
	}
	return num, wire, nil
}

// bytesField decodes a length-delimited payload (wire type 2).
func (r *reader) bytesField() ([]byte, error) {
	n, err := r.varint()
	if err != nil {
		return nil, err
	}
	if n > uint64(len(r.data)-r.pos) {
		return nil, errTruncated
	}
	b := r.data[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return b, nil
}

// skip discards a field body of the given wire type.
func (r *reader) skip(wire int) error {
	switch wire {
	case wireVarint:
		_, err := r.varint()
		return err
	case wireFixed64:
		if len(r.data)-r.pos < 8 {
			return errTruncated
		}
		r.pos += 8
		return nil
	case wireBytes:
		_, err := r.bytesField()
		return err
	case wireFixed32:
		if len(r.data)-r.pos < 4 {
			return errTruncated
		}
		r.pos += 4
		return nil
	default:
		return fmt.Errorf("unsupported wire type %d", wire)
	}
}

// int64s appends one repeated int64 value (unpacked, wire 0) or a whole
// packed run (wire 2) to dst. profile.proto declares Sample.value and
// friends as packed, but decoders must accept both encodings.
func (r *reader) int64s(dst []int64, wire int) ([]int64, error) {
	if wire == wireVarint {
		v, err := r.varint()
		if err != nil {
			return nil, err
		}
		return append(dst, int64(v)), nil
	}
	if wire != wireBytes {
		return nil, fmt.Errorf("repeated int64 field has wire type %d", wire)
	}
	body, err := r.bytesField()
	if err != nil {
		return nil, err
	}
	rr := reader{data: body}
	for !rr.done() {
		v, err := rr.varint()
		if err != nil {
			return nil, err
		}
		dst = append(dst, int64(v))
	}
	return dst, nil
}

// uint64s is int64s for unsigned fields (location and function ids).
func (r *reader) uint64s(dst []uint64, wire int) ([]uint64, error) {
	vs, err := r.int64s(nil, wire)
	if err != nil {
		return nil, err
	}
	for _, v := range vs {
		dst = append(dst, uint64(v))
	}
	return dst, nil
}

// The raw message mirrors of profile.proto. Field numbers follow the
// canonical schema published with the pprof project; fields profgate does
// not need (mappings, labels, comments, drop/keep frames) are skipped.

type protoProfile struct {
	sampleType        []protoValueType // field 1
	sample            []protoSample    // field 2
	location          []protoLocation  // field 4
	function          []protoFunction  // field 5
	stringTable       []string         // field 6
	timeNanos         int64            // field 9
	durationNanos     int64            // field 10
	periodType        protoValueType   // field 11
	period            int64            // field 12
	defaultSampleType int64            // field 14
}

type protoValueType struct {
	typ  int64 // field 1: index into string table
	unit int64 // field 2: index into string table
}

type protoSample struct {
	locationIDs []uint64 // field 1, leaf first
	values      []int64  // field 2, parallel to sample_type
}

type protoLocation struct {
	id      uint64      // field 1
	address uint64      // field 3
	lines   []protoLine // field 4, innermost inlined frame first
}

type protoLine struct {
	functionID uint64 // field 1
	line       int64  // field 2
}

type protoFunction struct {
	id        uint64 // field 1
	name      int64  // field 2: index into string table
	filename  int64  // field 4: index into string table
	startLine int64  // field 5
}

func decodeValueType(data []byte) (protoValueType, error) {
	var vt protoValueType
	r := reader{data: data}
	for !r.done() {
		num, wire, err := r.field()
		if err != nil {
			return vt, err
		}
		switch {
		case num == 1 && wire == wireVarint:
			v, err := r.varint()
			if err != nil {
				return vt, err
			}
			vt.typ = int64(v)
		case num == 2 && wire == wireVarint:
			v, err := r.varint()
			if err != nil {
				return vt, err
			}
			vt.unit = int64(v)
		default:
			if err := r.skip(wire); err != nil {
				return vt, err
			}
		}
	}
	return vt, nil
}

func decodeSample(data []byte) (protoSample, error) {
	var s protoSample
	r := reader{data: data}
	for !r.done() {
		num, wire, err := r.field()
		if err != nil {
			return s, err
		}
		switch num {
		case 1:
			s.locationIDs, err = r.uint64s(s.locationIDs, wire)
		case 2:
			s.values, err = r.int64s(s.values, wire)
		default:
			err = r.skip(wire)
		}
		if err != nil {
			return s, err
		}
	}
	return s, nil
}

func decodeLine(data []byte) (protoLine, error) {
	var ln protoLine
	r := reader{data: data}
	for !r.done() {
		num, wire, err := r.field()
		if err != nil {
			return ln, err
		}
		switch {
		case num == 1 && wire == wireVarint:
			v, err := r.varint()
			if err != nil {
				return ln, err
			}
			ln.functionID = v
		case num == 2 && wire == wireVarint:
			v, err := r.varint()
			if err != nil {
				return ln, err
			}
			ln.line = int64(v)
		default:
			if err := r.skip(wire); err != nil {
				return ln, err
			}
		}
	}
	return ln, nil
}

func decodeLocation(data []byte) (protoLocation, error) {
	var loc protoLocation
	r := reader{data: data}
	for !r.done() {
		num, wire, err := r.field()
		if err != nil {
			return loc, err
		}
		switch {
		case num == 1 && wire == wireVarint:
			v, err := r.varint()
			if err != nil {
				return loc, err
			}
			loc.id = v
		case num == 3 && wire == wireVarint:
			v, err := r.varint()
			if err != nil {
				return loc, err
			}
			loc.address = v
		case num == 4 && wire == wireBytes:
			body, err := r.bytesField()
			if err != nil {
				return loc, err
			}
			ln, err := decodeLine(body)
			if err != nil {
				return loc, err
			}
			loc.lines = append(loc.lines, ln)
		default:
			if err := r.skip(wire); err != nil {
				return loc, err
			}
		}
	}
	return loc, nil
}

func decodeFunction(data []byte) (protoFunction, error) {
	var fn protoFunction
	r := reader{data: data}
	for !r.done() {
		num, wire, err := r.field()
		if err != nil {
			return fn, err
		}
		if wire != wireVarint {
			if err := r.skip(wire); err != nil {
				return fn, err
			}
			continue
		}
		v, err := r.varint()
		if err != nil {
			return fn, err
		}
		switch num {
		case 1:
			fn.id = v
		case 2:
			fn.name = int64(v)
		case 4:
			fn.filename = int64(v)
		case 5:
			fn.startLine = int64(v)
		}
	}
	return fn, nil
}

// decodeProfile decodes an uncompressed profile.proto message body.
func decodeProfile(data []byte) (*protoProfile, error) {
	p := &protoProfile{}
	r := reader{data: data}
	for !r.done() {
		num, wire, err := r.field()
		if err != nil {
			return nil, err
		}
		if wire == wireVarint {
			v, err := r.varint()
			if err != nil {
				return nil, err
			}
			switch num {
			case 9:
				p.timeNanos = int64(v)
			case 10:
				p.durationNanos = int64(v)
			case 12:
				p.period = int64(v)
			case 14:
				p.defaultSampleType = int64(v)
			}
			continue
		}
		if wire != wireBytes {
			if err := r.skip(wire); err != nil {
				return nil, err
			}
			continue
		}
		body, err := r.bytesField()
		if err != nil {
			return nil, err
		}
		switch num {
		case 1:
			vt, err := decodeValueType(body)
			if err != nil {
				return nil, err
			}
			p.sampleType = append(p.sampleType, vt)
		case 2:
			s, err := decodeSample(body)
			if err != nil {
				return nil, err
			}
			p.sample = append(p.sample, s)
		case 4:
			loc, err := decodeLocation(body)
			if err != nil {
				return nil, err
			}
			p.location = append(p.location, loc)
		case 5:
			fn, err := decodeFunction(body)
			if err != nil {
				return nil, err
			}
			p.function = append(p.function, fn)
		case 6:
			p.stringTable = append(p.stringTable, string(body))
		case 11:
			vt, err := decodeValueType(body)
			if err != nil {
				return nil, err
			}
			p.periodType = vt
		}
	}
	return p, nil
}
