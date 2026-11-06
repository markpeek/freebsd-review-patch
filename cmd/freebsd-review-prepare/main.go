// freebsd-review-prepare fetches and checks a FreeBSD review, optionally running
// a headless reviewer. Reviews print Markdown by default; --json emits a manifest.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/markpeek/freebsd-review-patch/internal/prepare"
	"github.com/markpeek/freebsd-review-patch/internal/review"
	"github.com/spf13/pflag"
)

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := pflag.NewFlagSet("freebsd-review-prepare", pflag.ContinueOnError)
	flags.SetOutput(stderr)
	var cfg prepare.Config
	var reviewCfg review.Config
	var manifestPath string
	flags.StringVar(&cfg.Repo, "repo", ".", "source checkout (left unchanged)")
	flags.StringVar(&cfg.Output, "output", "", "new artifact directory outside the checkout (default: temporary directory)")
	flags.DurationVar(&cfg.Timeout, "timeout", 60*time.Second, "timeout per HTTP request or Git command")
	flags.Int64Var(&cfg.MaxBytes, "max-bytes", 64<<20, "maximum bytes per response, API list, Git output, or source snapshot")
	flags.BoolVar(&cfg.Snapshot, "snapshot", true, "prepare a sparse workspace of affected files when the patch applies")
	flags.StringVar(&reviewCfg.Reviewer, "reviewer", "none", "optional headless reviewer: none, codex, or claude")
	flags.StringVar(&reviewCfg.Model, "model", "", "reviewer model (default: selected CLI's default)")
	flags.DurationVar(&reviewCfg.Timeout, "review-timeout", 30*time.Minute, "timeout for the headless review")
	flags.StringVar(&manifestPath, "manifest", "", "reuse a saved preparation manifest instead of downloading a review")
	quiet := flags.BoolP("quiet", "q", false, "suppress progress messages (errors and results remain visible)")
	jsonOutput := flags.Bool("json", false, "emit the compact JSON manifest instead of the Markdown review")
	help := flags.BoolP("help", "h", false, "show usage")
	flags.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "Usage: freebsd-review-prepare [options] D12345 | 'PR #12345' | review/PR-URL")
		_, _ = fmt.Fprintln(stderr, "       freebsd-review-prepare --manifest PATH --reviewer codex|claude [options]")
		_, _ = fmt.Fprintln(stderr, "Prints Markdown for headless reviews, or a JSON manifest with --json or preparation-only runs.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *help {
		flags.Usage()
		return 0
	}
	if manifestPath == "" && flags.NArg() == 0 {
		flags.Usage()
		return 2
	}
	if reviewCfg.Reviewer != "none" {
		reviewCfg.IOTimeout, reviewCfg.MaxBytes = cfg.Timeout, cfg.MaxBytes
		if err := reviewCfg.Validate(); err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
	} else if reviewCfg.Model != "" || flags.Changed("review-timeout") || manifestPath != "" {
		var given []string
		if reviewCfg.Model != "" {
			given = append(given, "--model")
		}
		if flags.Changed("review-timeout") {
			given = append(given, "--review-timeout")
		}
		if manifestPath != "" {
			given = append(given, "--manifest")
		}
		_, _ = fmt.Fprintf(stderr, "%s require --reviewer codex or claude\n", strings.Join(given, ", "))
		return 2
	}
	if manifestPath != "" && (flags.NArg() != 0 || flags.Changed("repo") || flags.Changed("output") || flags.Changed("snapshot")) {
		_, _ = fmt.Fprintln(stderr, "--manifest cannot be combined with an identifier, --repo, --output, or --snapshot")
		return 2
	}
	progress := func(message string) {
		_, _ = fmt.Fprintln(stderr, "freebsd-review-prepare:", message)
	}
	if !*quiet {
		cfg.Progress, reviewCfg.Progress = progress, progress
	}
	cfg.Identifier = strings.Join(flags.Args(), " ")
	cfg.GitHubToken = os.Getenv("GITHUB_TOKEN")
	if cfg.GitHubToken == "" {
		cfg.GitHubToken = os.Getenv("GH_TOKEN")
	}
	var manifest *prepare.Manifest
	var err error
	if manifestPath != "" {
		if cfg.Progress != nil {
			cfg.Progress("Loading prepared manifest: " + manifestPath)
		}
		manifest, err = prepare.LoadManifest(manifestPath, cfg.MaxBytes)
	} else {
		manifest, err = prepare.Run(ctx, cfg)
	}
	if err == nil && reviewCfg.Reviewer != "none" {
		err = review.Run(ctx, reviewCfg, manifest)
	}
	if manifest != nil {
		if err == nil && cfg.Progress != nil {
			cfg.Progress("Manifest: " + manifest.ManifestPath)
		}
		if outputErr := writeResult(stdout, manifest, *jsonOutput || reviewCfg.Reviewer == "none"); outputErr != nil {
			_, _ = fmt.Fprintln(stderr, outputErr)
			return 1
		}
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "freebsd-review-prepare:", err)
		if manifest != nil {
			_, _ = fmt.Fprintln(stderr, "Artifacts:", manifest.ManifestPath)
			if manifest.Analysis != nil {
				_, _ = fmt.Fprintln(stderr, "Reviewer diagnostics:", manifest.Analysis.Diagnostics)
			}
		}
		return 1
	}
	return 0
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
