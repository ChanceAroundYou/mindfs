package deploy

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// SafeArchiveTarget 把压缩包里的条目名解析成解包目录下的绝对路径。
//
// 这是 **zip-slip** 的唯一闸门：条目名可以是 `../../Windows/System32/x`，也可以是
// `C:\Windows\x`。两道判据都要有 —— 先按名字本身挡掉绝对路径与 `..`，再按「相对解包
// 目录的结果」挡一次（前者对付 `a/../../b` 这类清洗后才显形的写法）。
func SafeArchiveTarget(root, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("empty archive entry name")
	}
	if filepath.IsAbs(name) {
		return "", fmt.Errorf("unsafe absolute archive entry: %s", name)
	}
	clean := filepath.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe archive entry path: %s", name)
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target := filepath.Join(rootAbs, clean)
	rel, err := filepath.Rel(rootAbs, target)
	if err != nil {
		return "", err
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("archive entry escapes destination: %s", name)
	}
	return target, nil
}

// WriteZipReader 把 srcDir 下的内容按 rel 前缀写进一个 zip 字节流。
//
// 只收 io.Writer 而不是 io.WriterAt：载荷整体最终会被追加到 exe 尾部，
// 大小必须事先知道，所以调用方拿到的是完整字节。
//
// 条目名一律用 `/`（zip 规范），不随构建机平台走 filepath.Separator ——
// 用 `\` 写出来的 zip 在 Windows 资源管理器里能开，在 Go 的 zip 解析里是
// 「一个名字里带反斜杠的文件」，解包后变成文件名而不是目录。
func WriteZipDir(w io.Writer, srcDir, prefix string) error {
	zw := zip.NewWriter(w)
	root := filepath.Clean(srcDir)
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		name := path
		if prefix != "" {
			name = strings.Trim(prefix, "/") + "/" + rel
		}
		name = filepath.ToSlash(name)
		if d.IsDir() {
			_, err := zw.Create(name + "/")
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to package symlink: %s", rel)
		}
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
		hdr.SetMode(info.Mode().Perm())
		hdr.Modified = info.ModTime()
		entry, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(entry, f)
		return err
	})
	if walkErr != nil {
		return walkErr
	}
	return zw.Close()
}

// ExtractZipReader 把 zip 解到 dst，逐条目过 SafeArchiveTarget。
//
// 收 io.ReaderAt 而不是路径：自解包 exe 的载荷是**追加在自己的可执行文件尾部**的，
// 用 io.NewSectionReader 就能直接读，不必先落一份几十 MB 的临时文件。
// 符号链接条目一律拒绝 —— 它能把解包目录指到任何地方，而安装包不需要符号链接。
func ExtractZipReader(r io.ReaderAt, size int64, dst string) error {
	reader, err := zip.NewReader(r, size)
	if err != nil {
		return err
	}
	for _, file := range reader.File {
		if file.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsupported zip symlink entry: %s", file.Name)
		}
		target, err := SafeArchiveTarget(dst, file.Name)
		if err != nil {
			return err
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		src, err := file.Open()
		if err != nil {
			return err
		}
		dstFile, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, file.Mode())
		if err != nil {
			src.Close()
			return err
		}
		if _, err := io.Copy(dstFile, src); err != nil {
			dstFile.Close()
			src.Close()
			return err
		}
		if err := dstFile.Close(); err != nil {
			src.Close()
			return err
		}
		if err := src.Close(); err != nil {
			return err
		}
	}
	return nil
}
