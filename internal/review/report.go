// Package review runs optional headless code reviews of prepared artifacts.
package review

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/markpeek/freebsd-review-patch/internal/prepare"
)

//go:embed schema.json
var schema string

type Finding struct {
	Priority    string `json:"priority"`
	Title       string `json:"title"`
	Path        string `json:"path"`
	StartLine   int    `json:"start_line"`
	EndLine     int    `json:"end_line"`
	Side        string `json:"side"`
	Description string `json:"description"`
	Evidence    string `json:"evidence"`
	Suggestion  string `json:"suggestion"`
}

type Report struct {
	Summary     string    `json:"summary"`
	Findings    []Finding `json:"findings"`
	Validation  []string  `json:"validation"`
	Limitations []string  `json:"limitations"`
}

func readFile(name string, limit int64) ([]byte, error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("review artifact exceeds max-bytes")
	}
	return data, nil
}

func requiredObject(data []byte, keys ...string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if len(fields) != len(keys) {
		return errors.New("review output has missing or unexpected fields")
	}
	for _, key := range keys {
		value, exists := fields[key]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("missing review field %s", key)
		}
	}
	return nil
}

func parseReport(data []byte, m *prepare.Manifest, maxBytes int64) (*Report, error) {
	if err := requiredObject(data, "summary", "findings", "validation", "limitations"); err != nil {
		return nil, err
	}
	var report Report
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return nil, fmt.Errorf("invalid findings JSON: %w", err)
	}
	if strings.TrimSpace(report.Summary) == "" || report.Findings == nil || report.Validation == nil || report.Limitations == nil {
		return nil, errors.New("review output requires a summary and non-null findings, validation, and limitations arrays")
	}
	filesJSON, err := readFile(m.Files, maxBytes)
	if err != nil {
		return nil, err
	}
	var files []struct {
		Path    string `json:"path"`
		OldPath string `json:"old_path"`
	}
	if err := json.Unmarshal(filesJSON, &files); err != nil {
		return nil, fmt.Errorf("read changed-file list: %w", err)
	}
	if len(files) != m.FileCount || len(files) == 0 {
		return nil, errors.New("changed-file list does not match the manifest")
	}
	changed := make(map[string]bool)
	for _, file := range files {
		changed[file.Path] = true
		if file.OldPath != "" {
			changed[file.OldPath] = true
		}
	}
	var raw struct {
		Findings []json.RawMessage `json:"findings"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	patch, err := readFile(m.Patch, maxBytes)
	if err != nil {
		return nil, err
	}
	hunks := parseHunks(string(patch), m.Strip)
	for i, finding := range report.Findings {
		if err := requiredObject(raw.Findings[i], "priority", "title", "path", "start_line", "end_line", "side", "description", "evidence", "suggestion"); err != nil {
			return nil, err
		}
		if finding.Priority != "P0" && finding.Priority != "P1" && finding.Priority != "P2" && finding.Priority != "P3" {
			return nil, errors.New("invalid finding priority")
		}
		if !changed[finding.Path] || path.IsAbs(finding.Path) || path.Clean(finding.Path) != finding.Path || strings.HasPrefix(finding.Path, "../") {
			return nil, errors.New("finding must reference a changed repository path")
		}
		if finding.StartLine < 1 || finding.EndLine < finding.StartLine || (finding.Side != "new" && finding.Side != "old") {
			return nil, errors.New("finding has an invalid line range or patch side")
		}
		if !inHunk(hunks[finding.Side][finding.Path], finding.StartLine, finding.EndLine) {
			return nil, fmt.Errorf("finding %s:%d-%d is outside the patch hunks on the %s side", finding.Path, finding.StartLine, finding.EndLine, finding.Side)
		}
		if strings.TrimSpace(finding.Title) == "" || strings.TrimSpace(finding.Description) == "" || strings.TrimSpace(finding.Evidence) == "" {
			return nil, errors.New("finding requires a title, description, and evidence")
		}
	}
	for _, list := range [][]string{report.Validation, report.Limitations} {
		for _, item := range list {
			if strings.TrimSpace(item) == "" {
				return nil, errors.New("validation and limitation entries must not be empty")
			}
		}
	}
	rank := map[string]int{"P0": 0, "P1": 1, "P2": 2, "P3": 3}
	sort.SliceStable(report.Findings, func(i, j int) bool {
		return rank[report.Findings[i].Priority] < rank[report.Findings[j].Priority]
	})
	return &report, nil
}

// lineRange is an inclusive range of lines on one side of a patch.
type lineRange struct{ first, last int }

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// parseHunks maps side ("old" or "new") and repository path to the line
// ranges the patch covers. Missing sides and files without textual hunks have
// no ranges, so findings cannot reference lines on them.
func parseHunks(patch string, strip int) map[string]map[string][]lineRange {
	out := map[string]map[string][]lineRange{"old": {}, "new": {}}
	var oldPath, newPath string
	var remainOld, remainNew int
	name := func(header string) string {
		header, _, _ = strings.Cut(header, "\t")
		if strings.HasPrefix(header, `"`) {
			var err error
			header, err = strconv.Unquote(header)
			if err != nil {
				return ""
			}
		}
		if header == "/dev/null" {
			return ""
		}
		for i := 0; i < strip; i++ {
			_, rest, ok := strings.Cut(header, "/")
			if !ok {
				return ""
			}
			header = rest
		}
		return header
	}
	for _, line := range strings.Split(patch, "\n") {
		if remainOld > 0 || remainNew > 0 {
			switch {
			case strings.HasPrefix(line, "-"):
				remainOld--
			case strings.HasPrefix(line, "+"):
				remainNew--
			case strings.HasPrefix(line, `\`):
			default:
				remainOld--
				remainNew--
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "diff --git "):
			oldPath, newPath = "", ""
		case strings.HasPrefix(line, "--- "):
			oldPath = name(strings.TrimPrefix(line, "--- "))
		case strings.HasPrefix(line, "+++ "):
			newPath = name(strings.TrimPrefix(line, "+++ "))
		default:
			match := hunkHeader.FindStringSubmatch(line)
			if match == nil {
				continue
			}
			num := func(s string, def int) int {
				if s == "" {
					return def
				}
				n, _ := strconv.Atoi(s)
				return n
			}
			oldStart, oldCount := num(match[1], 0), num(match[2], 1)
			newStart, newCount := num(match[3], 0), num(match[4], 1)
			remainOld, remainNew = oldCount, newCount
			if oldPath != "" {
				out["old"][oldPath] = append(out["old"][oldPath], lineRange{oldStart, oldStart + oldCount - 1})
			}
			if newPath != "" {
				out["new"][newPath] = append(out["new"][newPath], lineRange{newStart, newStart + newCount - 1})
			}
		}
	}
	return out
}

// inHunk reports whether [start, end] lies within one hunk. Missing ranges
// cannot establish a valid finding location and are rejected.
func inHunk(ranges []lineRange, start, end int) bool {
	for _, r := range ranges {
		if start >= r.first && end <= r.last {
			return true
		}
	}
	return false
}

func renderReport(m *prepare.Manifest, report *Report) string {
	var out strings.Builder
	fmt.Fprintf(&out, "# FreeBSD code review\n\nReview: %s\n\n", m.Review.URL)
	if m.Review.DiffID != "" {
		fmt.Fprintf(&out, "Differential diff: %s\n\n", m.Review.DiffID)
	} else {
		fmt.Fprintf(&out, "PR base: %s\n\nPR head: %s\n\n", m.Review.Base, m.Review.Head)
	}
	fmt.Fprintf(&out, "Baseline: %s (%s, local changes: %t)\n\nPatch SHA-256: %s\n\nApplicability: %s\n\n", m.Baseline.Head, m.Baseline.Branch, m.Baseline.Dirty, m.SHA256, m.Applicability)
	out.WriteString(report.Summary + "\n\n")
	if len(report.Findings) == 0 {
		out.WriteString("No actionable defects were reported.\n\n")
	}
	for _, finding := range report.Findings {
		fmt.Fprintf(&out, "## [%s] %s\n\n%s:%d-%d (%s side)\n\n%s\n\nEvidence: %s\n\n", finding.Priority, finding.Title, finding.Path, finding.StartLine, finding.EndLine, finding.Side, finding.Description, finding.Evidence)
		if finding.Suggestion != "" {
			out.WriteString("Suggested correction: " + finding.Suggestion + "\n\n")
		}
	}
	for _, section := range []struct {
		name  string
		items []string
	}{{"Validation", report.Validation}, {"Limitations", report.Limitations}, {"Preparation warnings", m.Warnings}} {
		if len(section.items) > 0 {
			out.WriteString("## " + section.name + "\n\n")
			for _, item := range section.items {
				out.WriteString("- " + item + "\n")
			}
			out.WriteByte('\n')
		}
	}
	return out.String()
}
