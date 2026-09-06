// Package features runs the executable, accountant-facing specification
// for taxman's tax mathematics: every .feature file in this directory is
// a worked example whose figures are produced by internal/engine and
// internal/taxrules, not by hand.
//
// Two modes:
//
//   - assert (default): `go test ./features/...` — every Then step checks
//     the engine's output against the number pinned in the .feature file.
//     Picked up by `make test` / `make ci`.
//   - regenerate: `REGEN_FEATURES=1 go test ./features/...` (via
//     `make regen-features`) — every Then step instead records the
//     engine's current output and rewrites the pinned number in place.
//     CI fails if running this would change any file (the drift gate),
//     which is how a change to engine/taxrules is forced to refresh the
//     worked examples in the same commit — the same discipline the
//     golden fixtures in testdata/golden/ already follow.
//
// docs/maths.md is the prose companion: it states each rule in words
// with the Irish tax-law citation; these features prove the engine
// implements what it describes.
package features

import (
	"os"
	"testing"

	"github.com/cucumber/godog"
	"github.com/cucumber/godog/colors"
)

// regenMode is set by `make regen-features`. In regen mode the Then
// steps record the engine's output instead of asserting on it, and
// TestFeatures rewrites the .feature files afterwards.
var regenMode = os.Getenv("REGEN_FEATURES") == "1"

func TestFeatures(t *testing.T) {
	opts := godog.Options{
		Format:   "pretty",
		Paths:    []string{"."},
		Output:   colors.Colored(os.Stdout),
		TestingT: t,
		// Serial: the regen pass rewrites files and the world is held
		// per scenario; there is no value in concurrency here.
		Concurrency: 1,
		Strict:      true,
	}

	suite := godog.TestSuite{
		Name:                "taxman-maths",
		ScenarioInitializer: InitializeScenario,
		Options:             &opts,
	}

	status := suite.Run()

	if regenMode {
		if err := applyRegenEdits(); err != nil {
			t.Fatalf("regenerating feature files: %v", err)
		}
		t.Logf("regen: rewrote %d pinned figure(s) across %d feature file(s)", regenEditCount(), regenFileCount())
		return
	}

	if status != 0 {
		t.Fatalf("feature suite failed (status %d)", status)
	}
}
