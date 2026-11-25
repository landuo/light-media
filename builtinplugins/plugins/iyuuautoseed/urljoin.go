package iyuuautoseed

// String-only urllib.parse operations consumed by the GPW source resolver.
// Constants and the complete Unicode15 NFKC delimiter predicate are extracted
// from the pinned actual CPython3.12.13 reference, not Go URL normalization.
import (
	"net/netip"
	"strings"
)

var sourceRelativeSchemes = strings.Fields("ftp http gopher nntp imap wais file https shttp mms prospero rtsp rtsps rtspu sftp svn svn+ssh ws wss")
var sourceNetlocSchemes = strings.Fields("ftp http gopher nntp telnet imap wais file mms https shttp snews prospero rtsp rtsps rtspu rsync svn svn+ssh sftp nfs git git+ssh ws wss itms-services")
var sourceParamSchemes = strings.Fields("ftp hdl prospero http imap https shttp rtsp rtsps rtspu sip sips mms sftp tel")

func sourceSchemeIn(s string, items []string) bool {
	if s == "" {
		return true
	}
	for _, item := range items {
		if item == s {
			return true
		}
	}
	return false
}

type sourceParsedURL struct{ scheme, netloc, path, params, query, fragment string }

func sourceURLValueError() error { return sourceFault("ValueError", "urllib.parse URL") }
func sourceNFKCDelimiter(r rune) bool {
	switch r {
	case 8263, 8264, 8265, 8448, 8449, 8453, 8454, 10868, 65043, 65046, 65109, 65110, 65119, 65131, 65283, 65295, 65306, 65311, 65312:
		return true
	}
	return false
}
func sourceBracketedNetloc(netloc string) error {
	hostPort := netloc
	if i := strings.LastIndexByte(hostPort, '@'); i >= 0 {
		hostPort = hostPort[i+1:]
	}
	host := hostPort
	if i := strings.IndexByte(hostPort, '['); i >= 0 {
		if i != 0 {
			return sourceURLValueError()
		}
		rest := hostPort[1:]
		j := strings.IndexByte(rest, ']')
		if j < 0 {
			return sourceURLValueError()
		}
		host = rest[:j]
		port := rest[j+1:]
		if port != "" && !strings.HasPrefix(port, ":") {
			return sourceURLValueError()
		}
	} else if i := strings.IndexByte(hostPort, ':'); i >= 0 {
		host = hostPort[:i]
	}
	if strings.HasPrefix(host, "v") {
		i := strings.IndexByte(host, '.')
		if i < 2 || i == len(host)-1 || strings.ContainsRune(host, '\n') {
			return sourceURLValueError()
		}
		for _, c := range host[1:i] {
			if !(c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' || c >= '0' && c <= '9') {
				return sourceURLValueError()
			}
		}
		return nil
	}
	if strings.Count(host, "%") > 1 {
		return sourceURLValueError()
	}
	addr, e := netip.ParseAddr(host)
	if e != nil || addr.Is4() {
		return sourceURLValueError()
	}
	return nil
}
func sourceParseURL(value, scheme string) (sourceParsedURL, error) {
	result := sourceParsedURL{}
	value = strings.TrimLeftFunc(value, func(r rune) bool { return r <= 32 })
	scheme = strings.TrimFunc(scheme, func(r rune) bool { return r <= 32 })
	for _, b := range []string{"\t", "\r", "\n"} {
		value = strings.ReplaceAll(value, b, "")
		scheme = strings.ReplaceAll(scheme, b, "")
	}
	if i := strings.IndexByte(value, ':'); i > 0 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) {
		valid := true
		for _, r := range value[:i] {
			if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789+-.", r) {
				valid = false
				break
			}
		}
		if valid {
			scheme = strings.ToLower(value[:i])
			value = value[i+1:]
		}
	}
	result.scheme = scheme
	if strings.HasPrefix(value, "//") {
		rest := value[2:]
		i := len(rest)
		for _, b := range []byte{'/', '?', '#'} {
			if n := strings.IndexByte(rest, b); n >= 0 && n < i {
				i = n
			}
		}
		result.netloc = rest[:i]
		value = rest[i:]
		open, close := strings.ContainsRune(result.netloc, '['), strings.ContainsRune(result.netloc, ']')
		if open != close {
			return result, sourceURLValueError()
		}
		if open {
			if e := sourceBracketedNetloc(result.netloc); e != nil {
				return result, e
			}
		}
	}
	if i := strings.IndexByte(value, '#'); i >= 0 {
		result.fragment = value[i+1:]
		value = value[:i]
	}
	if i := strings.IndexByte(value, '?'); i >= 0 {
		result.query = value[i+1:]
		value = value[:i]
	}
	for _, r := range result.netloc {
		if sourceNFKCDelimiter(r) {
			return result, sourceURLValueError()
		}
	}
	result.path = value
	if sourceSchemeIn(scheme, sourceParamSchemes) && strings.ContainsRune(value, ';') {
		from := 0
		if i := strings.LastIndexByte(value, '/'); i >= 0 {
			from = i
		}
		if i := strings.IndexByte(value[from:], ';'); i >= 0 {
			i += from
			result.path, result.params = value[:i], value[i+1:]
		}
	}
	return result, nil
}
func sourceURLUnparse(p sourceParsedURL) string {
	path := p.path
	if p.params != "" {
		path += ";" + p.params
	}
	if p.netloc != "" {
		if path != "" && !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		path = "//" + p.netloc + path
	} else if strings.HasPrefix(path, "//") {
		path = "//" + path
	} else if p.scheme != "" && sourceSchemeIn(p.scheme, sourceNetlocSchemes) && (path == "" || strings.HasPrefix(path, "/")) {
		path = "//" + path
	}
	if p.scheme != "" {
		path = p.scheme + ":" + path
	}
	if p.query != "" {
		path += "?" + p.query
	}
	if p.fragment != "" {
		path += "#" + p.fragment
	}
	return path
}
func sourceURLJoin(base, relative string) (string, error) {
	if base == "" {
		return relative, nil
	}
	if relative == "" {
		return base, nil
	}
	b, e := sourceParseURL(base, "")
	if e != nil {
		return "", e
	}
	r, e := sourceParseURL(relative, b.scheme)
	if e != nil {
		return "", e
	}
	if r.scheme != b.scheme || !sourceSchemeIn(r.scheme, sourceRelativeSchemes) {
		return relative, nil
	}
	if sourceSchemeIn(r.scheme, sourceNetlocSchemes) {
		if r.netloc != "" {
			return sourceURLUnparse(r), nil
		}
		r.netloc = b.netloc
	}
	if r.path == "" && r.params == "" {
		r.path, r.params = b.path, b.params
		if r.query == "" {
			r.query = b.query
		}
		return sourceURLUnparse(r), nil
	}
	baseParts := strings.Split(b.path, "/")
	if baseParts[len(baseParts)-1] != "" {
		baseParts = baseParts[:len(baseParts)-1]
	}
	var segments []string
	if strings.HasPrefix(r.path, "/") {
		segments = strings.Split(r.path, "/")
	} else {
		segments = append(baseParts, strings.Split(r.path, "/")...)
		if len(segments) > 2 {
			filtered := []string{segments[0]}
			for _, part := range segments[1 : len(segments)-1] {
				if part != "" {
					filtered = append(filtered, part)
				}
			}
			segments = append(filtered, segments[len(segments)-1])
		}
	}
	resolved := []string{}
	for _, part := range segments {
		if part == ".." {
			if len(resolved) > 0 {
				resolved = resolved[:len(resolved)-1]
			}
		} else if part != "." {
			resolved = append(resolved, part)
		}
	}
	last := segments[len(segments)-1]
	if last == "." || last == ".." {
		resolved = append(resolved, "")
	}
	r.path = strings.Join(resolved, "/")
	if r.path == "" {
		r.path = "/"
	}
	return sourceURLUnparse(r), nil
}
