package usecase

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdmime "mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mindfs/server/internal/apperr"
	"mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

type UploadFile struct {
	Name        string
	ContentType string
	Reader      io.Reader
}

type SaveUploadedFilesInput struct {
	RootID string
	Dir    string
	Files  []UploadFile
}

type UploadedFile struct {
	Path      string `json:"path"`
	AgentPath string `json:"agent_path,omitempty"`
	Name      string `json:"name"`
	Mime      string `json:"mime"`
	Size      int64  `json:"size"`
}

type SaveUploadedFilesOutput struct {
	Files []UploadedFile
}

func (s *Service) SaveUploadedFiles(_ context.Context, in SaveUploadedFilesInput) (SaveUploadedFilesOutput, error) {
	if err := s.ensureRegistry(); err != nil {
		return SaveUploadedFilesOutput{}, err
	}
	root, err := s.Registry.GetRoot(in.RootID)
	if err != nil {
		return SaveUploadedFilesOutput{}, err
	}
	if len(in.Files) == 0 {
		return SaveUploadedFilesOutput{}, errors.New("files required")
	}

	destDir, destAbs, err := resolveUploadDir(root, in.Dir)
	if err != nil {
		return SaveUploadedFilesOutput{}, err
	}
	if err := os.MkdirAll(destAbs, 0o755); err != nil {
		return SaveUploadedFilesOutput{}, apperr.Wrap("mkdir", destAbs, err)
	}

	saved := make([]UploadedFile, 0, len(in.Files))
	for _, file := range in.Files {
		result, err := saveUploadFile(destDir, destAbs, file)
		if err != nil {
			return SaveUploadedFilesOutput{}, err
		}
		if root.EffectiveMetaLocation() == fs.MetaLocationHome && strings.HasPrefix(result.Path, ".mindfs/") {
			if agentPath, resolveErr := root.ResolvePath(result.Path); resolveErr == nil {
				result.AgentPath = agentPath
			}
		}
		saved = append(saved, result)
	}
	return SaveUploadedFilesOutput{Files: saved}, nil
}

func resolveUploadDir(root fs.RootInfo, dir string) (string, string, error) {
	destDir := strings.TrimSpace(dir)
	if destDir == "" {
		destDir = defaultUploadDir()
	} else {
		var err error
		destDir, err = root.NormalizePath(destDir)
		if err != nil {
			return "", "", err
		}
	}
	if err := root.ValidateRelativePath(destDir); err != nil {
		return "", "", err
	}
	destAbs, err := root.ResolvePath(destDir)
	if err != nil {
		return "", "", err
	}
	return destDir, destAbs, nil
}

func defaultUploadDir() string {
	return filepath.ToSlash(filepath.Join(".mindfs", "upload", time.Now().Format("2006-01-02")))
}

func saveUploadFile(destDir, destAbs string, file UploadFile) (UploadedFile, error) {
	if file.Reader == nil {
		return UploadedFile{}, errors.New("file reader required")
	}
	name := sanitizeUploadName(file.Name)
	if name == "" {
		return UploadedFile{}, errors.New("file name required")
	}

	for index := 0; ; index++ {
		candidateName := disambiguateUploadName(name, index)
		relPath := joinUploadPath(destDir, candidateName)
		absPath := filepath.Join(destAbs, candidateName)

		handle, err := os.OpenFile(absPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			return UploadedFile{}, apperr.Wrap("create", absPath, err)
		}

		size, copyErr := io.Copy(handle, file.Reader)
		closeErr := handle.Close()
		if copyErr != nil {
			_ = os.Remove(absPath)
			return UploadedFile{}, apperr.Wrap("write", absPath, copyErr)
		}
		if closeErr != nil {
			_ = os.Remove(absPath)
			return UploadedFile{}, apperr.Wrap("write", absPath, closeErr)
		}

		return UploadedFile{
			Path: relPath,
			Name: candidateName,
			Mime: uploadMimeType(candidateName, file.ContentType),
			Size: size,
		}, nil
	}
}

func sanitizeUploadName(name string) string {
	clean := strings.TrimSpace(name)
	if clean == "" {
		return ""
	}
	clean = filepath.Base(clean)
	if clean == "." || clean == string(filepath.Separator) {
		return ""
	}
	return clean
}

func disambiguateUploadName(name string, index int) string {
	if index <= 0 {
		return name
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	return fmt.Sprintf("%s (%d)%s", base, index, ext)
}

func joinUploadPath(dir, name string) string {
	if dir == "." || dir == "" {
		return name
	}
	return filepath.ToSlash(filepath.Join(dir, name))
}

func uploadMimeType(name, contentType string) string {
	if contentType != "" {
		if mediaType, _, err := stdmime.ParseMediaType(contentType); err == nil && mediaType != "" {
			return mediaType
		}
	}
	if mimeType := stdmime.TypeByExtension(strings.ToLower(filepath.Ext(name))); mimeType != "" {
		return mimeType
	}
	return "application/octet-stream"
}

func fillFileMetaSessionInfo(ctx context.Context, s *Service, rootID string, meta []fs.FileMetaEntry) []fs.FileMetaEntry {
	if len(meta) == 0 {
		return meta
	}
	manager, err := s.Registry.GetSessionManager(rootID)
	if err != nil || manager == nil {
		return meta
	}
	for i := range meta {
		if meta[i].SourceSession == "" {
			continue
		}
		if meta[i].SessionName != "" && meta[i].Agent != "" {
			continue
		}
		sess, err := manager.Get(ctx, meta[i].SourceSession, 0)
		if err != nil || sess == nil {
			continue
		}
		if meta[i].SessionName == "" {
			meta[i].SessionName = sess.Name
		}
		if meta[i].Agent == "" {
			meta[i].Agent = session.InferAgentFromSession(sess)
		}
	}
	return meta
}

type GetFileMetaInput struct {
	RootID string
	Path   string
}

type GetFileMetaOutput struct {
	Meta []fs.FileMetaEntry
}

func (s *Service) GetFileMeta(_ context.Context, in GetFileMetaInput) (GetFileMetaOutput, error) {
	if err := s.ensureRegistry(); err != nil {
		return GetFileMetaOutput{}, err
	}
	root, err := s.Registry.GetRoot(in.RootID)
	if err != nil {
		return GetFileMetaOutput{}, err
	}
	if in.Path == "" {
		return GetFileMetaOutput{}, errors.New("path required")
	}
	path, err := root.NormalizePath(in.Path)
	if err != nil {
		return GetFileMetaOutput{}, err
	}
	meta, err := root.GetFileMeta(path)
	if err != nil {
		return GetFileMetaOutput{}, err
	}
	return GetFileMetaOutput{Meta: meta}, nil
}
