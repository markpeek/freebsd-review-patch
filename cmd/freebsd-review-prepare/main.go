// freebsd-review-prepare fetches and checks a FreeBSD review without changing
// the source checkout. Review content is saved to files, not printed to stdout.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/markpeek/freebsd-review-patch/internal/prepare"
	"github.com/spf13/pflag"
)

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := pflag.NewFlagSet("freebsd-review-prepare", pflag.ContinueOnError)
	flags.SetOutput(stderr)
	var cfg prepare.Config
	flags.StringVar(&cfg.Repo, "repo", ".", "source checkout (left unchanged)")
	flags.StringVar(&cfg.Output, "output", "", "new artifact directory outside the checkout (default: temporary directory)")
	flags.DurationVar(&cfg.Timeout, "timeout", 60*time.Second, "timeout per HTTP request or Git command")
	flags.Int64Var(&cfg.MaxBytes, "max-bytes", 64<<20, "maximum bytes per response, API list, Git output, or source snapshot")
	flags.BoolVar(&cfg.Snapshot, "snapshot", true, "prepare a sparse workspace of affected files when the patch applies")
	help := flags.BoolP("help", "h", false, "show usage")
	flags.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "Usage: freebsd-review-prepare [options] D12345 | 'PR #12345' | review/PR-URL")
		_, _ = fmt.Fprintln(stderr, "Saves a manifest and review artifacts; emits compact JSON on stdout.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *help {
		flags.Usage()
		return 0
	}
	if flags.NArg() == 0 {
		flags.Usage()
		return 2
	}
	cfg.Identifier = strings.Join(flags.Args(), " ")
	cfg.GitHubToken = os.Getenv("GITHUB_TOKEN")
	if cfg.GitHubToken == "" {
		cfg.GitHubToken = os.Getenv("GH_TOKEN")
	}
	manifest, err := prepare.Run(ctx, cfg)
	if manifest != nil {
		if encodeErr := json.NewEncoder(stdout).Encode(manifest); encodeErr != nil {
			_, _ = fmt.Fprintln(stderr, encodeErr)
			return 1
		}
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "freebsd-review-prepare:", err)
		return 1
	}
	return 0
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
