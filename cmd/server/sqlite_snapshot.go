package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/RhinoLink-Tech/data-fabric/internal/config"
)

// sqlite-metadata backup and restore both create a new consistent snapshot.
// Restore targets must be unused and services must be stopped by the operator.
func runSQLiteSnapshot(args []string) error {
	if len(args) == 0 || (args[0] != "backup" && args[0] != "restore") {
		return errors.New("usage: sqlite-metadata backup|restore --source ABSOLUTE_PATH --output ABSOLUTE_PATH")
	}
	fs := flag.NewFlagSet("sqlite-metadata "+args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	source := fs.String("source", "", "source metadata file")
	output := fs.String("output", "", "new snapshot file")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 || *source == "" || *output == "" {
		return errors.New("usage: sqlite-metadata backup|restore --source ABSOLUTE_PATH --output ABSOLUTE_PATH")
	}
	if err := config.SnapshotSQLiteMetadata(context.Background(), *source, *output); err != nil {
		return fmt.Errorf("SQLite metadata %s: %w", args[0], err)
	}
	return nil
}
