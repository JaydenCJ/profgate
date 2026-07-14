// profgate — diffs pprof profiles and fails CI when functions exceed
// CPU or allocation budgets, printing PR-ready Markdown.
//
// version:    0.1.0
// author:     JaydenCJ
// license:    MIT
// repository: https://github.com/JaydenCJ/profgate
// keywords:   pprof, profiling, ci, performance, regression, go, benchmark-gate
//
// Zero runtime dependencies: standard library only.
module github.com/JaydenCJ/profgate

go 1.22
