package fs

import (
	"errors"
	"io"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"time"
	"unicode/utf8"

	"mindfs/server/internal/apperr"
)

// Entry represents a filesystem entry for UI listings.
type Entry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	IsDir     bool   `json:"is_dir"`
	IsSymlink bool   `json:"is_symlink,omitempty"`
	Size      int64  `json:"size"`
	MTime     string `json:"mtime"`
}

func (r RootInfo) ListEntries(dirRelPath string) ([]Entry, error) {
	dirRelPath, err := r.NormalizePath(dirRelPath)
	if err != nil {
		return nil, err
	}
	dirAbs, err := r.ResolvePath(dirRelPath)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dirAbs)
	if err != nil {
		return nil, apperr.Wrap("list", dirAbs, err)
	}
	result := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		absPath := filepath.Join(dirAbs, name)
		relPath := filepath.ToSlash(filepath.Join(dirRelPath, name))
		if dirRelPath == "." {
			relPath = filepath.ToSlash(name)
		}
		info, err := entry.Info()
		if err != nil {
			// Some system directories contain permission-gated or special entries.
			// Skip those children so one unreadable item doesn't blank the whole view.
			continue
		}
		isDir := entry.IsDir()
		isSymlink := info.Mode()&os.ModeSymlink != 0
		if isSymlink {
			if targetInfo, err := os.Stat(absPath); err == nil {
				info = targetInfo
				isDir = targetInfo.IsDir()
			}
		}
		result = append(result, Entry{
			Name:      name,
			Path:      relPath,
			IsDir:     isDir,
			IsSymlink: isSymlink,
			Size:      info.Size(),
			MTime:     info.ModTime().UTC().Format(time.RFC3339Nano),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].IsDir != result[j].IsDir {
			return result[i].IsDir
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}

type ReadResult struct {
	Revision   string          `json:"revision,omitempty"`
	Path       string          `json:"path"`
	Name       string          `json:"name"`
	Content    string          `json:"content"`
	Encoding   string          `json:"encoding"`
	Truncated  bool            `json:"truncated"`
	NextCursor int64           `json:"next_cursor"`
	Size       int64           `json:"size"`
	Ext        string          `json:"ext"`
	Mime       string          `json:"mime"`
	MTime      time.Time       `json:"mtime"`
	Root       string          `json:"root,omitempty"`
	FileMeta   []FileMetaEntry `json:"file_meta,omitempty"`
}

func (r RootInfo) ReadFile(pathRel string, maxBytes int64, cursor int64, readMode string) (ReadResult, error) {
	if cursor < 0 {
		return ReadResult{}, errors.New("cursor must be non-negative")
	}
	if readMode == "" {
		readMode = "incremental"
	}
	if readMode != "full" && readMode != "incremental" {
		return ReadResult{}, errors.New("invalid read mode")
	}
	if readMode == "incremental" && maxBytes <= 0 {
		maxBytes = defaultMaxReadBytes
	}
	pathRel, err := r.NormalizePath(pathRel)
	if err != nil {
		return ReadResult{}, err
	}
	resolved, err := r.ResolvePath(pathRel)
	if err != nil {
		return ReadResult{}, err
	}
	relPath := filepath.ToSlash(pathRel)
	info, err := os.Stat(resolved)
	if err != nil {
		return ReadResult{}, apperr.Wrap("stat", resolved, err)
	}
	if info.IsDir() {
		return ReadResult{}, errors.New("path is a directory")
	}
	file, err := os.Open(resolved)
	if err != nil {
		return ReadResult{}, apperr.Wrap("open", resolved, err)
	}
	defer file.Close()
	ext := filepath.Ext(resolved)
	bom := readFileBOM(file)
	readOffset := cursor
	if cursor > 0 {
		readOffset = alignReadOffsetForDecoding(file, info, ext, cursor, bom)
		if _, err := file.Seek(readOffset, io.SeekStart); err != nil {
			return ReadResult{}, apperr.Wrap("read", resolved, err)
		}
	}

	var (
		buf []byte
		n   int
	)
	if readMode == "full" {
		if info.Size() > maxFullReadBytes {
			return ReadResult{}, errors.New("file is too large for full read")
		}
		buf, err = io.ReadAll(io.LimitReader(file, maxFullReadBytes+1))
		if err != nil {
			return ReadResult{}, apperr.Wrap("read", resolved, err)
		}
		if len(buf) > maxFullReadBytes {
			return ReadResult{}, errors.New("file is too large for full read")
		}
		n = len(buf)
	} else {
		buf = make([]byte, maxBytes)
		n, err = io.ReadFull(file, buf)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return ReadResult{}, apperr.Wrap("read", resolved, err)
		}
		buf = buf[:n]
	}
	buf = trimTailForDecoding(buf, ext, bom)
	n = len(buf)
	truncated := readMode != "full" && readOffset+int64(n) < info.Size()
	encoding := "utf-8"
	content := string(buf)
	if !utf8.Valid(buf) {
		if decoded, detectedEncoding, ok := TryDecodeText(buf, ext); ok {
			encoding = detectedEncoding
			content = decoded
		} else {
			encoding = "binary"
			content = ""
		}
	}
	mimeType := mime.TypeByExtension(ext)
	return ReadResult{
		Path:       relPath,
		Name:       filepath.Base(resolved),
		Content:    content,
		Encoding:   encoding,
		Truncated:  truncated,
		NextCursor: readOffset + int64(n),
		Size:       info.Size(),
		Ext:        ext,
		Mime:       mimeType,
		MTime:      info.ModTime().UTC(),
	}, nil
}

func (r RootInfo) OpenFile(pathRel string) (*os.File, os.FileInfo, string, error) {
	pathRel, err := r.NormalizePath(pathRel)
	if err != nil {
		return nil, nil, "", err
	}
	resolved, err := r.ResolvePath(pathRel)
	if err != nil {
		return nil, nil, "", err
	}
	relPath := filepath.ToSlash(pathRel)
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, nil, "", apperr.Wrap("stat", resolved, err)
	}
	if info.IsDir() {
		return nil, nil, "", errors.New("path is a directory")
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, nil, "", apperr.Wrap("open", resolved, err)
	}
	return file, info, relPath, nil
}

func (r RootInfo) StatFile(pathRel string) (os.FileInfo, string, error) {
	pathRel, err := r.NormalizePath(pathRel)
	if err != nil {
		return nil, "", err
	}
	resolved, err := r.ResolvePath(pathRel)
	if err != nil {
		return nil, "", err
	}
	relPath := filepath.ToSlash(pathRel)
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, "", apperr.Wrap("stat", resolved, err)
	}
	if info.IsDir() {
		return nil, "", errors.New("path is a directory")
	}
	return info, relPath, nil
}
