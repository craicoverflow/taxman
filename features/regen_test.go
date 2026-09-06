package features

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// pinnedValue is the recording, made while running `make regen-features`,
// of what the engine actually produced for one Then step. It is enough
// to locate the step's line in its .feature file (regardless of the now
// stale number written there) and substitute the fresh number.
type pinnedValue struct {
	uri        string // feature file path, as godog reports it (relative to this dir)
	fromLine   int    // the "Scenario:" line; the step is on or after it
	stepPrefix string // the step text with its trailing value token removed
	newValue   string // the freshly computed value token to write
}

var recorded []pinnedValue

// valueToken matches the single figure at the end of an assertion step:
// a euro amount (€1,270.00, -€40.00), a percentage (33%, 38.5%), an ISO
// date (2026-01-01), a quoted string, or a bare number. Assertion steps
// are written so this is always the last token.
var valueToken = regexp.MustCompile(`(?:-?€ ?[\d,]+\.\d{2}|-?[\d,]+(?:\.\d+)?\s?%|"[^"]*"|\d{4}-\d{2}-\d{2}|-?[\d,]+(?:\.\d+)?)\s*$`)

var stepKeyword = regexp.MustCompile(`^(\s*)(Given|When|Then|And|But|\*)\s+`)

// recordPinned is called by an assertion step in regen mode instead of
// comparing. stepText is the full step text godog passes (keyword
// already stripped), still carrying the old value.
func recordPinned(uri string, fromLine int, stepText, newValue string) {
	recorded = append(recorded, pinnedValue{
		uri:        uri,
		fromLine:   fromLine,
		stepPrefix: strings.TrimSpace(valueToken.ReplaceAllString(stepText, "")),
		newValue:   newValue,
	})
}

var regenTouchedFiles = map[string]bool{}
var regenChangedLines int

func regenEditCount() int { return regenChangedLines }
func regenFileCount() int { return len(regenTouchedFiles) }

// applyRegenEdits rewrites each feature file in place, substituting the
// freshly recorded value into the one line each pinnedValue identifies.
// Untouched lines are preserved byte-for-byte.
func applyRegenEdits() error {
	byFile := map[string][]pinnedValue{}
	for _, p := range recorded {
		byFile[p.uri] = append(byFile[p.uri], p)
	}

	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)

	for _, uri := range files {
		raw, err := os.ReadFile(uri)
		if err != nil {
			return fmt.Errorf("reading %s: %w", uri, err)
		}
		lines := strings.Split(string(raw), "\n")
		used := map[int]bool{}
		changed := false

		for _, p := range byFile[uri] {
			idx, err := findStepLine(lines, p, used)
			if err != nil {
				return fmt.Errorf("%s: %w", uri, err)
			}
			used[idx] = true

			indent, keyword, rest := splitStep(lines[idx])
			newRest := strings.TrimRight(valueToken.ReplaceAllString(rest, ""), " ") + " " + p.newValue
			newLine := indent + keyword + " " + newRest
			if newLine != lines[idx] {
				lines[idx] = newLine
				changed = true
				regenChangedLines++
			}
		}

		if changed {
			regenTouchedFiles[uri] = true
			if err := os.WriteFile(uri, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
				return fmt.Errorf("writing %s: %w", uri, err)
			}
		}
	}
	return nil
}

// findStepLine locates the unused line, at or after p.fromLine, whose
// step text (minus keyword, minus trailing value) equals p.stepPrefix.
func findStepLine(lines []string, p pinnedValue, used map[int]bool) (int, error) {
	start := p.fromLine - 1
	if start < 0 {
		start = 0
	}
	for i := start; i < len(lines); i++ {
		if used[i] || !stepKeyword.MatchString(lines[i]) {
			continue
		}
		_, _, rest := splitStep(lines[i])
		if strings.TrimSpace(valueToken.ReplaceAllString(rest, "")) == p.stepPrefix {
			return i, nil
		}
	}
	return 0, fmt.Errorf("could not find step %q at or after line %d (did the .feature wording change?)", p.stepPrefix, p.fromLine)
}

// splitStep breaks a feature-file line into leading whitespace, the
// step keyword, and the remaining step text.
func splitStep(line string) (indent, keyword, rest string) {
	m := stepKeyword.FindStringSubmatch(line)
	if m == nil {
		return "", "", line
	}
	return m[1], m[2], strings.TrimSpace(line[len(m[0]):])
}
