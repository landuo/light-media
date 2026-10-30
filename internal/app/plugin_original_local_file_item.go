package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"light-media/internal/plugins"
	"light-media/internal/transferextensions"
)

// Project a caller-authorized real local item through the fixed LocalStorage
// constructors. This helper neither creates folders nor grants access to a
// plugin-supplied path; each concrete operation retains its existing guard.
func originalLocalFileItem(path string) (*plugins.ModuleFileItem, error) {
	if !utf8.ValidString(path) {
		return nil, bad("INVALID_PATH", "本地文件路径编码无效")
	}
	if !importPath(path) {
		return nil, bad("INVALID_PATH", "本地文件路径必须规范且为绝对路径")
	}
	stat, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if stat.Mode()&os.ModeSymlink != 0 || !stat.Mode().IsRegular() && !stat.IsDir() {
		return nil, bad("INVALID_FILE", "只支持已核对的本地普通文件或目录")
	}
	return originalLocalFileItemFromStat(path, stat), nil
}

func originalLocalFileItemFromStat(path string, stat os.FileInfo) *plugins.ModuleFileItem {
	fields := map[string]any{}
	for _, key := range []string{"type", "name", "basename", "extension", "size", "modify_time", "fileid", "parent_fileid", "thumbnail", "pickcode", "drive_id", "url"} {
		fields[key] = nil
	}
	fields["path"], fields["storage"], fields["children"] = path, "local", []any{}
	name := filepath.Base(path)
	if path == "/" {
		name = ""
	}
	ext := *transferextensions.PathFile(path).Extension
	stem := name
	if ext != "" {
		stem = strings.TrimSuffix(name, "."+ext)
	}
	fields["type"], fields["name"], fields["basename"] = "file", name, stem
	fields["extension"], fields["size"] = ext, stat.Size()
	// Match stat().st_mtime without UnixNano's 1678–2262 overflow window.
	stamp := stat.ModTime()
	// CPython's stat float multiplies nanoseconds by 1e-9 before adding the
	// seconds. Division differs at subsecond boundaries; the explicit conversion
	// also preserves that rounded intermediate rather than allowing an FMA.
	fields["modify_time"] = float64(stamp.Unix()) + float64(float64(stamp.Nanosecond())*1e-9)
	if stat.IsDir() {
		fields["type"], fields["path"] = "dir", path+"/"
		fields["extension"], fields["size"] = nil, nil
	}
	encoded := &plugins.ModuleFileItem{Fields: map[string]json.RawMessage{}}
	for key, value := range fields {
		encoded.Fields[key], _ = json.Marshal(value)
	}
	return encoded
}
