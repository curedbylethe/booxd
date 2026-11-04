package main

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestRun(t *testing.T) {
	help := "usage: booxd search [flags] [query]\nflags must precede query arguments\n  -mirror string\n    \tbase URL; skip mirror discovery\n"
	for _, tt := range []struct {
		name     string
		args     []string
		code     int
		out      string
		err      string
		calls    int
		override string
		failure  bool
	}{
		{name: "no argument", code: 2, err: "usage: booxd <search|get|show|tui|serve>\n"},
		{name: "unknown command", args: []string{"nope"}, code: 2, err: "booxd: unknown command \"nope\"\n"},
		{name: "search", args: []string{"search"}, code: 1, out: "booxd: search is not implemented yet\n", err: "mirror: https://mirror.test\n", calls: 1},
		{name: "search override", args: []string{"search", "--mirror", "https://override.test"}, code: 1, out: "booxd: search is not implemented yet\n", err: "mirror: https://override.test\n", calls: 1, override: "https://override.test"},
		{name: "search positional argument", args: []string{"search", "test"}, code: 1, out: "booxd: search is not implemented yet\n", err: "mirror: https://mirror.test\n", calls: 1},
		{name: "search resolver error", args: []string{"search"}, code: 1, err: "booxd: no working mirror: x\n", calls: 1, failure: true},
		{name: "search unknown flag", args: []string{"search", "--nope"}, code: 2, err: "flag provided but not defined: -nope\n" + help},
		{name: "search missing flag value", args: []string{"search", "--mirror"}, code: 2, err: "flag needs an argument: -mirror\n" + help},
		{name: "search help", args: []string{"search", "-h"}, code: 2, err: help},
		{name: "get", args: []string{"get"}, code: 1, out: "booxd: get is not implemented yet\n"},
		{name: "show", args: []string{"show"}, code: 1, out: "booxd: show is not implemented yet\n"},
		{name: "tui", args: []string{"tui"}, code: 1, out: "booxd: tui is not implemented yet\n"},
		{name: "serve", args: []string{"serve"}, code: 1, out: "booxd: serve is not implemented yet\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			calls := 0
			resolve := func(ctx context.Context, override string) (string, error) {
				calls++
				if override != tt.override {
					t.Errorf("override = %q, want %q", override, tt.override)
				}
				if tt.failure {
					return "", errors.New("no working mirror: x")
				}
				if override != "" {
					return override, nil
				}
				return "https://mirror.test", nil
			}
			if code := run(tt.args, &stdout, &stderr, resolve); code != tt.code {
				t.Fatalf("exit code = %d, want %d", code, tt.code)
			}
			if stdout.String() != tt.out || stderr.String() != tt.err {
				t.Fatalf("stdout = %q, stderr = %q; want %q, %q", stdout.String(), stderr.String(), tt.out, tt.err)
			}
			if calls != tt.calls {
				t.Fatalf("resolver calls = %d, want %d", calls, tt.calls)
			}
		})
	}
}

func TestResolveMirrorOverride(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	got, err := resolveMirror(context.Background(), "https://mirror.test/")
	if err != nil || got != "https://mirror.test" {
		t.Fatalf("resolveMirror override = %q, %v", got, err)
	}
	if _, err := resolveMirror(context.Background(), ""); err == nil {
		t.Fatal("discovery succeeded without a user cache directory")
	}
}
