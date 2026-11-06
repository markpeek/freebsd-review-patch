package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/markpeek/freebsd-review-patch/internal/prepare"
)

func resultFixture(t *testing.T) (*prepare.Manifest, string) {
	t.Helper()
	text := "# FreeBSD code review\n\n## [P2] Example finding\n\n**Evidence:** a concrete trigger.\n"
	path := filepath.Join(t.TempDir(), "review.md")
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	count := 1
	return &prepare.Manifest{Schema: 1, State: "ready", Analysis: &prepare.Analysis{State: "complete", Reviewer: "codex", Report: path, Count: &count}}, text
}

func TestResultFormats(t *testing.T) {
	m, markdown := resultFixture(t)
	var stdout bytes.Buffer
	if err := writeResult(&stdout, m, false); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != markdown {
		t.Fatalf("Markdown styling was not preserved: %q", stdout.String())
	}
	stdout.Reset()
	if err := writeResult(&stdout, m, true); err != nil {
		t.Fatal(err)
	}
	var decoded prepare.Manifest
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Analysis.Report != m.Analysis.Report || *decoded.Analysis.Count != 1 {
		t.Fatal("JSON output lost review metadata")
	}
	if strings.Contains(stdout.String(), "Example finding") {
		t.Fatal("Markdown report content leaked into manifest output")
	}
}

func TestFailedReviewOutput(t *testing.T) {
	m, _ := resultFixture(t)
	m.Analysis.State, m.Analysis.Error, m.Analysis.Count = "error", "review failed", nil
	var stdout bytes.Buffer
	if err := writeResult(&stdout, m, false); err != nil || stdout.Len() != 0 {
		t.Fatalf("failed review printed a report: %s, %v", stdout.String(), err)
	}
	if err := writeResult(&stdout, m, true); err != nil || !json.Valid(stdout.Bytes()) {
		t.Fatalf("error manifest was not available in JSON mode: %v", err)
	}
}

type brokenOutput struct{}

func (brokenOutput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestOutputErrors(t *testing.T) {
	m, _ := resultFixture(t)
	for _, asJSON := range []bool{false, true} {
		if err := writeResult(brokenOutput{}, m, asJSON); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("lost output error: %v", err)
		}
	}
	m.Analysis.Report = filepath.Join(t.TempDir(), "missing.md")
	if err := writeResult(io.Discard, m, false); err == nil {
		t.Fatal("missing report was silently ignored")
	}
}

func TestQuietProgressStillReportsErrors(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("fake executable needs a POSIX shell")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FREEBSD_REVIEW_HEADLESS", "")
	for _, option := range []string{"", "-q", "--quiet"} {
		t.Run(option, func(t *testing.T) {
			args := []string{"--manifest", filepath.Join(dir, "missing.json"), "--reviewer", "codex"}
			if option != "" {
				args = append(args, option)
			}
			var stdout, stderr bytes.Buffer
			if code := run(context.Background(), args, &stdout, &stderr); code != 1 {
				t.Fatalf("unexpected exit: %d; %s", code, stderr.String())
			}
			hasProgress := strings.Contains(stderr.String(), "Loading prepared manifest:")
			if hasProgress != (option == "") {
				t.Fatalf("quiet/progress behavior: %s", stderr.String())
			}
			if !strings.Contains(stderr.String(), "no such file") || stdout.Len() != 0 {
				t.Fatalf("error was hidden or stdout contaminated: %s / %s", stderr.String(), stdout.String())
			}
		})
	}
}
