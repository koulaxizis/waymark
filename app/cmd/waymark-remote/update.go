package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// releasesURL is the public GitHub API for the latest stable release. GitHub
// leaves pre-releases and drafts out of "latest", so only stable versions are
// offered. The request carries no identifier, only a generic User-Agent.
const releasesURL = "https://api.github.com/repos/koulaxizis/waymark/releases/latest"

// Update describes a newer stable version.
type Update struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	Notes   string `json:"notes"`
}

func (a *App) checkUpdate() {
	if u, ok := fetchUpdate(Version); ok {
		a.emit("update", u)
	}
}

// CheckUpdateNow is the "check now" button in the settings.
func (a *App) CheckUpdateNow() (*Update, error) {
	u, ok := fetchUpdate(Version)
	if !ok {
		return nil, nil
	}
	return &u, nil
}

func fetchUpdate(current string) (Update, bool) {
	cl := &http.Client{Timeout: 6 * time.Second}
	req, _ := http.NewRequest("GET", releasesURL, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Waymark")
	resp, err := cl.Do(req)
	if err != nil {
		return Update{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return Update{}, false
	}
	var r struct {
		Tag        string `json:"tag_name"`
		URL        string `json:"html_url"`
		Body       string `json:"body"`
		Prerelease bool   `json:"prerelease"`
		Draft      bool   `json:"draft"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil || r.Prerelease || r.Draft {
		return Update{}, false
	}
	if !newer(r.Tag, current) || !strings.HasPrefix(r.URL, "https://github.com/koulaxizis/waymark/") {
		return Update{}, false
	}
	notes := r.Body
	if len(notes) > 600 {
		notes = notes[:600] + "…"
	}
	return Update{Version: strings.TrimPrefix(r.Tag, "v"), URL: r.URL, Notes: notes}, true
}

// newer compares dotted versions like v0.2.1 > 0.2.0. A suffix such as
// "-dev" counts as older than the plain version.
func newer(tag, cur string) bool {
	a, as := parseVer(tag)
	b, bs := parseVer(cur)
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return as == "" && bs != ""
}

func parseVer(s string) ([3]int, string) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	suffix := ""
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s, suffix = s[:i], s[i:]
	}
	var v [3]int
	for i, p := range strings.SplitN(s, ".", 3) {
		v[i], _ = strconv.Atoi(p)
	}
	return v, suffix
}
