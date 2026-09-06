package main

import (
	"strings"
	"testing"
)

func TestRun_NoArgs_ReturnsUsageError(t *testing.T) {
	err := run(nil)
	if err == nil {
		t.Fatal("expected an error for no arguments, got nil")
	}
	if !strings.Contains(err.Error(), "usage:") {
		t.Errorf("expected usage text in error, got: %v", err)
	}
}

func TestRun_UnknownCommand_ReturnsError(t *testing.T) {
	err := run([]string{"bogus"})
	if err == nil {
		t.Fatal("expected an error for an unknown command, got nil")
	}
	if !strings.Contains(err.Error(), `unknown command "bogus"`) {
		t.Errorf("expected unknown-command message, got: %v", err)
	}
}

func TestRun_AllSevenCommandsAreRegistered(t *testing.T) {
	want := []string{"serve", "import", "backfill", "classify", "report", "validate", "migrate"}
	for _, name := range want {
		if _, ok := commands[name]; !ok {
			t.Errorf("expected command %q to be registered", name)
		}
	}
	if len(commands) != len(want) {
		t.Errorf("expected exactly %d commands, got %d", len(want), len(commands))
	}
}

func TestRun_NoCommandsAreStubbedAnymore(t *testing.T) {
	// As of task 8.4, every one of the 7 subcommands has a real
	// implementation — see each command's own _test.go file for its
	// coverage (serve_test.go, import_test.go, backfill_test.go,
	// classify_test.go, report_test.go, validate_test.go,
	// migrate_test.go). This is a regression guard: running each with
	// no args should error for a real reason (missing required flags,
	// etc.), never with the literal "not implemented" suffix the old
	// stub helper used to produce during Phases 0-7.
	//
	// serve is excluded: with no args it would actually start
	// listening and block forever rather than returning an error —
	// its own real-implementation coverage lives in serve_test.go.
	for name, cmd := range commands {
		if name == "serve" {
			continue
		}
		err := cmd(nil)
		if err != nil && strings.HasSuffix(err.Error(), "not implemented") {
			t.Errorf("command %q still appears to be stubbed: %v", name, err)
		}
	}
}
