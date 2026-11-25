package apprise

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

const MaxAttachmentSize = 1048576000 // Apprise AttachBase.max_file_size.
const defaultAttachmentType = "application/octet-stream"

var errAttachmentUnavailable = errors.New("Apprise attachment is unavailable")

// LocalAttachmentReader is supplied by the composition root. Paths from a
// notification are never opened without a caller-owned storage grant.
type LocalAttachmentReader func(context.Context, string) ([]byte, error)

type attachment struct {
	name, mediaType string
	data            []byte
}

type attachmentSource struct {
	request         *Request
	path            string
	name, mediaType string
	remoteName      string
	mu              sync.Mutex
	cached          *attachment
}

//go:embed mimetypes-linux.json
var mimeTypesJSON []byte

var mimeTypes = func() struct {
	Types      []map[string]string
	Suffixes   map[string]string
	Encodings  map[string]string
	Extensions map[string]string
} {
	var out struct {
		Types      []map[string]string
		Suffixes   map[string]string
		Encodings  map[string]string
		Extensions map[string]string
	}
	if err := json.Unmarshal(mimeTypesJSON, &out); err != nil {
		panic(err)
	}
	return out
}()

func attachmentSources(value any) ([]*attachmentSource, bool) {
	var paths []string
	switch value := value.(type) {
	case nil:
		return nil, true
	case string:
		paths = []string{value}
	case []string:
		paths = value
	case []any:
		for _, entry := range value {
			path, ok := entry.(string)
			if !ok {
				return nil, false
			}
			paths = append(paths, path)
		}
	default:
		return nil, false
	}
	result := make([]*attachmentSource, 0, len(paths))
	for _, path := range paths {
		source, ok := parseAttachment(path)
		if !ok {
			return nil, false
		}
		result = append(result, source)
	}
	return result, true
}

func parseAttachment(raw string) (*attachmentSource, bool) {
	if raw == "" {
		return nil, false
	}
	protocol, path, found := strings.Cut(raw, "://")
	source := &attachmentSource{}
	if !found {
		source.path = raw
		return source, true
	}
	switch strings.ToLower(protocol) {
	case "file", "memory":
		// AttachFile keeps the complete path between scheme and query.
		path, query, _ := strings.Cut(path, "?")
		if path == "" {
			return nil, false
		}
		u, _ := parseSourceURL("http://placeholder.invalid/?" + query)
		if !source.setOverrides(u.query) {
			return nil, false
		}
		if strings.EqualFold(protocol, "file") {
			source.path = sourceUnquote(path)
		}
		return source, true
	case "http", "https":
		u, ok := parseSourceURL(raw)
		if !ok {
			return nil, false
		}
		// AttachHTTP disables sanitization of query keys.
		query := make(map[string]string, len(u.queryPairs))
		for _, pair := range u.queryPairs {
			query[pair[0]] = pair[1]
		}
		if !source.setOverrides(query) {
			return nil, false
		}
		source.request = attachmentHTTP(u)
		if slash := strings.LastIndexByte(u.path, '/'); slash >= 0 {
			source.remoteName = u.path[slash+1:]
		}
		return source, true
	default:
		return nil, false
	}
}

func (s *attachmentSource) setOverrides(query map[string]string) bool {
	s.name = strings.ToLower(strings.TrimFunc(query["name"], sourceSpace))
	s.mediaType = strings.ToLower(strings.TrimFunc(query["mime"], sourceSpace))
	if s.mediaType == "" {
		return true
	}
	for _, name := range mimeTypes.Types[1] {
		if name == s.mediaType {
			return true
		}
	}
	return false
}

func attachmentHTTP(u sourceURL) *Request {
	request := &Request{
		Method: http.MethodGet, URL: u.scheme + "://" + u.host + u.port + u.path,
		HeaderOrder: [][2]string{{"User-Agent", "Apprise"}},
		Verify:      true, Timeout: [2]float64{4, 4}, MaxResponseBytes: MaxAttachmentSize,
	}
	if u.user != nil && *u.user != "" {
		var password any
		if u.password != nil {
			password = *u.password
		}
		request.Auth = []any{*u.user, password}
	}
	for _, pair := range u.queryPairs {
		key := pair[0]
		switch key {
		case "verify":
			request.Verify = parseBool(pair[1], false)
			continue
		case "cache", "name", "mime":
			continue
		case "cto", "rto":
			if seconds, err := strconv.ParseFloat(pair[1], 64); err == nil {
				if key == "cto" {
					request.Timeout[0] = seconds
				} else {
					request.Timeout[1] = seconds
				}
			}
		}
		request.ParamOrder = append(request.ParamOrder, pair)
	}
	// AttachHTTP merges the '-' headers first, then the '+' overrides.
	for _, prefix := range []byte{'-', '+'} {
		for _, pair := range u.queryPairs {
			if len(pair[0]) > 1 && (pair[0][0] == prefix || prefix == '+' && pair[0][0] == ' ') {
				request.HeaderOrder = orderedPairSet(request.HeaderOrder, pair[0][1:], pair[1])
			}
		}
	}
	request.Headers = make(map[string]string, len(request.HeaderOrder))
	for _, pair := range request.HeaderOrder {
		request.Headers[pair[0]] = pair[1]
	}
	return request
}

var dispositionFilename = regexp.MustCompile(`(?i)filename=['"]?([^'"]+)['"]?`)

func attachmentHeader(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

func resolveAttachments(ctx context.Context, sources []*attachmentSource, transport Transport, readLocal LocalAttachmentReader) ([]attachment, error) {
	files := make([]attachment, 0, len(sources))
	for _, source := range sources {
		file, err := source.resolve(ctx, transport, readLocal)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

func (s *attachmentSource) resolve(ctx context.Context, transport Transport, readLocal LocalAttachmentReader) (attachment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// AppriseAttachment defaults cache=True and overrides URL cache values.
	if s.cached != nil {
		return *s.cached, nil
	}
	var name, mediaType string
	var data []byte
	if s.request != nil {
		if transport == nil {
			return attachment{}, ErrUnsupported
		}
		reply, err := transport(ctx, *s.request)
		if err != nil || reply.Status >= 400 {
			return attachment{}, errAttachmentUnavailable
		}
		if length, _ := strconv.ParseInt(attachmentHeader(reply.Headers, "Content-Length"), 10, 64); length > MaxAttachmentSize {
			return attachment{}, errAttachmentUnavailable
		}
		if match := dispositionFilename.FindStringSubmatch(attachmentHeader(reply.Headers, "Content-Disposition")); match != nil {
			name = strings.TrimFunc(match[1], sourceSpace)
		}
		if name == "" {
			name = s.remoteName
		}
		mediaType = attachmentHeader(reply.Headers, "Content-Type")
		data = reply.Content
	} else {
		if s.path == "" {
			// A string memory:// URL carries no file bytes.
			return attachment{}, errAttachmentUnavailable
		}
		if readLocal == nil {
			return attachment{}, ErrUnsupported
		}
		var err error
		data, err = readLocal(ctx, s.path)
		if err != nil {
			return attachment{}, errAttachmentUnavailable
		}
		name = filepath.Base(s.path)
	}
	if len(data) > MaxAttachmentSize {
		return attachment{}, errAttachmentUnavailable
	}
	if s.name != "" {
		name = s.name
	}
	if s.mediaType != "" {
		mediaType = s.mediaType
	}
	if mediaType == "" {
		mediaType = guessAttachmentType(name)
	}
	if name == "" {
		ext := mimeTypes.Extensions[mediaType]
		if ext == "" {
			ext = ".obj"
		}
		name = "apprise-attachment" + ext
	}
	value := attachment{name: name, mediaType: mediaType, data: data}
	s.cached = &value
	return value, nil
}

func guessAttachmentType(name string) string {
	ext := filepath.Ext(name)
	if suffix := mimeTypes.Suffixes[strings.ToLower(ext)]; suffix != "" {
		name = strings.TrimSuffix(name, ext) + suffix
		ext = filepath.Ext(name)
	}
	if mimeTypes.Encodings[ext] != "" {
		ext = filepath.Ext(strings.TrimSuffix(name, ext))
	}
	for _, types := range []map[string]string{mimeTypes.Types[1], mimeTypes.Types[0]} {
		if name := types[ext]; name != "" {
			return name
		}
		if name := types[strings.ToLower(ext)]; name != "" {
			return name
		}
	}
	return defaultAttachmentType
}

func attachmentPayload(attachments []attachment) []any {
	payload := make([]any, 0, len(attachments))
	for i, item := range attachments {
		name := item.name
		if name == "" {
			name = fmt.Sprintf("file%03d.dat", i+1)
		}
		payload = append(payload, object{
			{"filename", name},
			{"base64", base64.StdEncoding.EncodeToString(item.data)},
			{"mimetype", item.mediaType},
		})
	}
	return payload
}
