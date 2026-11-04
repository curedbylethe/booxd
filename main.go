package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: booxd <search|get|show|tui|serve>")
		return 2
	}

	switch args[0] {
	case "search", "get", "show", "tui", "serve":
		fmt.Fprintf(stdout, "booxd: %s is not implemented yet\n", args[0])
		return 1
	default:
		fmt.Fprintf(stderr, "booxd: unknown command %q\n", args[0])
		return 2
	}
}
