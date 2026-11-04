//go:build ignore

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type download struct {
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Filename string `json:"filename"`
	URL      string `json:"url"`
	SHA256   string `json:"sha256,omitempty"`
	Size     int64  `json:"size,omitempty"`
}

type manifest struct {
	Version   string     `json:"version"`
	Notes     string     `json:"notes,omitempty"`
	Downloads []download `json:"downloads"`
}

func main() {
	version := flag.String("version", "", "release tag, e.g. v0.5.6")
	dist := flag.String("dist", "dist", "dist directory")
	repo := flag.String("repo", "ChanceAroundYou/mindfs", "GitHub repo owner/name")
	notesFile := flag.String("notes", "", "path to release notes text")
	apkName := flag.String("apk", "", "APK filename (default mindfs_<version>_android.apk)")
	flag.Parse()

	if *version == "" {
		fmt.Fprintln(os.Stderr, "-version is required")
		os.Exit(2)
	}
	tag := strings.TrimPrefix(*version, "v")
	if *apkName == "" {
		*apkName = "mindfs_v" + tag + "_android.apk"
	}
	apkPath := filepath.Join(*dist, *apkName)
	info, err := os.Stat(apkPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "APK not found: %s\n", apkPath)
		os.Exit(1)
	}

	notes := ""
	if *notesFile != "" {
		b, err := os.ReadFile(*notesFile)
		if err == nil {
			notes = strings.TrimSpace(string(b))
		}
	}

	m := manifest{
		Version: *version,
		Notes:   notes,
		Downloads: []download{{
			OS:       "android",
			Arch:     "universal",
			Filename: *apkName,
			URL:      fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", *repo, *version, *apkName),
			Size:     info.Size(),
		}},
	}

	out := filepath.Join(*dist, "android-version.json")
	f, err := os.Create(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s (%d bytes)\n", out, info.Size())
}