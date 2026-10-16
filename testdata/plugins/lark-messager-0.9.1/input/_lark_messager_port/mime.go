package larkmessager

import (
	_ "embed"
	"net/netip"
	"strings"

	"light-media/internal/releasemeta/legacyanime"
)

// The registry is a fresh process's actual installed Python 3.12.13 registry,
// not Go's OS-dependent mime.TypeByExtension. In particular strict=True does
// not include .webp. See reference/mimetypes.py and mime-registry.json.
//
//go:embed mime-registry.json
var mimeRegistryJSON []byte

var sourceMIMERegistry = func() struct {
	Types     map[string]string `json:"types_map"`
	Suffixes  map[string]string `json:"suffix_map"`
	Encodings map[string]string `json:"encodings_map"`
} {
	var v struct {
		Types     map[string]string `json:"types_map"`
		Suffixes  map[string]string `json:"suffix_map"`
		Encodings map[string]string `json:"encodings_map"`
	}
	if e := decode(mimeRegistryJSON, &v); e != nil {
		panic(e)
	}
	return v
}()

// GuessMIME ports the strict branch of CPython mimetypes.guess_type. Empty
// strings represent Python None. It retains raw URL escapes and the distinct
// all-leading-dots splitext rule (Path.suffix uses a different rule).
func GuessMIME(name string) (mime, encoding string, err error) {
	scheme, parsedPath, err := sourceURLPath(name)
	if err != nil {
		return "", "", err
	}
	if len(scheme) > 1 {
		name = parsedPath
	} else {
		scheme = ""
	}
	if scheme == "data" {
		comma := strings.IndexByte(name, ',')
		if comma < 0 {
			return "", "", nil
		}
		t := name[:comma]
		if i := strings.IndexByte(t, ';'); i >= 0 {
			t = t[:i]
		}
		if strings.Contains(t, "=") || !strings.Contains(t, "/") {
			t = "text/plain"
		}
		return t, "", nil
	}
	base, ext := sourceSplitExt(name)
	for {
		replacement, ok := sourceMIMERegistry.Suffixes[legacyanime.LowerCase(ext)]
		if !ok {
			break
		}
		base, ext = sourceSplitExt(base + replacement)
	}
	if e, ok := sourceMIMERegistry.Encodings[ext]; ok {
		encoding = e
		_, ext = sourceSplitExt(base)
	}
	return sourceMIMERegistry.Types[legacyanime.LowerCase(ext)], encoding, nil
}

func sourceSplitExt(name string) (string, string) {
	start, dot := strings.LastIndexByte(name, '/')+1, strings.LastIndexByte(name, '.')
	if dot >= start {
		for _, r := range name[start:dot] {
			if r != '.' {
				return name[:dot], name[dot:]
			}
		}
	}
	return name, ""
}

// sourceURLPath is the urllib.parse.urlparse closure actually used by the
// source for MIME and remote image paths. It does not percent-decode a path.
// IPv6/NFKC validation uses the fixed Python15 closure already independently
// captured in clean-invalid-seed-2.1/reference/urlparse-source.json.
func sourceURLPath(raw string) (scheme, path string, err error) {
	s := strings.TrimLeftFunc(raw, func(r rune) bool { return r <= 32 })
	s = strings.NewReplacer("\r", "", "\n", "", "\t", "").Replace(s)
	if i := strings.IndexByte(s, ':'); i > 0 && asciiAlpha(s[0]) {
		valid := true
		for _, r := range s[:i] {
			if !(r < 128 && (asciiAlpha(byte(r)) || r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.')) {
				valid = false
				break
			}
		}
		if valid {
			scheme, s = strings.ToLower(s[:i]), s[i+1:]
		}
	}
	if strings.HasPrefix(s, "//") {
		netloc := s[2:]
		i := strings.IndexAny(netloc, "/?#")
		if i < 0 {
			s = ""
		} else {
			s, netloc = netloc[i:], netloc[:i]
		}
		if e := validateSourceNetloc(netloc); e != nil {
			return "", "", e
		}
	}
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '?'); i >= 0 {
		s = s[:i]
	}
	if sourceUsesParams[scheme] {
		lastSlash := strings.LastIndexByte(s, '/')
		if i := strings.IndexByte(s[lastSlash+1:], ';'); i >= 0 {
			s = s[:lastSlash+1+i]
		}
	}
	return scheme, s, nil
}
func asciiAlpha(b byte) bool { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' }

var sourceUsesParams = map[string]bool{"": true, "ftp": true, "hdl": true, "prospero": true, "http": true, "imap": true, "https": true, "shttp": true, "rtsp": true, "rtsps": true, "rtspu": true, "sip": true, "sips": true, "mms": true, "sftp": true, "tel": true}
var sourceNFKCReserved = map[rune]bool{8263: true, 8264: true, 8265: true, 8448: true, 8449: true, 8453: true, 8454: true, 10868: true, 65043: true, 65046: true, 65109: true, 65110: true, 65119: true, 65131: true, 65283: true, 65295: true, 65306: true, 65311: true, 65312: true}

func validateSourceNetloc(s string) error {
	open, close := strings.Contains(s, "["), strings.Contains(s, "]")
	if open != close {
		return ErrUnsupported
	}
	if open {
		hostpart := s
		if i := strings.LastIndexByte(hostpart, '@'); i >= 0 {
			hostpart = hostpart[i+1:]
		}
		i := strings.IndexByte(hostpart, '[')
		if i != 0 {
			return ErrUnsupported
		}
		host, rest, _ := strings.Cut(hostpart[1:], "]")
		if rest != "" && !strings.HasPrefix(rest, ":") {
			return ErrUnsupported
		}
		if strings.HasPrefix(host, "v") {
			version, addr, ok := strings.Cut(host[1:], ".")
			if !ok || version == "" || addr == "" {
				return ErrUnsupported
			}
			for _, r := range version {
				if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
					return ErrUnsupported
				}
			}
		} else {
			ip, e := netip.ParseAddr(host)
			if e != nil || !ip.Is6() || strings.Count(host, "%") > 1 {
				return ErrUnsupported
			}
		}
	}
	for _, r := range s {
		if sourceNFKCReserved[r] {
			return ErrUnsupported
		}
	}
	return nil
}

func remoteImageBlob(ref string, b Blob) (Blob, error) {
	suffix := ".jpg"
	switch {
	case strings.Contains(b.MIME, "png"):
		suffix = ".png"
	case strings.Contains(b.MIME, "gif"):
		suffix = ".gif"
	case strings.Contains(b.MIME, "webp"):
		suffix = ".webp"
	case strings.Contains(b.MIME, "jpeg"), strings.Contains(b.MIME, "jpg"):
		suffix = ".jpg"
	default:
		_, p, e := sourceURLPath(ref)
		if e != nil {
			return Blob{}, e
		}
		ext := legacyanime.LowerCase(SourceSuffix(p))
		if imageSuffix[ext] {
			suffix = ext
		}
	}
	// Random temporary basenames are deliberately represented by a fixed owned
	// basename: extension, media bytes and MIME are the observable source data.
	b.Name = "lark-remote" + suffix
	return b, nil
}
