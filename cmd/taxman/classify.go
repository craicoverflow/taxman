package main

import (
	"database/sql"
	"flag"
	"fmt"

	_ "modernc.org/sqlite"

	"github.com/craicoverflow/taxman/internal/classify"
	"github.com/craicoverflow/taxman/internal/db"
)

// runClassify implements `taxman classify --list-unclassified | --set <instrument> <classification> [--db path]`.
func runClassify(args []string) error {
	fs := flag.NewFlagSet("classify", flag.ContinueOnError)
	listUnclassified := fs.Bool("list-unclassified", false, "list holdings with no classification on record")
	set := fs.Bool("set", false, "set a holding's classification: --set <instrument> <CGT_ASSET|EXIT_TAX_FUND>")
	dbPath := fs.String("db", defaultDBPath, "path to the SQLite database file")

	// --set takes two positional values, so it must be parsed before
	// the flag package tries (and fails) to interpret them as flags.
	// flag.FlagSet doesn't support intermixed positional args well;
	// simplest robust approach is a manual scan for --set's operands.
	instrument, classificationArg, args, err := extractSetOperands(args)
	if err != nil {
		return err
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	if !*listUnclassified && !*set {
		return fmt.Errorf("usage: taxman classify --list-unclassified | --set <instrument> <CGT_ASSET|EXIT_TAX_FUND> [--db path]")
	}

	conn, err := sql.Open("sqlite", *dbPath)
	if err != nil {
		return fmt.Errorf("opening database %s: %w", *dbPath, err)
	}
	defer func() { _ = conn.Close() }()

	if err := db.Up(conn, db.Migrations); err != nil {
		return fmt.Errorf("ensuring schema is up to date: %w", err)
	}

	store := classify.NewStore(conn)

	if *set {
		classification, ok := classify.ValidClassifications[classificationArg]
		if !ok {
			return fmt.Errorf("classify: invalid classification %q (expected CGT_ASSET or EXIT_TAX_FUND)", classificationArg)
		}
		if err := store.Set(instrument, classification); err != nil {
			return fmt.Errorf("setting classification: %w", err)
		}
		fmt.Printf("classified %s as %s\n", instrument, classification)
	}

	if *listUnclassified {
		unclassified, err := store.ListUnclassified()
		if err != nil {
			return fmt.Errorf("listing unclassified holdings: %w", err)
		}
		if len(unclassified) == 0 {
			fmt.Println("no unclassified holdings")
		}
		for _, instrument := range unclassified {
			fmt.Println(instrument)
		}
	}

	return nil
}

// extractSetOperands scans args for "--set" and, if present, removes
// it along with its next two positional operands (instrument,
// classification), returning them separately so the remaining args
// can be parsed normally by a flag.FlagSet. If "--set" isn't present,
// it returns empty operands and args unchanged.
func extractSetOperands(args []string) (instrument, classification string, rest []string, err error) {
	for i, a := range args {
		if a != "--set" {
			continue
		}
		if i+2 >= len(args) {
			return "", "", nil, fmt.Errorf("classify: --set requires two arguments: <instrument> <CGT_ASSET|EXIT_TAX_FUND>")
		}
		instrument = args[i+1]
		classification = args[i+2]

		rest = make([]string, 0, len(args)-2)
		rest = append(rest, args[:i]...)
		rest = append(rest, "--set")
		rest = append(rest, args[i+3:]...)
		return instrument, classification, rest, nil
	}
	return "", "", args, nil
}
