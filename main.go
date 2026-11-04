package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/curedbylethe/booxd/internal/fetch"
	"github.com/curedbylethe/booxd/internal/mirror"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, resolveMirror))
}

type resolveFunc func(ctx context.Context, override string) (string, error)

func resolveMirror(ctx context.Context, override string) (string, error) {
	if override != "" {
		return strings.TrimRight(override, "/"), nil
	}
	path, err := mirror.StatePath()
	if err != nil {
		return "", err
	}
	return mirror.Resolve(ctx, fetch.New(), path, mirror.Seeds(), time.Now())
}

func run(args []string, stdout, stderr io.Writer, resolve resolveFunc) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: booxd <search|get|show|tui|serve>")
		return 2
	}

	switch args[0] {
	case "search":
		flags := flag.NewFlagSet("search", flag.ContinueOnError)
		flags.SetOutput(stderr)
		flags.Usage = func() {
			_, _ = fmt.Fprintln(stderr, "usage: booxd search [flags] [query]\nflags must precede query arguments")
			flags.PrintDefaults()
		}
		override := flags.String("mirror", "", "base URL; skip mirror discovery")
		if err := flags.Parse(args[1:]); err != nil {
			return 2
		}
		base, err := resolve(context.Background(), *override)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "booxd: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintf(stderr, "mirror: %s\n", base)
		_, _ = fmt.Fprintln(stdout, "booxd: search is not implemented yet")
		return 1
	case "get", "show", "tui", "serve":
		_, _ = fmt.Fprintf(stdout, "booxd: %s is not implemented yet\n", args[0])
		return 1
	default:
		_, _ = fmt.Fprintf(stderr, "booxd: unknown command %q\n", args[0])
		return 2
	}
}
