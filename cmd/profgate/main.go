// Command profgate diffs pprof profiles and fails CI when functions
// exceed CPU or allocation budgets.
package main

import (
	"os"

	"github.com/JaydenCJ/profgate/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
