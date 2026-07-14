package render

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/JaydenCJ/profgate/internal/aggregate"
	"github.com/JaydenCJ/profgate/internal/budget"
	"github.com/JaydenCJ/profgate/internal/diff"
	"github.com/JaydenCJ/profgate/internal/version"
)

// schemaVersion is bumped only on breaking JSON shape changes.
const schemaVersion = 1

type jsonSampleType struct {
	Type string `json:"type"`
	Unit string `json:"unit"`
}

type jsonTotal struct {
	Base  int64 `json:"base"`
	Head  int64 `json:"head"`
	Delta int64 `json:"delta"`
}

type jsonFunction struct {
	Name      string `json:"name"`
	FlatBase  int64  `json:"flat_base"`
	FlatHead  int64  `json:"flat_head"`
	FlatDelta int64  `json:"flat_delta"`
	CumBase   int64  `json:"cum_base"`
	CumHead   int64  `json:"cum_head"`
	CumDelta  int64  `json:"cum_delta"`
	InBase    bool   `json:"in_base"`
	InHead    bool   `json:"in_head"`
}

type jsonViolation struct {
	Function string `json:"function"`
	Metric   string `json:"metric"`
	Limit    string `json:"limit"`     // "max-flat-growth=50%"
	RuleLine int    `json:"rule_line"` // 0 for --budget flags
	Base     int64  `json:"base"`
	Head     int64  `json:"head"`
	Allowed  int64  `json:"allowed"`
	Growth   bool   `json:"growth"`
}

type jsonReport struct {
	Tool          string           `json:"tool"`
	SchemaVersion int              `json:"schema_version"`
	Version       string           `json:"version"`
	Command       string           `json:"command"`
	SampleType    jsonSampleType   `json:"sample_type"`
	Base          string           `json:"base,omitempty"`
	Head          string           `json:"head,omitempty"`
	Total         jsonTotal        `json:"total"`
	Verdict       string           `json:"verdict,omitempty"` // check only: "pass" | "fail"
	Checks        *int             `json:"checks,omitempty"`  // check only
	Matched       *int             `json:"functions_matched,omitempty"`
	Violations    *[]jsonViolation `json:"violations,omitempty"` // check only; [] when none
	Functions     []jsonFunction   `json:"functions"`
}

func envelope(command string, r *diff.Report) jsonReport {
	out := jsonReport{
		Tool:          "profgate",
		SchemaVersion: schemaVersion,
		Version:       version.Version,
		Command:       command,
		SampleType:    jsonSampleType{Type: r.SampleType.Type, Unit: r.SampleType.Unit},
		Base:          r.BaseLabel,
		Head:          r.HeadLabel,
		Total:         jsonTotal{Base: r.TotalBase, Head: r.TotalHead, Delta: r.TotalDelta()},
		Functions:     []jsonFunction{},
	}
	for _, e := range r.Entries {
		out.Functions = append(out.Functions, jsonFunction{
			Name:     e.Name,
			FlatBase: e.FlatBase, FlatHead: e.FlatHead, FlatDelta: e.FlatDelta(),
			CumBase: e.CumBase, CumHead: e.CumHead, CumDelta: e.CumDelta(),
			InBase: e.InBase, InHead: e.InHead,
		})
	}
	return out
}

func writeJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	// Encoding our own structs cannot fail; ignore the writer error the
	// same way fmt.Fprintf does elsewhere.
	_ = enc.Encode(v)
}

// JSONDiff writes the diff report as indented, stable JSON.
func JSONDiff(w io.Writer, r *diff.Report) {
	writeJSON(w, envelope("diff", r))
}

// JSONCheck writes the check report, including verdict and violations.
func JSONCheck(w io.Writer, r *diff.Report, res *budget.Result) {
	out := envelope("check", r)
	out.Verdict = "pass"
	if res.Failed() {
		out.Verdict = "fail"
	}
	out.Checks = &res.Checked
	out.Matched = &res.Matched
	violations := []jsonViolation{}
	for _, v := range res.Violations {
		violations = append(violations, jsonViolation{
			Function: v.Function,
			Metric:   v.Metric,
			Limit:    fmt.Sprintf("%s=%s", v.Key, v.LimitRaw),
			RuleLine: v.RuleLine,
			Base:     v.Base,
			Head:     v.Head,
			Allowed:  v.Allowed,
			Growth:   v.Growth,
		})
	}
	out.Violations = &violations
	writeJSON(w, out)
}

type jsonShowFunction struct {
	Name string `json:"name"`
	Flat int64  `json:"flat"`
	Cum  int64  `json:"cum"`
}

type jsonShow struct {
	Tool          string             `json:"tool"`
	SchemaVersion int                `json:"schema_version"`
	Version       string             `json:"version"`
	Command       string             `json:"command"`
	SampleType    jsonSampleType     `json:"sample_type"`
	Profile       string             `json:"profile"`
	Total         int64              `json:"total"`
	Functions     []jsonShowFunction `json:"functions"`
}

// JSONShow writes a single profile's aggregation as JSON.
func JSONShow(w io.Writer, label, typ, unit string, stats []aggregate.Stat, total int64) {
	out := jsonShow{
		Tool:          "profgate",
		SchemaVersion: schemaVersion,
		Version:       version.Version,
		Command:       "show",
		SampleType:    jsonSampleType{Type: typ, Unit: unit},
		Profile:       label,
		Total:         total,
		Functions:     []jsonShowFunction{},
	}
	for _, s := range stats {
		out.Functions = append(out.Functions, jsonShowFunction{Name: s.Name, Flat: s.Flat, Cum: s.Cum})
	}
	writeJSON(w, out)
}
