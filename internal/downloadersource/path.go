package downloadersource

import "strings"

// POSIX Path.as_posix is lexical: it drops '.'/duplicate separators but retains
// '..'. Unlike Go path.Clean it must not resolve a downloader-returned '..'.
func PosixPath(s string) string {
	prefix := ""
	if strings.HasPrefix(s, "/") {
		prefix = "/"
		if strings.HasPrefix(s, "//") && !strings.HasPrefix(s, "///") {
			prefix = "//"
		}
	}
	parts := []string{}
	for _, p := range strings.Split(s, "/") {
		if p != "" && p != "." {
			parts = append(parts, p)
		}
	}
	out := prefix + strings.Join(parts, "/")
	if out == "" {
		return "."
	}
	return out
}
func JoinPath(a, b string) string {
	if a == "" {
		return PosixPath(b)
	}
	if strings.HasPrefix(b, "/") {
		return PosixPath(b)
	}
	return PosixPath(a + "/" + b)
}
