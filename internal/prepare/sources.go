package prepare

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/net/html"
)

type pullRequest struct {
	Number       int    `json:"number"`
	HTMLURL      string `json:"html_url"`
	ChangedFiles int    `json:"changed_files"`
	Commits      int    `json:"commits"`
	Base         struct {
		SHA  string `json:"sha"`
		Repo struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"base"`
	Head struct {
		SHA string `json:"sha"`
	} `json:"head"`
}

type remoteFile struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
	Status           string `json:"status"`
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
}

const githubAPI = "https://api.github.com/repos/freebsd/freebsd-src"

func (r *runner) contextDirectory() error {
	r.m.Context = filepath.Join(r.m.Directory, "context")
	return os.Mkdir(r.m.Context, 0700)
}

func (r *runner) getPull(endpoint string) (pullRequest, []byte, error) {
	data, _, err := r.get(endpoint, "application/vnd.github+json")
	if err != nil {
		return pullRequest{}, nil, err
	}
	var pr pullRequest
	if err := decodeJSON(data, &pr); err != nil {
		return pr, nil, err
	}
	if numberString(pr.Number) != r.m.Review.ID || pr.HTMLURL != r.m.Review.URL || pr.Base.Repo.FullName != "freebsd/freebsd-src" || !sha.MatchString(pr.Base.SHA) || !sha.MatchString(pr.Head.SHA) || pr.ChangedFiles < 0 {
		return pr, nil, errors.New("GitHub metadata does not match the requested PR or contains invalid SHAs")
	}
	return pr, data, nil
}

// list follows pagination using numbered requests to a fixed, trusted endpoint.
// It never follows arbitrary next URLs supplied in an API response.
func (r *runner) list(endpoint string, maxPages int) ([]json.RawMessage, error) {
	all := make([]json.RawMessage, 0)
	var size int64
	for page := 1; page <= maxPages; page++ {
		data, headers, err := r.get(fmt.Sprintf("%s?per_page=100&page=%d", endpoint, page), "application/vnd.github+json")
		if err != nil {
			return nil, err
		}
		size += int64(len(data))
		if size > r.cfg.MaxBytes {
			return nil, fmt.Errorf("API list exceeds max-bytes: %s", endpoint)
		}
		var items []json.RawMessage
		if err := decodeJSON(data, &items); err != nil {
			return nil, err
		}
		if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
			return nil, errors.New("expected an API list, received null")
		}
		all = append(all, items...)
		if !strings.Contains(headers.Get("Link"), `rel="next"`) {
			return all, nil
		}
	}
	return nil, fmt.Errorf("API list exceeds %d pages: inspect %s manually", maxPages, endpoint)
}

func (r *runner) github() error {
	if err := r.contextDirectory(); err != nil {
		return err
	}
	endpoint := githubAPI + "/pulls/" + r.m.Review.ID
	var chosen pullRequest
	for attempt := 0; attempt < 3; attempt++ {
		before, metadata, err := r.getPull(endpoint)
		if err != nil {
			return err
		}
		if before.ChangedFiles > 3000 {
			return errors.New("PR exceeds GitHub's 3000-file metadata limit; reconstruct from commits manually")
		}
		// Save initial metadata even when subsequent retrieval fails.
		if err := os.WriteFile(filepath.Join(r.m.Context, "pull.json"), metadata, 0600); err != nil {
			return err
		}
		diff, _, err := r.get(endpoint, "application/vnd.github.diff")
		if err != nil {
			return err
		}
		items, err := r.list(endpoint+"/files", 30)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(items)
		if err != nil {
			return err
		}
		var files []remoteFile
		if err := decodeJSON(encoded, &files); err != nil {
			return err
		}
		after, _, err := r.getPull(endpoint)
		if err != nil {
			return err
		}
		if before.Base.SHA != after.Base.SHA || before.Head.SHA != after.Head.SHA || before.ChangedFiles != after.ChangedFiles {
			continue
		}
		if len(files) != before.ChangedFiles {
			return errors.New("PR file list is incomplete; reconstruct from commits manually")
		}
		r.m.Review.Base, r.m.Review.Head = before.Base.SHA, before.Head.SHA
		if err := writeJSON(filepath.Join(r.m.Context, "remote-files.json"), files); err != nil {
			return err
		}
		if err := r.savePatch(diff); err != nil {
			return err
		}
		r.expected, chosen = files, before
		break
	}
	if r.m.Patch == "" {
		return errors.New("PR changed during all three retrieval attempts; pin and retrieve it manually")
	}
	// Context retrieval is best effort. Missing context is explicitly listed in
	// the manifest, while a complete diff and file list are mandatory above.
	lists := []struct{ name, endpoint string }{
		{"comments.json", githubAPI + "/issues/" + r.m.Review.ID + "/comments"},
		{"reviews.json", endpoint + "/reviews"},
		{"inline-comments.json", endpoint + "/comments"},
		{"commits.json", endpoint + "/commits"},
	}
	for _, item := range lists {
		items, err := r.list(item.endpoint, 30)
		if err != nil {
			r.m.Warnings = append(r.m.Warnings, item.name+": "+err.Error())
			continue
		}
		if item.name == "commits.json" && len(items) != chosen.Commits {
			r.m.Warnings = append(r.m.Warnings, "commits.json is incomplete; inspect remaining commits manually")
		}
		if err := writeJSON(filepath.Join(r.m.Context, item.name), items); err != nil {
			return err
		}
	}
	for _, item := range []struct{ name, endpoint string }{
		{"checks.json", githubAPI + "/commits/" + chosen.Head.SHA + "/check-runs?per_page=100"},
		{"statuses.json", githubAPI + "/commits/" + chosen.Head.SHA + "/status?per_page=100"},
	} {
		data, headers, err := r.get(item.endpoint, "application/vnd.github+json")
		if err != nil {
			r.m.Warnings = append(r.m.Warnings, item.name+": "+err.Error())
			continue
		}
		if !json.Valid(data) {
			r.m.Warnings = append(r.m.Warnings, item.name+": invalid API JSON")
			continue
		}
		if strings.Contains(headers.Get("Link"), `rel="next"`) {
			r.m.Warnings = append(r.m.Warnings, item.name+": additional pages must be inspected manually")
		}
		if err := os.WriteFile(filepath.Join(r.m.Context, item.name), data, 0600); err != nil {
			return err
		}
	}
	return nil
}

func attr(n *html.Node, name string) (string, bool) {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val, true
		}
	}
	return "", false
}

func differentialPage(data []byte, id string) (string, string, error) {
	root, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return "", "", err
	}
	var selected, title string
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "input" {
			name, _ := attr(n, "name")
			value, _ := attr(n, "value")
			_, checked := attr(n, "checked")
			if name == "id" && checked && digits.MatchString(value) {
				selected = value
			}
		}
		if n.Type == html.ElementNode && n.Data == "title" && n.FirstChild != nil {
			title = n.FirstChild.Data
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(root)
	if selected == "" || !strings.Contains(title, id+" ") {
		return "", "", errors.New("could not identify the selected Differential diff (possible challenge or changed page format)")
	}
	var text strings.Builder
	var extract func(*html.Node)
	extract = func(n *html.Node) {
		if n.Type == html.ElementNode {
			class, _ := attr(n, "class")
			if n.Data == "script" || n.Data == "style" || strings.Contains(class, "differential-review-stage") {
				return
			}
		}
		if n.Type == html.TextNode {
			if s := strings.TrimSpace(n.Data); s != "" {
				text.WriteString(s)
				text.WriteByte('\n')
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			extract(c)
		}
	}
	extract(root)
	return selected, text.String(), nil
}

func (r *runner) differential() error {
	if err := r.contextDirectory(); err != nil {
		return err
	}
	review := &r.m.Review
	pageURL := review.URL
	if review.DiffID != "" {
		pageURL += "?id=" + review.DiffID
	}
	page, _, err := r.get(pageURL, "text/html")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(r.m.Context, "review.html"), page, 0600); err != nil {
		return err
	}
	diffID, text, err := differentialPage(page, review.ID)
	if err != nil {
		return err
	}
	if review.DiffID != "" && diffID != review.DiffID {
		return errors.New("review page did not select the requested historical diff")
	}
	review.DiffID = diffID
	if err := os.WriteFile(filepath.Join(r.m.Context, "review.txt"), []byte(text), 0600); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(r.m.Context, "review.json"), review); err != nil {
		return err
	}
	query := url.Values{"id": {diffID}, "download": {"true"}}
	diff, _, err := r.get(review.URL+"?"+query.Encode(), "text/plain")
	if err != nil {
		return err
	}
	if err := r.savePatch(diff); err != nil {
		return err
	}
	// Verify the pinned selector still names the requested diff. The latest
	// revision may advance independently; this run continues to review diffID.
	page, _, err = r.get(review.URL+"?id="+diffID, "text/html")
	if err != nil {
		return err
	}
	verified, _, err := differentialPage(page, review.ID)
	if err != nil {
		return err
	}
	if verified != diffID {
		return errors.New("review server no longer serves the pinned diff")
	}
	r.m.Warnings = append(r.m.Warnings, "Differential HTML context may hide older or inline discussion; inspect the review page as needed")
	return nil
}
