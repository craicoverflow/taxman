package main

import (
	"strings"
	"testing"
)

// TestBuildPageNeutralisesEveryScriptCaseSpelling pins the fix for a
// tokenisation bug: HTML5 end-tag matching is ASCII case-insensitive, so
// "</Script" in docs/maths.md or a feature file would close the
// <script id="doc" type="text/markdown"> wrapper just as "</script"
// does, spilling the rest of the payload into the document.
func TestBuildPageNeutralisesEveryScriptCaseSpelling(t *testing.T) {
	for _, spelling := range []string{"</script>", "</Script>", "</SCRIPT>", "</ScRiPt >"} {
		page := buildPage("prose mentioning "+spelling+" inline", 1)

		body := page[strings.Index(page, `<script id="doc"`):]
		payload := body[:strings.Index(body, "\n</script>")]
		if strings.Contains(payload, spelling) {
			t.Errorf("%q survived unescaped in the markdown payload", spelling)
		}
		if !strings.Contains(payload, `<\/`+spelling[2:]) {
			t.Errorf("%q was not neutralised with its case preserved", spelling)
		}
	}
}

// TestBuildPageIsDeterministic guards the `make check-docs` drift gate:
// the committed docs/maths.html must be a pure function of its sources,
// with no timestamp to churn the file on every regeneration.
func TestBuildPageIsDeterministic(t *testing.T) {
	if a, b := buildPage("# same input", 3), buildPage("# same input", 3); a != b {
		t.Error("buildPage output differs between runs on identical input")
	}
	if a, b := buildPage("# one", 3), buildPage("# two", 3); a == b {
		t.Error("buildPage output is identical for different input; fingerprint is not tracking the payload")
	}
}
