// Command gendocs renders docs/maths.md plus every features/*.feature
// file into a single, self-contained HTML page for sharing with an
// accountant. Standard library only.
//
// The page embeds the Markdown as text and renders it in the browser
// with marked.js from a CDN; if the CDN is unreachable it falls back to
// showing the raw text, so the file is still readable offline. Print it
// from the browser to get a PDF.
//
// Run via `make docs`.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func main() {
	out := flag.String("out", "docs/maths.html", "output HTML file path")
	flag.Parse()

	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "gendocs:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	md, err := os.ReadFile("docs/maths.md")
	if err != nil {
		return fmt.Errorf("reading docs/maths.md (run from the repo root): %w", err)
	}

	featureFiles, err := filepath.Glob("features/*.feature")
	if err != nil {
		return err
	}
	sort.Strings(featureFiles)

	var doc strings.Builder
	doc.Write(md)
	doc.WriteString("\n\n---\n\n# Appendix: the executable scenarios\n\n")
	doc.WriteString("Each block below is the verbatim content of a file in `features/`. ")
	doc.WriteString("The figures in the `Then` steps are produced by the calculation engine ")
	doc.WriteString("and rewritten in place by `make regen-features`; `make ci` fails if they ")
	doc.WriteString("are out of date.\n")

	for _, f := range featureFiles {
		c, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("reading %s: %w", f, err)
		}
		fmt.Fprintf(&doc, "\n## `%s`\n\n```gherkin\n%s\n```\n",
			filepath.Base(f), strings.TrimRight(string(c), "\n"))
	}

	page := buildPage(doc.String(), len(featureFiles))
	if err := os.WriteFile(out, []byte(page), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", out, err)
	}
	fmt.Printf("gendocs: wrote %s (%d feature files embedded)\n", out, len(featureFiles))
	return nil
}

// scriptClose matches any spelling of a script end-tag opener. HTML5
// end-tag matching is ASCII case-insensitive, so "</Script" closes a
// <script> block just as "</script" does.
var scriptClose = regexp.MustCompile(`(?i)</script`)

// buildPage wraps the Markdown payload in a self-contained HTML shell.
//
// The payload is embedded inside a <script type="text/markdown"> block,
// so every "</script" spelling is neutralised (case preserved) to keep
// the block from closing early.
//
// The footer carries a fingerprint of the payload rather than a
// generation timestamp: the output is then a pure function of
// docs/maths.md and features/, so regenerating on an unchanged tree is a
// no-op and `make check-docs` can gate the committed page against drift.
func buildPage(markdown string, featureCount int) string {
	payload := scriptClose.ReplaceAllStringFunc(markdown, func(m string) string {
		return `<\/` + m[2:]
	})
	sum := sha256.Sum256([]byte(markdown))
	fingerprint := hex.EncodeToString(sum[:])[:12]

	return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>taxman — how each tax figure is calculated</title>
<style>
  :root { color-scheme: light; }
  body {
    max-width: 46rem; margin: 2rem auto; padding: 0 1.25rem;
    font: 16px/1.65 Georgia, "Times New Roman", serif; color: #1a1a1a;
    background: #fff;
  }
  h1, h2, h3, h4 { font-family: -apple-system, Segoe UI, Roboto, sans-serif; line-height: 1.25; }
  h1 { font-size: 1.9rem; margin: 2.4rem 0 1rem; }
  h2 { font-size: 1.4rem; margin: 2.2rem 0 .8rem; border-bottom: 1px solid #ddd; padding-bottom: .2rem; }
  h3 { font-size: 1.1rem; margin: 1.6rem 0 .5rem; }
  code { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; font-size: .88em; background: #f4f4f4; padding: .1em .3em; border-radius: 3px; }
  pre { background: #f6f8fa; border: 1px solid #e2e2e2; border-radius: 5px; padding: .9rem 1rem; overflow-x: auto; font-size: .82rem; line-height: 1.5; }
  pre code { background: none; padding: 0; font-size: inherit; }
  table { border-collapse: collapse; width: 100%; margin: 1rem 0; font-size: .9rem; }
  th, td { border: 1px solid #ccc; padding: .4rem .6rem; text-align: left; vertical-align: top; }
  th { background: #f4f4f4; }
  blockquote { margin: 1rem 0; padding: .4rem 1rem; border-left: 4px solid #c0c0c0; background: #fafafa; color: #333; }
  a { color: #0b5cad; }
  hr { border: none; border-top: 1px solid #ddd; margin: 2.5rem 0; }
  .gendocs-foot { margin-top: 3rem; padding-top: 1rem; border-top: 1px solid #ddd; font-family: -apple-system, Segoe UI, sans-serif; font-size: .8rem; color: #666; }
  @media print {
    body { max-width: none; margin: 0; font-size: 11pt; }
    h1, h2, h3 { page-break-after: avoid; }
    pre, table, blockquote { page-break-inside: avoid; }
    a { color: inherit; text-decoration: none; }
  }
</style>
</head>
<body>
<div id="rendered">Rendering…</div>
<pre id="raw" hidden></pre>
<p class="gendocs-foot">
  Generated by <code>make docs</code> from <code>docs/maths.md</code> and
  ` + fmt.Sprint(featureCount) + ` feature files — source fingerprint
  <code>` + fingerprint + `</code>. Every figure is produced by the calculation
  engine, not written by hand. If this page shows raw text, the Markdown renderer
  could not be fetched — the content is complete and readable as-is.
</p>
<script id="doc" type="text/markdown">
` + payload + `
</script>
<script src="https://cdn.jsdelivr.net/npm/marked@12.0.2/marked.min.js" onerror="gendocsRaw()"></script>
<script>
  function gendocsRaw() {
    var raw = document.getElementById('raw');
    raw.textContent = document.getElementById('doc').textContent.trim();
    raw.hidden = false;
    document.getElementById('rendered').hidden = true;
  }
  (function () {
    var src = document.getElementById('doc').textContent;
    if (window.marked && typeof marked.parse === 'function') {
      marked.setOptions({ mangle: false, headerIds: true });
      document.getElementById('rendered').innerHTML = marked.parse(src);
      document.title = 'taxman — how each tax figure is calculated';
    } else {
      gendocsRaw();
    }
  })();
</script>
</body>
</html>
`
}
