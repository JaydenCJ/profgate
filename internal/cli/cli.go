// Package cli implements the profgate command-line interface. Run takes
// argv and two writers and returns an exit code, so the whole surface is
// testable in-process without building a binary.
package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/JaydenCJ/profgate/internal/aggregate"
	"github.com/JaydenCJ/profgate/internal/budget"
	"github.com/JaydenCJ/profgate/internal/diff"
	"github.com/JaydenCJ/profgate/internal/pprof"
	"github.com/JaydenCJ/profgate/internal/render"
	"github.com/JaydenCJ/profgate/internal/version"
)

// Exit codes. Documented in the README; `check` uses ExitBreach as its
// machine-readable verdict, so CI can distinguish "budget exceeded" from
// "profgate itself failed".
const (
	ExitOK      = 0 // success / all budgets respected
	ExitBreach  = 1 // check: at least one budget breached
	ExitUsage   = 2 // bad flags, bad budgets file, unit mismatch
	ExitRuntime = 3 // unreadable or corrupt profile
)

const usage = `profgate — diff pprof profiles and gate CI on CPU/alloc budgets

Usage:
  profgate diff  [flags] <base.pb.gz> <head.pb.gz>   compare two profiles
  profgate check [flags] <base.pb.gz> <head.pb.gz>   enforce budgets (exit 1 on breach)
  profgate show  [flags] <profile.pb.gz>             top functions of one profile
  profgate version                                   print the version

Common flags:
  --format text|markdown|json   output format (default text)
  --sample-type TYPE            e.g. cpu, alloc_space, or type/unit (default: profile default)
  --top N                       limit table rows (default 20; 0 = unlimited)
  --all                         include functions whose values did not change

Check flags:
  --budgets FILE                budgets file (see docs/budgets.md)
  --budget 'PATTERN k=v ...'    inline rule, repeatable

Exit codes: 0 ok · 1 budget breach · 2 usage/config error · 3 runtime error
`

// Run dispatches argv and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return ExitUsage
	}
	switch args[0] {
	case "diff":
		return runDiff(args[1:], stdout, stderr)
	case "check":
		return runCheck(args[1:], stdout, stderr)
	case "show":
		return runShow(args[1:], stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "profgate %s\n", version.Version)
		return ExitOK
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return ExitOK
	default:
		fmt.Fprintf(stderr, "profgate: unknown command %q\n\n%s", args[0], usage)
		return ExitUsage
	}
}

// commonFlags is the flag set shared by diff, check, and show.
type commonFlags struct {
	format     string
	sampleType string
	top        int
	all        bool
}

func newFlagSet(name string, stderr io.Writer, c *commonFlags, defaultTop int) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&c.format, "format", "text", "output format: text, markdown, or json")
	fs.StringVar(&c.sampleType, "sample-type", "", "sample type to compare (e.g. cpu, alloc_space)")
	fs.IntVar(&c.top, "top", defaultTop, "limit table rows; 0 = unlimited")
	fs.BoolVar(&c.all, "all", false, "include functions with unchanged values")
	return fs
}

func (c *commonFlags) validFormat(stderr io.Writer) bool {
	switch c.format {
	case "text", "markdown", "json":
		return true
	}
	fmt.Fprintf(stderr, "profgate: unknown --format %q (want text, markdown, or json)\n", c.format)
	return false
}

// loadPair parses the base and head profile arguments.
func loadPair(fs *flag.FlagSet, stderr io.Writer) (base, head *pprof.Profile, labels [2]string, exit int) {
	if fs.NArg() != 2 {
		fmt.Fprintf(stderr, "profgate: want exactly two profile paths (base, head), got %d\n", fs.NArg())
		return nil, nil, labels, ExitUsage
	}
	labels = [2]string{fs.Arg(0), fs.Arg(1)}
	var err error
	if base, err = pprof.ParseFile(fs.Arg(0)); err != nil {
		fmt.Fprintf(stderr, "profgate: %v\n", err)
		return nil, nil, labels, ExitRuntime
	}
	if head, err = pprof.ParseFile(fs.Arg(1)); err != nil {
		fmt.Fprintf(stderr, "profgate: %v\n", err)
		return nil, nil, labels, ExitRuntime
	}
	return base, head, labels, ExitOK
}

// compare builds the diff report for the resolved sample type.
func compare(base, head *pprof.Profile, labels [2]string, spec string, stderr io.Writer) (*diff.Report, int) {
	rep, err := diff.Compute(base, head, spec)
	if err != nil {
		fmt.Fprintf(stderr, "profgate: %v\n", err)
		return nil, ExitUsage
	}
	rep.BaseLabel, rep.HeadLabel = labels[0], labels[1]
	return rep, ExitOK
}

func runDiff(args []string, stdout, stderr io.Writer) int {
	var c commonFlags
	fs := newFlagSet("diff", stderr, &c, 20)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if !c.validFormat(stderr) {
		return ExitUsage
	}
	base, head, labels, exit := loadPair(fs, stderr)
	if exit != ExitOK {
		return exit
	}
	rep, exit := compare(base, head, labels, c.sampleType, stderr)
	if exit != ExitOK {
		return exit
	}
	switch c.format {
	case "markdown":
		render.MarkdownDiff(stdout, rep, render.Options{Top: c.top, All: c.all})
	case "json":
		render.JSONDiff(stdout, rep)
	default:
		render.TextDiff(stdout, rep, render.Options{Top: c.top, All: c.all})
	}
	return ExitOK
}

// budgetList collects repeatable --budget flags.
type budgetList []string

func (b *budgetList) String() string     { return strings.Join(*b, "; ") }
func (b *budgetList) Set(s string) error { *b = append(*b, s); return nil }

func runCheck(args []string, stdout, stderr io.Writer) int {
	var c commonFlags
	var budgetsPath string
	var inline budgetList
	fs := newFlagSet("check", stderr, &c, 10)
	fs.StringVar(&budgetsPath, "budgets", "", "path to a budgets file")
	fs.Var(&inline, "budget", "inline budget rule 'PATTERN key=value ...' (repeatable)")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if !c.validFormat(stderr) {
		return ExitUsage
	}

	rules, exit := loadRules(budgetsPath, inline, stderr)
	if exit != ExitOK {
		return exit
	}
	base, head, labels, exit := loadPair(fs, stderr)
	if exit != ExitOK {
		return exit
	}
	rep, exit := compare(base, head, labels, c.sampleType, stderr)
	if exit != ExitOK {
		return exit
	}
	res, err := budget.Evaluate(rules, rep)
	if err != nil {
		fmt.Fprintf(stderr, "profgate: %v\n", err)
		return ExitUsage
	}

	switch c.format {
	case "markdown":
		render.MarkdownCheck(stdout, rep, res, render.Options{Top: c.top, All: c.all})
	case "json":
		render.JSONCheck(stdout, rep, res)
	default:
		render.TextCheck(stdout, rep, res)
	}
	if res.Failed() {
		return ExitBreach
	}
	return ExitOK
}

// loadRules merges a budgets file with inline --budget flags.
func loadRules(path string, inline []string, stderr io.Writer) ([]budget.Rule, int) {
	var rules []budget.Rule
	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			fmt.Fprintf(stderr, "profgate: %v\n", err)
			return nil, ExitUsage
		}
		defer f.Close()
		rules, err = budget.Parse(f)
		if err != nil {
			fmt.Fprintf(stderr, "profgate: %s: %v\n", path, err)
			return nil, ExitUsage
		}
	}
	for _, s := range inline {
		r, err := budget.ParseRule(s, 0)
		if err != nil {
			fmt.Fprintf(stderr, "profgate: --budget %q: %v\n", s, err)
			return nil, ExitUsage
		}
		rules = append(rules, r)
	}
	if len(rules) == 0 {
		fmt.Fprintf(stderr, "profgate: no budgets given; pass --budgets FILE or --budget 'PATTERN k=v'\n")
		return nil, ExitUsage
	}
	return rules, ExitOK
}

func runShow(args []string, stdout, stderr io.Writer) int {
	var c commonFlags
	fs := newFlagSet("show", stderr, &c, 20)
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if !c.validFormat(stderr) {
		return ExitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintf(stderr, "profgate: want exactly one profile path, got %d\n", fs.NArg())
		return ExitUsage
	}
	p, err := pprof.ParseFile(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "profgate: %v\n", err)
		return ExitRuntime
	}
	vi, err := p.SampleTypeIndex(c.sampleType)
	if err != nil {
		fmt.Fprintf(stderr, "profgate: %v\n", err)
		return ExitUsage
	}
	stats, total := aggregate.Functions(p, vi)
	sorted := aggregate.Sorted(stats)
	vt := p.SampleTypes[vi]
	switch c.format {
	case "json":
		if c.top > 0 && len(sorted) > c.top {
			sorted = sorted[:c.top]
		}
		render.JSONShow(stdout, fs.Arg(0), vt.Type, vt.Unit, sorted, total)
	case "markdown":
		fmt.Fprintf(stderr, "profgate: show supports --format text or json\n")
		return ExitUsage
	default:
		render.TextShow(stdout, fs.Arg(0), vt.String(), vt.Unit, sorted, total, c.top)
	}
	return ExitOK
}
