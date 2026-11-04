package main

import (
	"bytes"
	"testing"
)

func TestRun(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		code int
		out  string
		err  string
	}{
		{name: "no argument", code: 2, err: "usage: booxd <search|get|show|tui|serve>\n"},
		{name: "unknown command", args: []string{"nope"}, code: 2, err: "booxd: unknown command \"nope\"\n"},
		{name: "search", args: []string{"search"}, code: 1, out: "booxd: search is not implemented yet\n"},
		{name: "get", args: []string{"get"}, code: 1, out: "booxd: get is not implemented yet\n"},
		{name: "show", args: []string{"show"}, code: 1, out: "booxd: show is not implemented yet\n"},
		{name: "tui", args: []string{"tui"}, code: 1, out: "booxd: tui is not implemented yet\n"},
		{name: "serve", args: []string{"serve"}, code: 1, out: "booxd: serve is not implemented yet\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tt.args, &stdout, &stderr); code != tt.code {
				t.Fatalf("exit code = %d, want %d", code, tt.code)
			}
			if stdout.String() != tt.out || stderr.String() != tt.err {
				t.Fatalf("stdout = %q, stderr = %q; want %q, %q", stdout.String(), stderr.String(), tt.out, tt.err)
			}
		})
	}
}
