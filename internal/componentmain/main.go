// Package componentmain provides the common Phase 1 command scaffold.
package componentmain

import (
	"flag"
	"fmt"
	"io"
)

// Version is replaced by release builds.
var Version = "development"

// Run handles the common version surface until the component runtime is wired.
func Run(name string, args []string, stdout, stderr io.Writer) int {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(stderr)
	showVersion := set.Bool("version", false, "print version")
	if err := set.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintf(stdout, "%s %s\n", name, Version)
		return 0
	}
	fmt.Fprintf(stderr, "%s runtime is not wired yet; Phase 1 foundation only\n", name)
	return 2
}
