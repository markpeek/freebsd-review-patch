package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/markpeek/freebsd-review-patch/internal/prepare"
)

func writeResult(stdout io.Writer, manifest *prepare.Manifest, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(stdout).Encode(manifest)
	}
	// Failed attempts have no review to print. Their diagnostics are written
	// to stderr by the caller, and --json can still expose the error manifest.
	if manifest.Analysis == nil || manifest.Analysis.State != "complete" {
		return nil
	}
	report, err := os.Open(manifest.Analysis.Report)
	if err != nil {
		return fmt.Errorf("open Markdown review: %w", err)
	}
	defer func() { _ = report.Close() }()
	_, err = io.Copy(stdout, report)
	return err
}
