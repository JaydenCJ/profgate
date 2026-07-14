// Command make-demo-profiles writes a deterministic set of pprof
// profiles into a directory: a base/head CPU pair where one render
// function regresses hard, and a base/head heap pair where a cache
// starts over-allocating. The files are byte-stable across runs, so the
// smoke script and the README examples always see the same numbers.
//
// Usage: go run ./examples/make-demo-profiles <output-dir>
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/JaydenCJ/profgate/internal/pprof"
)

// frame builds a stack frame with a synthetic but plausible file path.
func frame(name string) pprof.Frame {
	return pprof.Frame{Name: name, File: "demoapp/" + name + ".go", Line: 42}
}

// Shared demo stacks (leaf-first, the pprof convention).
var (
	fMain    = frame("main.main")
	fServe   = frame("demoapp/router.Serve")
	fIndex   = frame("demoapp/handlers.Index")
	fReport  = frame("demoapp/handlers.Report")
	fTable   = frame("demoapp/render.Table")
	fQuery   = frame("demoapp/store.Query")
	fMarshal = frame("encoding/json.Marshal")
	fCache   = frame("demoapp/cache.Fill")
)

const ms = int64(1e6) // milliseconds in nanoseconds

// cpuProfile fabricates a CPU profile. Values are (samples, cpu-nanos)
// pairs; the sampling period is the Go default of 10ms.
func cpuProfile(tableMs, queryMs, marshalMs, indexMs int64) []byte {
	b := pprof.NewBuilder(
		pprof.ValueType{Type: "samples", Unit: "count"},
		pprof.ValueType{Type: "cpu", Unit: "nanoseconds"},
	)
	b.SetDefaultSampleType("cpu")
	b.SetPeriod(pprof.ValueType{Type: "cpu", Unit: "nanoseconds"}, 10*ms)
	b.SetDuration(1e9)
	c := func(valMs int64, stack ...pprof.Frame) {
		b.AddSample([]int64{valMs / 10, valMs * ms}, stack...)
	}
	c(tableMs, fTable, fReport, fServe, fMain)
	c(queryMs, fQuery, fReport, fServe, fMain)
	c(marshalMs, fMarshal, fIndex, fServe, fMain)
	c(indexMs, fIndex, fServe, fMain)
	return b.MarshalGzipped()
}

const kib = int64(1024)

// heapProfile fabricates an alloc profile with (objects, bytes) values.
func heapProfile(cacheKiB, queryKiB int64) []byte {
	b := pprof.NewBuilder(
		pprof.ValueType{Type: "alloc_objects", Unit: "count"},
		pprof.ValueType{Type: "alloc_space", Unit: "bytes"},
	)
	b.SetDefaultSampleType("alloc_space")
	b.SetPeriod(pprof.ValueType{Type: "space", Unit: "bytes"}, 512*kib)
	b.AddSample([]int64{cacheKiB / 4, cacheKiB * kib}, fCache, fIndex, fServe, fMain)
	b.AddSample([]int64{queryKiB / 4, queryKiB * kib}, fQuery, fReport, fServe, fMain)
	return b.MarshalGzipped()
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: make-demo-profiles <output-dir>")
		os.Exit(2)
	}
	dir := os.Args[1]
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	files := map[string][]byte{
		// Head: render.Table regresses 10ms → 42ms, store.Query improves
		// slightly, json.Marshal is flat, handlers.Index grows a little.
		"base.cpu.pb.gz":  cpuProfile(10, 30, 20, 40),
		"head.cpu.pb.gz":  cpuProfile(42, 28, 20, 44),
		"base.heap.pb.gz": heapProfile(2048, 1024),
		"head.heap.pb.gz": heapProfile(5120, 1000),
	}
	for name, data := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(path)
	}
}
