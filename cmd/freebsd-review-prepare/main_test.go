package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCommandArguments(t *testing.T) {
	for _, item := range []struct {
		name string
		args []string
		want int
	}{
		{"help", []string{"--help"}, 0},
		{"missing identifier", nil, 2},
		{"unknown option", []string{"--nonexistent"}, 2},
		{"invalid identifier", []string{"--repo", ".", "not-a-review"}, 1},
		{"invalid timeout", []string{"--timeout=0", "D42"}, 1},
		{"invalid limit", []string{"--max-bytes=-1", "D42"}, 1},
		{"unknown reviewer", []string{"--reviewer=invalid", "D42"}, 1},
		{"model without reviewer", []string{"--model=example", "D42"}, 2},
		{"review timeout without reviewer", []string{"--review-timeout=1s", "D42"}, 2},
		{"manifest without reviewer", []string{"--manifest=/nonexistent"}, 2},
	} {
		t.Run(item.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(context.Background(), item.args, &stdout, &stderr); code != item.want {
				t.Fatalf("exit=%d, want=%d; stderr=%s", code, item.want, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatal("usage or preflight error polluted JSON stdout")
			}
			if item.name == "model without reviewer" && !strings.HasPrefix(stderr.String(), "--model require") {
				t.Fatalf("message does not name only the offending option: %s", stderr.String())
			}
			if item.name == "help" && !strings.Contains(stderr.String(), "--snapshot") {
				t.Fatal("missing option documentation")
			}
		})
	}
}
