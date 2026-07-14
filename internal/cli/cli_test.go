// In-process CLI integration tests: real argv, real files in t.TempDir,
// asserting on stdout/stderr text and the documented exit codes — the
// same contract scripts/smoke.sh checks against the built binary.
package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JaydenCJ/profgate/internal/pprof"
	"github.com/JaydenCJ/profgate/internal/version"
)

// run executes the CLI in-process and captures both streams.
func run(t *testing.T, args ...string) (exit int, stdout, stderr string) {
	t.Helper()
	var out, errBuf bytes.Buffer
	exit = Run(args, &out, &errBuf)
	return exit, out.String(), errBuf.String()
}

func frame(name string) pprof.Frame {
	return pprof.Frame{Name: name, File: name + ".go", Line: 1}
}

// writeCPU writes a gzipped CPU profile with the given flat values (ms).
func writeCPU(t *testing.T, path string, flatMs map[string]int64) {
	t.Helper()
	b := pprof.NewBuilder(
		pprof.ValueType{Type: "samples", Unit: "count"},
		pprof.ValueType{Type: "cpu", Unit: "nanoseconds"},
	)
	b.SetDefaultSampleType("cpu")
	names := make([]string, 0, len(flatMs))
	for n := range flatMs {
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
		v := flatMs[n]
		b.AddSample([]int64{v / 10, v * 1e6}, frame(n), frame("main.main"))
	}
	if err := os.WriteFile(path, b.MarshalGzipped(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// pair writes a base/head pair where app.Render regresses 10ms → 40ms.
func pair(t *testing.T) (base, head string) {
	t.Helper()
	dir := t.TempDir()
	base = filepath.Join(dir, "base.pb.gz")
	head = filepath.Join(dir, "head.pb.gz")
	writeCPU(t, base, map[string]int64{"app.Render": 10, "app.Query": 30})
	writeCPU(t, head, map[string]int64{"app.Render": 40, "app.Query": 30})
	return base, head
}

func TestVersionCommandAndFlag(t *testing.T) {
	for _, argv := range [][]string{{"version"}, {"--version"}} {
		exit, out, _ := run(t, argv...)
		if exit != ExitOK || strings.TrimSpace(out) != "profgate "+version.Version {
			t.Errorf("%v: exit=%d out=%q", argv, exit, out)
		}
	}
}

func TestTopLevelDispatch(t *testing.T) {
	// No arguments: usage on stderr, exit 2.
	exit, _, errOut := run(t)
	if exit != ExitUsage || !strings.Contains(errOut, "Usage:") {
		t.Fatalf("no args: exit=%d stderr=%q", exit, errOut)
	}
	// Unknown command: named in the error, exit 2.
	exit, _, errOut = run(t, "frobnicate")
	if exit != ExitUsage || !strings.Contains(errOut, "unknown command") {
		t.Fatalf("unknown: exit=%d stderr=%q", exit, errOut)
	}
	// Explicit help: usage on stdout, exit 0.
	exit, out, _ := run(t, "help")
	if exit != ExitOK || !strings.Contains(out, "profgate diff") {
		t.Fatalf("help: exit=%d out=%q", exit, out)
	}
}

func TestDiffTextEndToEnd(t *testing.T) {
	base, head := pair(t)
	exit, out, _ := run(t, "diff", base, head)
	if exit != ExitOK {
		t.Fatalf("exit=%d", exit)
	}
	for _, want := range []string{"cpu/nanoseconds", "app.Render", "+30ms (+300.0%)"} {
		if !strings.Contains(out, want) {
			t.Errorf("diff output missing %q:\n%s", want, out)
		}
	}
	// Unchanged app.Query is hidden by default.
	if strings.Contains(out, "app.Query") {
		t.Errorf("unchanged function listed without --all:\n%s", out)
	}
}

func TestDiffMarkdownAndJSONFormats(t *testing.T) {
	base, head := pair(t)
	exit, md, _ := run(t, "diff", "--format", "markdown", base, head)
	if exit != ExitOK || !strings.Contains(md, "## profgate diff") {
		t.Fatalf("markdown: exit=%d out=%q", exit, md)
	}
	exit, js, _ := run(t, "diff", "--format", "json", base, head)
	if exit != ExitOK {
		t.Fatalf("json exit=%d", exit)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(js), &got); err != nil {
		t.Fatalf("diff --format json is not valid JSON: %v", err)
	}
	if got["command"] != "diff" {
		t.Fatalf("json command: %v", got["command"])
	}
}

func TestDiffUsageErrorsExit2(t *testing.T) {
	base, head := pair(t)
	exit, _, errOut := run(t, "diff", "--format", "yaml", base, head)
	if exit != ExitUsage || !strings.Contains(errOut, "yaml") {
		t.Fatalf("bad format: exit=%d stderr=%q", exit, errOut)
	}
	exit, _, errOut = run(t, "diff", base)
	if exit != ExitUsage || !strings.Contains(errOut, "two profile paths") {
		t.Fatalf("one arg: exit=%d stderr=%q", exit, errOut)
	}
}

func TestDiffRuntimeErrorsExit3(t *testing.T) {
	base, _ := pair(t)
	exit, _, _ := run(t, "diff", base, filepath.Join(t.TempDir(), "nope.pb.gz"))
	if exit != ExitRuntime {
		t.Fatalf("missing file: exit=%d, want %d", exit, ExitRuntime)
	}
	// A corrupt profile names the offending file in the error.
	bad := filepath.Join(t.TempDir(), "corrupt.pb.gz")
	if err := os.WriteFile(bad, []byte("not a profile"), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, _, errOut := run(t, "diff", base, bad)
	if exit != ExitRuntime || !strings.Contains(errOut, "corrupt.pb.gz") {
		t.Fatalf("corrupt: exit=%d stderr=%q", exit, errOut)
	}
}

func TestCheckPassesUnderBudgetExits0(t *testing.T) {
	base, head := pair(t)
	exit, out, _ := run(t, "check", "--budget", "app.* max-flat-growth=400%", base, head)
	if exit != ExitOK || !strings.Contains(out, "check: PASS") {
		t.Fatalf("exit=%d out=%q", exit, out)
	}
}

func TestCheckBreachExits1(t *testing.T) {
	base, head := pair(t)
	exit, out, _ := run(t, "check", "--budget", "app.Render max-flat-growth=50%", base, head)
	if exit != ExitBreach {
		t.Fatalf("exit=%d, want %d", exit, ExitBreach)
	}
	if !strings.Contains(out, "BREACH") || !strings.Contains(out, "check: FAIL") {
		t.Fatalf("breach output:\n%s", out)
	}
}

func TestCheckReadsBudgetsFile(t *testing.T) {
	base, head := pair(t)
	budgets := filepath.Join(t.TempDir(), "budgets.txt")
	content := "# demo budgets\n@total max-growth=10%\napp.* max-flat=35ms\n"
	if err := os.WriteFile(budgets, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, out, _ := run(t, "check", "--budgets", budgets, base, head)
	if exit != ExitBreach {
		t.Fatalf("exit=%d, want breach", exit)
	}
	// Both the total rule (40→70 is +75%) and app.Render (40ms > 35ms) fire.
	if !strings.Contains(out, "@total") || !strings.Contains(out, "app.Render") {
		t.Fatalf("expected both breaches:\n%s", out)
	}
}

func TestCheckMarkdownVerdictOnStdout(t *testing.T) {
	base, head := pair(t)
	exit, out, _ := run(t, "check", "--format", "markdown",
		"--budget", "app.Render max-flat=20ms", base, head)
	if exit != ExitBreach {
		t.Fatalf("exit=%d", exit)
	}
	if !strings.Contains(out, "## profgate check — ❌ FAIL") ||
		!strings.Contains(out, "| `app.Render` | flat |") {
		t.Fatalf("markdown check output:\n%s", out)
	}
}

func TestCheckJSONVerdictMatchesExitCode(t *testing.T) {
	base, head := pair(t)
	exit, js, _ := run(t, "check", "--format", "json",
		"--budget", "app.Render max-flat=20ms", base, head)
	if exit != ExitBreach {
		t.Fatalf("exit=%d", exit)
	}
	var got struct {
		Verdict    string            `json:"verdict"`
		Violations []json.RawMessage `json:"violations"`
	}
	if err := json.Unmarshal([]byte(js), &got); err != nil {
		t.Fatal(err)
	}
	if got.Verdict != "fail" || len(got.Violations) != 1 {
		t.Fatalf("json verdict: %+v", got)
	}
}

func TestCheckWithoutBudgetsExits2(t *testing.T) {
	base, head := pair(t)
	exit, _, errOut := run(t, "check", base, head)
	if exit != ExitUsage || !strings.Contains(errOut, "no budgets") {
		t.Fatalf("exit=%d stderr=%q", exit, errOut)
	}
}

func TestCheckBadBudgetsFileExits2WithLineNumber(t *testing.T) {
	base, head := pair(t)
	budgets := filepath.Join(t.TempDir(), "budgets.txt")
	if err := os.WriteFile(budgets, []byte("app.* max-flat=oops\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, _, errOut := run(t, "check", "--budgets", budgets, base, head)
	if exit != ExitUsage || !strings.Contains(errOut, "line 1") {
		t.Fatalf("exit=%d stderr=%q", exit, errOut)
	}
}

func TestCheckUnitMismatchExits2(t *testing.T) {
	base, head := pair(t)
	exit, _, errOut := run(t, "check", "--budget", "app.* max-flat=4MiB", base, head)
	if exit != ExitUsage || !strings.Contains(errOut, "bytes") {
		t.Fatalf("exit=%d stderr=%q", exit, errOut)
	}
}

func TestCheckSampleTypeFlagSelectsCounts(t *testing.T) {
	// Budget the sample count (samples/count) instead of cpu time: bare
	// numbers are the only valid amounts for a count unit.
	base, head := pair(t)
	exit, out, _ := run(t, "check", "--sample-type", "samples",
		"--budget", "app.Render max-flat-growth=1", base, head)
	if exit != ExitBreach || !strings.Contains(out, "samples/count") {
		t.Fatalf("exit=%d out=%q", exit, out)
	}
}

func TestShowTextAndTopFlag(t *testing.T) {
	base, _ := pair(t)
	exit, out, _ := run(t, "show", "--top", "1", base)
	if exit != ExitOK {
		t.Fatalf("exit=%d", exit)
	}
	if !strings.Contains(out, "app.Query") || strings.Contains(out, "app.Render") {
		t.Fatalf("--top 1 should keep only the hottest function:\n%s", out)
	}
}

func TestShowJSONAndSampleTypeErrors(t *testing.T) {
	base, _ := pair(t)
	exit, js, _ := run(t, "show", "--format", "json", base)
	if exit != ExitOK {
		t.Fatalf("exit=%d", exit)
	}
	var got struct {
		Command string `json:"command"`
		Total   int64  `json:"total"`
	}
	if err := json.Unmarshal([]byte(js), &got); err != nil {
		t.Fatal(err)
	}
	if got.Command != "show" || got.Total != 40e6 {
		t.Fatalf("show json: %+v", got)
	}
	// Unknown sample types exit 2 and list what the profile does have.
	exit, _, errOut := run(t, "show", "--sample-type", "inuse_space", base)
	if exit != ExitUsage || !strings.Contains(errOut, "cpu/nanoseconds") {
		t.Fatalf("exit=%d stderr=%q", exit, errOut)
	}
}
