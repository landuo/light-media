package brushflow

import (
	enc "light-media/internal/plugins/iyuuautoseed/compat/encodingdetector"
	pyre "light-media/internal/plugins/iyuuautoseed/compat/pythonre"
	"strings"
)

// DecodeRSSXML ports RequestUtils.get_decoded_xml_content at the original
// default compatibility mode/0.8 threshold. Strict decoding and codec errors
// use the already-pinned CPython/chardet implementation.
func DecodeRSSXML(raw []byte, contentType string) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	try := func(names []string) (string, bool, error) {
		seen := map[string]bool{}
		for _, name := range names {
			name = strings.TrimSpace(name)
			key := strings.ToLower(name)
			if name == "" || seen[key] {
				continue
			}
			seen[key] = true
			text, err := enc.Decode(raw, name, false)
			if err == nil {
				return text, true, nil
			}
			if class := enc.ErrorClass(err); class != "LookupError" && class != "UnicodeDecodeError" {
				return "", false, err
			}
		}
		return "", false, nil
	}
	head := raw
	if len(head) > 512 {
		head = head[:512]
	}
	var ascii strings.Builder
	for _, c := range head {
		if c < 128 {
			ascii.WriteByte(c)
		}
	}
	p, _ := pyre.Compile(`(?i)^\s*(?:\ufeff)?<\?xml[^>]*encoding\s*=\s*["']([^"']+)["']`)
	if m, e := p.SearchGroups(ascii.String(), 0); e == nil && m != nil && len(m.Groups) > 1 && m.Groups[1] != nil {
		if text, ok, err := try([]string{*m.Groups[1]}); err != nil {
			return "", err
		} else if ok {
			return text, nil
		}
	}
	responseEncoding := ""
	parts := strings.Split(contentType, ";")
	for _, part := range parts[1:] {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 && strings.ToLower(strings.Trim(kv[0], " \t\r\n'\"")) == "charset" {
			responseEncoding = strings.Trim(strings.TrimSpace(kv[1]), "'\"")
		}
	}
	if responseEncoding == "" {
		if strings.Contains(parts[0], "text") {
			responseEncoding = "ISO-8859-1"
		} else if strings.Contains(parts[0], "application/json") {
			responseEncoding = "utf-8"
		}
	}
	apparent, confidence, e := enc.Detect(raw)
	if e != nil {
		return "", e
	}
	if confidence > .8 {
		if text, ok, err := try([]string{apparent}); err != nil {
			return enc.Decode(raw, "utf-8", true)
		} else if ok {
			return text, nil
		}
	}
	trusted := func(v string) string {
		s := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(v)), "_", "-")
		if s == "iso-8859-1" || s == "latin-1" || s == "latin1" {
			return ""
		}
		return v
	}
	if text, ok, err := try([]string{"utf-8", trusted(responseEncoding), trusted(apparent)}); err != nil {
		return enc.Decode(raw, "utf-8", true)
	} else if ok {
		return text, nil
	}
	if text, ok, err := try([]string{apparent, "utf-8", apparent, responseEncoding}); err != nil {
		return enc.Decode(raw, "utf-8", true)
	} else if ok {
		return text, nil
	}
	return enc.Decode(raw, "utf-8", true)
}
