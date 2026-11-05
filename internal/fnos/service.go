package fnos

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"light-media/internal/logging"
	"math/big"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Service is the application fnOS Media adapter. It never creates or deletes a
// library. Each operation authenticates a fresh bounded in-memory session.
type Service struct {
	config Config
	host   string
	http   *http.Client
}

type ServiceLibrary struct {
	GUID        string   `json:"guid"`
	Name        string   `json:"name"`
	Category    string   `json:"category"`
	Directories []string `json:"directories"`
}

// ServiceError contains no provider body, address or credential.
type ServiceError struct{ Code string }

func (e *ServiceError) Error() string {
	switch e.Code {
	case "not_configured":
		return "飞牛影视凭据尚未配置"
	case "invalid_config":
		return "飞牛影视地址无效，请使用可信 HTTPS 地址，或内网 IP 的 HTTP 地址；路径仅支持 /v"
	case "invalid_input":
		return "飞牛影视请求参数或目录无效"
	case "unauthorized":
		return "飞牛影视认证失败或权限不足"
	case "invalid_response":
		return "飞牛影视返回了无法确认的数据"
	case "request_limit", "needs_attention":
		return "尚未唯一确认飞牛入库结果，请稍后重试或人工核对"
	case "redirect_blocked":
		return "飞牛影视返回了不允许的重定向"
	case "canceled":
		return "飞牛影视请求已取消"
	case "tls_certificate_failed":
		return "飞牛影视 HTTPS 证书校验失败，请使用证书可信的地址，或内网 HTTP 端口"
	case "timeout":
		return "连接飞牛影视超时，请检查地址、端口和网络"
	case "connection_refused":
		return "飞牛影视端口拒绝连接，请确认端口正确且服务已启动"
	case "scan_rejected":
		return "飞牛影视未接受媒体库刷新请求"
	default:
		return "飞牛影视暂时不可用，请稍后重试"
	}
}

func (*Service) String() string   { return "fnOS Media service [redacted]" }
func (*Service) GoString() string { return "fnos.Service{[redacted]}" }

func NewService(config Config) (*Service, error) { return newService(config, http.DefaultTransport) }

func newService(config Config, transport http.RoundTripper) (*Service, error) {
	host, err := NormalizeHost(config.Host)
	if err != nil || config.AccessCode != "" {
		return nil, &ServiceError{Code: "invalid_config"}
	}
	if config.Username == "" || config.Password == "" {
		return nil, &ServiceError{Code: "not_configured"}
	}
	if !serviceText(config.Username, 256) || !serviceText(config.Password, 8192) {
		return nil, &ServiceError{Code: "invalid_config"}
	}
	return &Service{config: Config{Username: config.Username, Password: config.Password}, host: host, http: newFNOSHTTPClient(host, transport)}, nil
}

type serviceSession struct {
	service  *Service
	host     string
	token    string
	admin    bool
	requests int
}

// The original refresh endpoints use POST with an empty body, signed as an
// empty payload. This is distinct from POSTing a JSON empty object.
type serviceEmptyPost struct{}

func (*serviceSession) String() string   { return "fnOS Media session [redacted]" }
func (*serviceSession) GoString() string { return "fnos.serviceSession{[redacted]}" }

func (s *serviceSession) call(ctx context.Context, endpoint string, body any, query string, dst any) error {
	if s.requests >= 20 {
		return &ServiceError{Code: "request_limit"}
	}
	if ctx == nil {
		return &ServiceError{Code: "invalid_input"}
	}
	ctx, _ = logging.Target(ctx, "fnos")
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	method, signingPayload := http.MethodGet, ""
	var payload []byte
	if body != nil {
		var err error
		if _, empty := body.(serviceEmptyPost); !empty {
			payload, err = json.Marshal(body)
		}
		if err != nil || query != "" {
			return &ServiceError{Code: "invalid_input"}
		}
		method, signingPayload = http.MethodPost, string(payload)
	}
	host := s.host
	if host == "" {
		host = s.service.host
	}
	endpointURL := host + "/api/v1" + endpoint
	if query != "" {
		endpointURL += "?" + url.Values{"q": {query}}.Encode()
		signingPayload = "q=" + query
	}
	nonce, err := rand.Int(rand.Reader, big.NewInt(900000))
	if err != nil {
		return &ServiceError{Code: "unavailable"}
	}
	request, err := http.NewRequestWithContext(logging.EndpointContext(ctx, "fnos", endpoint), method, endpointURL, bytes.NewReader(payload))
	if err != nil {
		return &ServiceError{Code: "invalid_input"}
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "light-media/0.1")
	request.Header.Set("Referer", host)
	request.Header.Set("authx", Signature("/v/api/v1"+endpoint, signingPayload, strconv.FormatInt(nonce.Int64()+100000, 10), strconv.FormatInt(time.Now().UnixMilli(), 10)))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if s.token != "" {
		request.Header.Set("Authorization", s.token)
	}
	s.requests++
	response, err := s.service.http.Do(request)
	if err != nil {
		return &ServiceError{Code: transportErrorCode(err)}
	}
	defer response.Body.Close()
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return &ServiceError{Code: "unauthorized"}
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return &ServiceError{Code: "redirect_blocked"}
	}
	if response.StatusCode != http.StatusOK {
		return &ServiceError{Code: "unavailable"}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2<<20+1))
	if err != nil {
		return &ServiceError{Code: transportErrorCode(err)}
	}
	var env envelope
	if len(raw) > 2<<20 || json.Unmarshal(raw, &env) != nil || env.Code == nil {
		return &ServiceError{Code: "invalid_response"}
	}
	if *env.Code != 0 {
		if endpoint == "/login" {
			return &ServiceError{Code: "unauthorized"}
		}
		return &ServiceError{Code: "unavailable"}
	}
	if dst == nil {
		return nil // A scan is accepted, not proof that import has completed.
	}
	if len(env.Data) == 0 || bytes.Equal(bytes.TrimSpace(env.Data), []byte("null")) || json.Unmarshal(env.Data, dst) != nil {
		return &ServiceError{Code: "invalid_response"}
	}
	return nil
}

func (s *Service) session(ctx context.Context) (*serviceSession, error) {
	if s == nil {
		return nil, &ServiceError{Code: "not_configured"}
	}
	if ctx == nil {
		return nil, &ServiceError{Code: "invalid_input"}
	}
	host, requests, err := s.resolveSessionEntry(ctx)
	if err != nil {
		return nil, err
	}
	current := &serviceSession{service: s, host: host, requests: requests}
	var auth struct {
		Token string `json:"token"`
	}
	if err := current.call(ctx, "/login", map[string]string{"username": s.config.Username, "password": s.config.Password, "app_name": "trimemedia-web"}, "", &auth); err != nil {
		return nil, err
	}
	if !serviceText(auth.Token, 8192) {
		return nil, &ServiceError{Code: "unauthorized"}
	}
	current.token = auth.Token
	var user struct {
		Admin *int `json:"is_admin"`
	}
	if err := current.call(ctx, "/user/info", nil, "", &user); err != nil {
		return nil, err
	}
	if user.Admin == nil || (*user.Admin != 0 && *user.Admin != 1) {
		return nil, &ServiceError{Code: "invalid_response"}
	}
	current.admin = *user.Admin == 1
	return current, nil
}

func (s *serviceSession) libraries(ctx context.Context) ([]ServiceLibrary, error) {
	return s.readLibraries(ctx, true)
}

func (s *serviceSession) readLibraries(ctx context.Context, sortByName bool) ([]ServiceLibrary, error) {
	return s.readLibrariesWithPaths(ctx, sortByName, false)
}

// sourcePaths preserves raw pathlib inputs for the plugin source matcher. All
// existing callers retain the strict, canonical P0 path contract above.
func (s *serviceSession) readLibrariesWithPaths(ctx context.Context, sortByName, sourcePaths bool) ([]ServiceLibrary, error) {
	endpoint := "/mediadb/list"
	if s.admin {
		endpoint = "/mdb/list"
	}
	var rows []struct {
		GUID     string   `json:"guid"`
		Name     string   `json:"name"`
		Title    string   `json:"title"`
		Category string   `json:"category"`
		Dirs     []string `json:"dir_list"`
	}
	if err := s.call(ctx, endpoint, nil, "", &rows); err != nil {
		return nil, err
	}
	if rows == nil || len(rows) > 200 {
		return nil, &ServiceError{Code: "invalid_response"}
	}
	result, seen := []ServiceLibrary{}, map[string]bool{}
	for _, row := range rows {
		if row.Name == "" {
			row.Name = row.Title
		}
		if !detailGUID.MatchString(row.GUID) || seen[row.GUID] || !serviceText(row.Name, 512) || !serviceText(row.Category, 128) || (!sourcePaths && len(row.Dirs) == 0) || len(row.Dirs) > 32 || s.sensitive(row.Name) || s.sensitive(row.Category) || s.sensitive(row.GUID) {
			return nil, &ServiceError{Code: "invalid_response"}
		}
		seen[row.GUID] = true
		lib := ServiceLibrary{GUID: row.GUID, Name: row.Name, Category: row.Category, Directories: []string{}}
		for _, dir := range row.Dirs {
			if sourcePaths {
				lib.Directories = append(lib.Directories, dir)
				continue
			}
			if !servicePath(dir, true) || s.sensitive(dir) {
				return nil, &ServiceError{Code: "invalid_response"}
			}
			lib.Directories = append(lib.Directories, path.Clean(dir))
		}
		result = append(result, lib)
	}
	if sortByName {
		sort.SliceStable(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	}
	return result, nil
}

func (s *serviceSession) sensitive(text string) bool {
	for _, secret := range []string{s.service.config.Password, s.token} {
		if secret != "" && strings.Contains(text, secret) {
			return true
		}
	}
	return false
}

func (s *Service) Libraries(ctx context.Context) ([]ServiceLibrary, error) {
	current, err := s.session(ctx)
	if err != nil {
		return nil, err
	}
	return current.libraries(ctx)
}

// Scan only requests the specified strict child directory of the selected
// configured library. A successful return means scan accepted, not item visible.
func (s *Service) Scan(ctx context.Context, libraryGUID, dir string) error {
	if !detailGUID.MatchString(libraryGUID) || !servicePath(dir, false) {
		return &ServiceError{Code: "invalid_input"}
	}
	current, err := s.session(ctx)
	if err != nil {
		return err
	}
	if !current.admin {
		return &ServiceError{Code: "unauthorized"}
	}
	libs, err := current.libraries(ctx)
	if err != nil {
		return err
	}
	lib, found := serviceLibrary(libs, libraryGUID)
	if !found || !insideLibrary(lib, dir, true) {
		return &ServiceError{Code: "invalid_input"}
	}
	return current.call(ctx, "/mdb/scan/"+libraryGUID, map[string][]string{"dir_list": {dir}}, "", nil)
}

func serviceLibrary(libraries []ServiceLibrary, guid string) (ServiceLibrary, bool) {
	for _, lib := range libraries {
		if lib.GUID == guid {
			return lib, true
		}
	}
	return ServiceLibrary{}, false
}

func insideLibrary(lib ServiceLibrary, target string, strict bool) bool {
	for _, root := range lib.Directories {
		if (!strict && target == root) || strings.HasPrefix(target, root+"/") {
			return true
		}
	}
	return false
}

func serviceText(value string, limit int) bool {
	if value == "" || len(value) > limit || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func servicePath(value string, trailingSlash bool) bool {
	if !serviceText(value, 4096) || !strings.HasPrefix(value, "/") || value == "/" || strings.Contains(value, "\\") {
		return false
	}
	want := value
	if trailingSlash {
		want = strings.TrimSuffix(value, "/")
	}
	return path.Clean(value) == want
}

type serviceNode struct {
	GUID         string  `json:"guid"`
	AncestorGUID *string `json:"ancestor_guid"`
	ParentGUID   *string `json:"parent_guid"`
	Type         string  `json:"type"`
	Title        string  `json:"title"`
	Season       *int    `json:"season_number"`
	Episode      *int    `json:"episode_number"`
}

func (s *serviceSession) detail(ctx context.Context, guid string) (serviceNode, error) {
	if !detailGUID.MatchString(guid) {
		return serviceNode{}, &ServiceError{Code: "needs_attention"}
	}
	var item serviceNode
	if err := s.call(ctx, "/item/"+guid, nil, "", &item); err != nil {
		return serviceNode{}, err
	}
	titleValid := serviceText(item.Title, 1024) || (item.Type == "Season" && item.Title == "" && item.Season != nil && *item.Season >= 0)
	if item.GUID != guid || s.sensitive(item.GUID) || item.AncestorGUID == nil || !detailGUID.MatchString(*item.AncestorGUID) || item.ParentGUID == nil || !titleValid {
		return serviceNode{}, &ServiceError{Code: "needs_attention"}
	}
	return item, nil
}

func scopeNode(node serviceNode, libraryGUID, parent, kind string) bool {
	return node.AncestorGUID != nil && *node.AncestorGUID == libraryGUID && node.ParentGUID != nil && *node.ParentGUID == parent && node.Type == kind
}

var serviceEpisodePattern = regexp.MustCompile(`(?i)(?:^|[ ._-])S([0-9]{1,3})E([0-9]{1,4})(?:$|[ ._-])`)

// Verify confirms a unique actual item-file association, not just a search hit.
// TV uses exact series title and the actual root/season/episode parent chain.
// A canonical SxxExx basename only selects which season/episode to inspect.
func (s *Service) Verify(ctx context.Context, libraryGUID, title, kind, actualPath string) (string, error) {
	if !detailGUID.MatchString(libraryGUID) || !serviceText(title, 1024) || !servicePath(actualPath, false) || (kind != "Movie" && kind != "Episode") {
		return "", &ServiceError{Code: "invalid_input"}
	}
	seasonNumber, episodeNumber := 0, 0
	if kind == "Episode" {
		stem := strings.TrimSuffix(path.Base(actualPath), path.Ext(actualPath))
		hints := serviceEpisodePattern.FindAllStringSubmatch(stem, -1)
		if len(hints) != 1 {
			return "", &ServiceError{Code: "needs_attention"}
		}
		seasonNumber, _ = strconv.Atoi(hints[0][1])
		episodeNumber, _ = strconv.Atoi(hints[0][2])
		if episodeNumber < 1 {
			return "", &ServiceError{Code: "needs_attention"}
		}
	}
	current, err := s.session(ctx)
	if err != nil {
		return "", err
	}
	libs, err := current.libraries(ctx)
	if err != nil {
		return "", err
	}
	lib, found := serviceLibrary(libs, libraryGUID)
	if !found || !insideLibrary(lib, actualPath, true) {
		return "", &ServiceError{Code: "invalid_input"}
	}
	var results []serviceNode
	if err := current.call(ctx, "/search/list", nil, title, &results); err != nil {
		return "", err
	}
	if results == nil || len(results) > 10 {
		return "", &ServiceError{Code: "needs_attention"}
	}
	matches := []string{}
	seen := map[string]bool{}
	for _, candidate := range results {
		rootKind := kind
		if kind == "Episode" {
			rootKind = "TV"
		}
		if candidate.Title != title || candidate.Type != rootKind {
			continue
		}
		if seen[candidate.GUID] {
			return "", &ServiceError{Code: "needs_attention"}
		}
		seen[candidate.GUID] = true
		root, err := current.detail(ctx, candidate.GUID)
		if err != nil {
			return "", err
		}
		if root.AncestorGUID != nil && *root.AncestorGUID != libraryGUID {
			continue
		}
		if !scopeNode(root, libraryGUID, "", rootKind) || root.Title != title {
			return "", &ServiceError{Code: "needs_attention"}
		}
		if kind == "Movie" {
			found, err := current.fileMatches(ctx, root, lib, actualPath)
			if err != nil {
				return "", err
			}
			if found {
				matches = append(matches, root.GUID)
			}
		} else {
			found, err := current.episodeMatches(ctx, root, lib, actualPath, seasonNumber, episodeNumber)
			if err != nil {
				return "", err
			}
			matches = append(matches, found...)
		}
		if len(matches) > 1 {
			return "", &ServiceError{Code: "needs_attention"}
		}
	}
	if len(matches) != 1 {
		return "", &ServiceError{Code: "needs_attention"}
	}
	return matches[0], nil
}

func (s *serviceSession) episodeMatches(ctx context.Context, root serviceNode, lib ServiceLibrary, target string, seasonNumber, episodeNumber int) ([]string, error) {
	var seasons []serviceNode
	if err := s.call(ctx, "/season/list/"+root.GUID, nil, "", &seasons); err != nil {
		return nil, err
	}
	if seasons == nil || len(seasons) > 1000 {
		return nil, &ServiceError{Code: "needs_attention"}
	}
	seasonCandidates := []serviceNode{}
	for _, season := range seasons {
		if season.Season == nil || *season.Season == seasonNumber {
			seasonCandidates = append(seasonCandidates, season)
		}
	}
	if len(seasonCandidates) > 10 {
		return nil, &ServiceError{Code: "needs_attention"}
	}
	matches, seen := []string{}, map[string]bool{}
	for _, candidate := range seasonCandidates {
		if seen[candidate.GUID] {
			return nil, &ServiceError{Code: "needs_attention"}
		}
		seen[candidate.GUID] = true
		season, err := s.detail(ctx, candidate.GUID)
		if err != nil {
			return nil, err
		}
		if !scopeNode(season, lib.GUID, root.GUID, "Season") || season.Season == nil {
			return nil, &ServiceError{Code: "needs_attention"}
		}
		if *season.Season != seasonNumber {
			continue
		}
		var episodes []serviceNode
		if err := s.call(ctx, "/episode/list/"+season.GUID, nil, "", &episodes); err != nil {
			return nil, err
		}
		if episodes == nil || len(episodes) > 10000 {
			return nil, &ServiceError{Code: "needs_attention"}
		}
		candidates := []serviceNode{}
		for _, episode := range episodes {
			if episode.Episode == nil || *episode.Episode == episodeNumber {
				candidates = append(candidates, episode)
			}
		}
		if len(candidates) > 10 {
			return nil, &ServiceError{Code: "needs_attention"}
		}
		for _, candidate := range candidates {
			episode, err := s.detail(ctx, candidate.GUID)
			if err != nil {
				return nil, err
			}
			if !scopeNode(episode, lib.GUID, season.GUID, "Episode") || episode.Season == nil || episode.Episode == nil || *episode.Season != seasonNumber {
				return nil, &ServiceError{Code: "needs_attention"}
			}
			if *episode.Episode != episodeNumber {
				continue
			}
			found, err := s.fileMatches(ctx, episode, lib, target)
			if err != nil {
				return nil, err
			}
			if found {
				matches = append(matches, episode.GUID)
			}
		}
	}
	return matches, nil
}

func (s *serviceSession) fileMatches(ctx context.Context, item serviceNode, lib ServiceLibrary, target string) (bool, error) {
	var rows []map[string]json.RawMessage
	if err := s.call(ctx, "/media/itemfile/"+item.GUID, nil, "", &rows); err != nil {
		return false, err
	}
	if rows == nil || len(rows) > 16 {
		return false, &ServiceError{Code: "needs_attention"}
	}
	matches := 0
	for _, row := range rows {
		for key, expected := range map[string]string{"item_guid": item.GUID, "ancestor_guid": lib.GUID, "mdb_guid": lib.GUID, "parent_guid": *item.ParentGUID, "item_type": item.Type} {
			if raw, exists := row[key]; exists {
				var actual string
				if json.Unmarshal(raw, &actual) != nil || actual != expected {
					return false, &ServiceError{Code: "needs_attention"}
				}
			}
		}
		var actual string
		if json.Unmarshal(row["file_path"], &actual) != nil || !servicePath(actual, false) || !insideLibrary(lib, actual, true) || s.sensitive(actual) {
			return false, &ServiceError{Code: "needs_attention"}
		}
		if actual == target {
			matches++
		}
	}
	if matches > 1 {
		return false, &ServiceError{Code: "needs_attention"}
	}
	return matches == 1, nil
}

var _ fmt.Stringer = (*Service)(nil)
