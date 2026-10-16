package usecase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mindfs/server/internal/fs"
)

type ListTreeInput struct {
	RootID string
	Dir    string
}

type ListTreeOutput struct {
	Entries []fs.Entry
}

type OpenFileRawInput struct {
	RootID string
	Path   string
}

type OpenFileRawOutput struct {
	File    *os.File
	Info    os.FileInfo
	RelPath string
}

type GetFileInfoInput struct {
	RootID string
	Path   string
}

type GetFileInfoOutput struct {
	Path  string
	Name  string
	Size  int64
	MTime time.Time
}

func (s *Service) ListTree(_ context.Context, in ListTreeInput) (ListTreeOutput, error) {
	if err := s.ensureRegistry(); err != nil {
		return ListTreeOutput{}, err
	}
	root, err := s.Registry.GetRoot(in.RootID)
	if err != nil {
		return ListTreeOutput{}, err
	}
	dir := in.Dir
	if dir == "" || dir == "." {
		dir = "."
	} else {
		dir, err = root.NormalizePath(dir)
		if err != nil {
			return ListTreeOutput{}, err
		}
	}
	if err := root.ValidateRelativePath(dir); err != nil {
		return ListTreeOutput{}, err
	}
	s.ensureFileWatcher(in.RootID, dir)
	entries, err := root.ListEntries(dir)
	if err != nil {
		return ListTreeOutput{}, err
	}
	return ListTreeOutput{Entries: entries}, nil
}

func parentDir(path string) string {
	clean := strings.Trim(filepath.ToSlash(path), "/")
	if clean == "" || clean == "." {
		return "."
	}
	idx := strings.LastIndex(clean, "/")
	if idx <= 0 {
		return "."
	}
	return clean[:idx]
}

// resolveFileReadTarget keeps project paths (including virtual metadata) on the
// managed root and supports explicit external absolute paths for file reads only.
func resolveFileReadTarget(root fs.RootInfo, input string) (fs.RootInfo, string, bool, error) {
	path, err := root.NormalizePath(input)
	if err == nil {
		return root, path, false, nil
	}
	if !filepath.IsAbs(input) {
		return root, "", false, err
	}
	absolute := filepath.Clean(input)
	target := fs.NewRootInfo(root.ID, root.Name, filepath.Dir(absolute))
	return target, filepath.Base(absolute), true, nil
}

func (s *Service) OpenFileRaw(_ context.Context, in OpenFileRawInput) (OpenFileRawOutput, error) {
	if err := s.ensureRegistry(); err != nil {
		return OpenFileRawOutput{}, err
	}
	root, err := s.Registry.GetRoot(in.RootID)
	if err != nil {
		return OpenFileRawOutput{}, err
	}
	if in.Path == "" {
		return OpenFileRawOutput{}, errors.New("path required")
	}
	target, path, external, err := resolveFileReadTarget(root, in.Path)
	if err != nil {
		return OpenFileRawOutput{}, err
	}
	file, info, relPath, err := target.OpenFile(path)
	if err != nil {
		return OpenFileRawOutput{}, err
	}
	if external {
		relPath = filepath.ToSlash(filepath.Clean(in.Path))
	}
	return OpenFileRawOutput{File: file, Info: info, RelPath: relPath}, nil
}

func (s *Service) GetFileInfo(_ context.Context, in GetFileInfoInput) (GetFileInfoOutput, error) {
	if err := s.ensureRegistry(); err != nil {
		return GetFileInfoOutput{}, err
	}
	root, err := s.Registry.GetRoot(in.RootID)
	if err != nil {
		return GetFileInfoOutput{}, err
	}
	if in.Path == "" {
		return GetFileInfoOutput{}, errors.New("path required")
	}
	target, path, external, err := resolveFileReadTarget(root, in.Path)
	if err != nil {
		return GetFileInfoOutput{}, err
	}
	info, relPath, err := target.StatFile(path)
	if err != nil {
		return GetFileInfoOutput{}, err
	}
	if external {
		relPath = filepath.ToSlash(filepath.Clean(in.Path))
	}
	return GetFileInfoOutput{
		Path:  relPath,
		Name:  filepath.Base(relPath),
		Size:  info.Size(),
		MTime: info.ModTime().UTC(),
	}, nil
}

type ReadFileInput struct {
	RootID   string
	Path     string
	MaxBytes int64
	Cursor   int64
	ReadMode string
}

type ReadFileOutput struct {
	File fs.ReadResult
}

func (s *Service) ReadFile(ctx context.Context, in ReadFileInput) (ReadFileOutput, error) {
	if err := s.ensureRegistry(); err != nil {
		return ReadFileOutput{}, err
	}
	root, err := s.Registry.GetRoot(in.RootID)
	if err != nil {
		return ReadFileOutput{}, err
	}
	if in.Path == "" {
		return ReadFileOutput{}, errors.New("path required")
	}
	target, path, external, err := resolveFileReadTarget(root, in.Path)
	if err != nil {
		return ReadFileOutput{}, err
	}
	if !external {
		s.ensureFileWatcher(in.RootID, parentDir(path))
	}
	result, err := target.ReadFile(path, in.MaxBytes, in.Cursor, in.ReadMode)
	if err != nil {
		return ReadFileOutput{}, err
	}
	if external {
		result.Root = root.ID
		result.Path = filepath.ToSlash(filepath.Clean(in.Path))
		return ReadFileOutput{File: result}, nil
	}
	meta, err := root.GetFileMeta(path)
	if err != nil {
		return ReadFileOutput{}, err
	}
	meta = fillFileMetaSessionInfo(ctx, s, in.RootID, meta)
	result.Root = root.ID
	result.FileMeta = meta
	return ReadFileOutput{File: result}, nil
}
