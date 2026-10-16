package update

import (
	"time"
)

const (
	defaultCheckInterval = time.Hour
	releaseNotesPath     = "release-notes.md"
)

type latestRelease struct {
	TagName     string         `json:"tag_name"`
	Name        string         `json:"name"`
	Body        string         `json:"body"`
	HTMLURL     string         `json:"html_url"`
	PublishedAt time.Time      `json:"published_at"`
	Assets      []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type releaseManifest struct {
	Version   string             `json:"version"`
	Repo      string             `json:"repo,omitempty"`
	Artifacts []manifestArtifact `json:"artifacts"`
}

type signedReleaseManifest struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

type manifestArtifact struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size,omitempty"`
}
