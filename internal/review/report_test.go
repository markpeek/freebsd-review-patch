package review

import (
	"encoding/json"
	"testing"
)

func TestFindingPatchSides(t *testing.T) {
	deleted := "diff --git a/file.go b/file.go\ndeleted file mode 100644\n--- a/file.go\n+++ /dev/null\n@@ -1 +0,0 @@\n-removed\n"
	added := "diff --git a/file.go b/file.go\nnew file mode 100644\n--- /dev/null\n+++ b/file.go\n@@ -0,0 +1 @@\n+added\n"
	quoted := "diff --git a/file.go b/file.go\n--- \"a/file.go\"\n+++ \"b/file.go\"\n@@ -1 +1 @@\n-before\n+after\n"
	rename := "diff --git a/old.go b/file.go\nsimilarity index 100%\nrename from old.go\nrename to file.go\n"
	for _, tc := range []struct {
		name, patch, side string
		line              int
		valid             bool
	}{
		{"deleted old side", deleted, "old", 1, true},
		{"deleted new side", deleted, "new", 1, false},
		{"deleted nonexistent line", deleted, "new", 99999, false},
		{"added new side", added, "new", 1, true},
		{"added old side", added, "old", 1, false},
		{"added nonexistent line", added, "old", 99999, false},
		{"quoted valid line", quoted, "new", 1, true},
		{"quoted invalid line", quoted, "new", 99999, false},
		{"rename without hunks", rename, "new", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := preparedFixture(t)
			writeTestFile(t, m.Patch, tc.patch)
			var report Report
			if err := json.Unmarshal([]byte(validReport), &report); err != nil {
				t.Fatal(err)
			}
			report.Findings[0].Side = tc.side
			report.Findings[0].StartLine, report.Findings[0].EndLine = tc.line, tc.line
			data, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			_, err = parseReport(data, m, 1<<20)
			if (err == nil) != tc.valid {
				t.Fatalf("finding acceptance: want %t, error: %v", tc.valid, err)
			}
		})
	}
}
