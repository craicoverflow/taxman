// Command taxman is a self-hosted ledger and tax calculator for Irish
// investment income: DIRT, CGT, and exit-tax/deemed-disposal, computed
// from CSV exports across multiple brokers. See SPEC.md for the full
// design.
package main

import (
	"fmt"
	"os"
)

// commands maps each subcommand name to its handler. As of
// tasks/plan.md Phase 8, every one of these has a real implementation
// — see each command's own file (serve.go, import.go, backfill.go,
// classify.go, report.go, validate.go, migrate.go).
var commands = map[string]func(args []string) error{
	"serve":    runServe,
	"import":   runImport,
	"backfill": runBackfill,
	"classify": runClassify,
	"report":   runReport,
	"validate": runValidate,
	"migrate":  runMigrate,
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "taxman:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usageError()
	}

	cmd, ok := commands[args[0]]
	if !ok {
		return fmt.Errorf("unknown command %q\n\n%s", args[0], usage())
	}

	return cmd(args[1:])
}

func usageError() error {
	return fmt.Errorf("%s", usage())
}

func usage() string {
	return `usage: taxman <command> [arguments]

commands:
  serve      start the local dashboard server
  import     ingest a single platform export file
  backfill   ingest a directory of historical export files
  classify   manage holding tax classifications
  report     generate a tax-year report
  validate   run the computation engine against a golden fixture
  migrate    apply or roll back the database schema`
}
