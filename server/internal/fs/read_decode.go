package fs

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func alignReadOffsetForDecoding(file *os.File, info os.FileInfo, ext string, offset int64, bom string) int64 {
	if offset <= 0 {
		return 0
	}
	if !isTextLikeExt(ext) {
		return offset
	}
	if offset >= info.Size() {
		return info.Size()
	}
	aligned := offset
	switch bom {
	case "utf16le", "utf16be":
		base := int64(2)
		if aligned < base {
			aligned = base
		}
		if (aligned-base)%2 != 0 {
			aligned--
		}
		if aligned < base {
			aligned = base
		}
		return aligned
	case "utf8bom":
		if aligned < 3 {
			aligned = 3
		}
	}
	// UTF-8 safety: if starts from a continuation byte, advance to next rune start.
	// This avoids malformed prefix when cursor lands in the middle of a multibyte rune.
	var probe [4]byte
	n, err := file.ReadAt(probe[:], aligned)
	if err != nil && err != io.EOF {
		return aligned
	}
	for i := 0; i < n; i++ {
		if !isUTF8ContinuationByte(probe[i]) {
			aligned += int64(i)
			break
		}
	}
	return alignReadOffsetForGB18030(file, info.Size(), aligned)
}

func isTextLikeExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".txt", ".text", ".md", ".markdown", ".mdx", ".rst", ".adoc", ".log",
		".json", ".jsonc", ".json5", ".yaml", ".yml", ".toml", ".ini", ".conf", ".config", ".env",
		".csv", ".tsv", ".xml", ".html", ".htm", ".css", ".scss", ".sass", ".less",
		".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".mts", ".cts",
		".go", ".rs", ".java", ".kt", ".kts", ".scala", ".swift",
		".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh", ".m", ".mm",
		".cs", ".php", ".rb", ".py", ".pyw", ".pl", ".pm", ".r", ".lua",
		".sh", ".bash", ".zsh", ".fish", ".ps1", ".bat", ".cmd",
		".sql", ".graphql", ".gql", ".proto", ".dockerfile", ".gradle":
		return true
	default:
		return false
	}
}

func readFileBOM(file *os.File) string {
	var hdr [3]byte
	n, err := file.ReadAt(hdr[:], 0)
	if err != nil && err != io.EOF {
		return ""
	}
	if n >= 2 && hdr[0] == 0xFF && hdr[1] == 0xFE {
		return "utf16le"
	}
	if n >= 2 && hdr[0] == 0xFE && hdr[1] == 0xFF {
		return "utf16be"
	}
	if n >= 3 && hdr[0] == 0xEF && hdr[1] == 0xBB && hdr[2] == 0xBF {
		return "utf8bom"
	}
	return ""
}

func isUTF8ContinuationByte(b byte) bool {
	return b&0xC0 == 0x80
}

func alignReadOffsetForGB18030(file *os.File, fileSize int64, offset int64) int64 {
	if offset >= fileSize {
		return fileSize
	}
	// If offset lands in the middle of a GB18030 rune, advance to next valid start.
	// Max rune width is 4 bytes, so scanning forward up to 3 bytes is sufficient.
	for shift := int64(0); shift <= 3; shift++ {
		candidate := offset + shift
		if candidate >= fileSize {
			return fileSize
		}
		var probe [4]byte
		n, err := file.ReadAt(probe[:], candidate)
		if err != nil && err != io.EOF {
			return offset
		}
		if n <= 0 {
			return candidate
		}
		runeLen, incomplete, valid := parseGB18030Rune(probe[:n])
		if valid && runeLen > 0 {
			return candidate
		}
		if incomplete {
			// Near EOF, treat as aligned to avoid over-shifting.
			return candidate
		}
	}
	return offset
}

func trimTailForGB18030(buf []byte) []byte {
	i := 0
	for i < len(buf) {
		runeLen, incomplete, valid := parseGB18030Rune(buf[i:])
		if incomplete {
			return buf[:i]
		}
		if !valid || runeLen <= 0 {
			// Not a clean GB18030 stream; leave buffer unchanged.
			return buf
		}
		i += runeLen
	}
	return buf
}

func parseGB18030Rune(buf []byte) (runeLen int, incomplete bool, valid bool) {
	if len(buf) == 0 {
		return 0, true, false
	}
	b0 := buf[0]
	if b0 <= 0x7F {
		return 1, false, true
	}
	if b0 < 0x81 || b0 > 0xFE {
		return 0, false, false
	}
	if len(buf) < 2 {
		return 0, true, false
	}
	b1 := buf[1]
	// Four-byte sequence: 81-FE 30-39 81-FE 30-39
	if b1 >= 0x30 && b1 <= 0x39 {
		if len(buf) < 4 {
			return 0, true, false
		}
		b2 := buf[2]
		b3 := buf[3]
		if b2 >= 0x81 && b2 <= 0xFE && b3 >= 0x30 && b3 <= 0x39 {
			return 4, false, true
		}
		return 0, false, false
	}
	// Two-byte sequence: 81-FE 40-FE (excluding 7F)
	if b1 >= 0x40 && b1 <= 0xFE && b1 != 0x7F {
		return 2, false, true
	}
	return 0, false, false
}

func trimTailForDecoding(buf []byte, ext string, bom string) []byte {
	if len(buf) == 0 || !isTextLikeExt(ext) {
		return buf
	}
	// UTF-16 files: enforce 2-byte boundary at tail.
	if bom == "utf16le" || bom == "utf16be" {
		if len(buf)%2 == 1 {
			return buf[:len(buf)-1]
		}
		return buf
	}
	// If already valid UTF-8, no tail trim is needed.
	if utf8.Valid(buf) {
		return buf
	}
	// GB18030 tail safety: trim only when the tail is an incomplete rune.
	if trimmed := trimTailForGB18030(buf); len(trimmed) != len(buf) {
		return trimmed
	}
	// UTF-8 tail safety: remove incomplete trailing rune bytes.
	// Try up to 4 bytes (max UTF-8 rune length) for a valid prefix.
	for cut := 1; cut <= 4 && cut < len(buf); cut++ {
		candidate := buf[:len(buf)-cut]
		if utf8.Valid(candidate) {
			return candidate
		}
	}
	return buf
}

func TryDecodeText(buf []byte, ext string) (string, string, bool) {
	// Only attempt legacy decoding for common text file types.
	if !isTextLikeExt(ext) {
		return "", "", false
	}

	if decoded, ok := decodeUTF16(buf); ok {
		return decoded, "utf-16", true
	}

	decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(buf)
	if err != nil {
		return "", "", false
	}
	decoded = bytes.Trim(decoded, "\x00")
	if len(decoded) == 0 {
		return "", "", false
	}
	if !utf8.Valid(decoded) {
		return "", "", false
	}
	return string(decoded), "gb18030", true
}

func decodeUTF16(buf []byte) (string, bool) {
	if len(buf) < 2 {
		return "", false
	}
	// BOM detection
	if buf[0] == 0xFF && buf[1] == 0xFE {
		return decodeUTF16Endian(buf[2:], binary.LittleEndian)
	}
	if buf[0] == 0xFE && buf[1] == 0xFF {
		return decodeUTF16Endian(buf[2:], binary.BigEndian)
	}
	// Heuristic detection when BOM is missing.
	var evenZero, oddZero int
	limit := len(buf)
	if limit > 4096 {
		limit = 4096
	}
	for i := 0; i < limit; i++ {
		if buf[i] != 0 {
			continue
		}
		if i%2 == 0 {
			evenZero++
		} else {
			oddZero++
		}
	}
	// ASCII-like UTF-16LE usually has many zero bytes at odd positions.
	if oddZero > evenZero*4 && oddZero > 32 {
		if s, ok := decodeUTF16Endian(buf, binary.LittleEndian); ok {
			return s, true
		}
	}
	// ASCII-like UTF-16BE usually has many zero bytes at even positions.
	if evenZero > oddZero*4 && evenZero > 32 {
		if s, ok := decodeUTF16Endian(buf, binary.BigEndian); ok {
			return s, true
		}
	}
	return "", false
}

func decodeUTF16Endian(buf []byte, order binary.ByteOrder) (string, bool) {
	if len(buf) < 2 {
		return "", false
	}
	if len(buf)%2 == 1 {
		buf = buf[:len(buf)-1]
	}
	u16 := make([]uint16, 0, len(buf)/2)
	for i := 0; i+1 < len(buf); i += 2 {
		u16 = append(u16, order.Uint16(buf[i:i+2]))
	}
	if len(u16) == 0 {
		return "", false
	}
	s := string(utf16.Decode(u16))
	s = strings.Trim(s, "\x00")
	if s == "" {
		return "", false
	}
	return s, true
}
