// Package iyuuautoseed ports the installed IYUUAutoSeed 2.22 plugin.
// Its API helper retains the byte-identical iyuu_helper.py behavior.
package iyuuautoseed

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Operation string

const (
	SitesIndex     Operation = "sites_index"
	ReportExisting Operation = "report_existing"
	ReseedIndex    Operation = "reseed_index"
)

// APIRequest is a closed operation set. The worker never supplies an origin,
// token, HTTP headers, or an arbitrary RPC name.
type APIRequest struct {
	Operation Operation
	Params    map[string]any
}
type APIResponse struct {
	Status int
	Reason string
	Body   []byte
}
type API interface {
	Call(context.Context, APIRequest) (*APIResponse, error)
}
type SourceFault struct {
	Class     string
	operation string
}

func (f *SourceFault) Error() string     { return "IYUU source " + f.Class + " in " + f.operation }
func sourceFault(class, op string) error { return &SourceFault{Class: class, operation: op} }

type Failure struct {
	Kind            string
	RequireSiteHash bool
	sourceMessage   string
}

func (f *Failure) Error() string { return "IYUU request failed (" + f.Kind + ")" }

type APIResult struct {
	Data    any
	Failure *Failure
	// ObjectKeys is the insertion order of data when it is a JSON object.
	ObjectKeys []string
}

type siteEntry struct {
	key  string
	id   any
	data map[string]any
}
type Helper struct {
	mu      sync.Mutex
	api     API
	now     func() time.Time
	sites   []siteEntry
	sidSHA1 any
}

type HelperSiteState struct {
	Key  string         `json:"key"`
	ID   any            `json:"id"`
	Data map[string]any `json:"data"`
}
type HelperState struct {
	Sites []HelperSiteState `json:"sites"`
	SID   any               `json:"sid_sha1"`
}

func (h *Helper) Snapshot() (HelperState, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := HelperState{Sites: []HelperSiteState{}}
	var e error
	s.SID, e = cloneJSON(h.sidSHA1)
	if e != nil {
		return s, e
	}
	for _, site := range h.sites {
		data, e := cloneMap(site.data)
		if e != nil {
			return s, e
		}
		id, e := cloneJSON(site.id)
		if e != nil {
			return s, e
		}
		s.Sites = append(s.Sites, HelperSiteState{site.key, id, data})
	}
	return s, nil
}
func RestoreHelper(api API, now func() time.Time, state HelperState) (*Helper, error) {
	h, e := NewHelper(api, now)
	if e != nil {
		return nil, e
	}
	h.sidSHA1, e = cloneJSON(state.SID)
	if e != nil {
		return nil, e
	}
	for _, site := range state.Sites {
		key, e := scalarKey(site.ID)
		if e != nil || key != site.Key {
			return nil, errors.New("invalid IYUU helper checkpoint")
		}
		data, e := cloneMap(site.Data)
		if e != nil {
			return nil, e
		}
		h.sites = append(h.sites, siteEntry{key: key, id: site.ID, data: data})
	}
	return h, nil
}

func NewHelper(api API, now func() time.Time) (*Helper, error) {
	if api == nil {
		return nil, errors.New("IYUU API is required")
	}
	if now == nil {
		now = time.Now
	}
	return &Helper{api: api, now: now}, nil
}

func (h *Helper) request(ctx context.Context, req APIRequest) (APIResult, error) {
	response, err := h.api.Call(ctx, req)
	if err != nil {
		return APIResult{}, err
	}
	fail := func(kind, msg string) (APIResult, error) {
		return APIResult{Failure: &Failure{Kind: kind, RequireSiteHash: strings.Contains(msg, "站点哈希值 require"), sourceMessage: msg}}, nil
	}
	if response == nil {
		return fail("no_response", "请求IYUU失败，未获取到返回信息")
	}
	if response.Status >= 400 {
		return fail("http", "请求IYUU失败，状态码："+pyString(json.Number(itoa(response.Status)))+"，错误原因："+response.Reason)
	}
	value, err := decodeJSON(response.Body)
	if err != nil {
		// Python JSONDecodeError derives from ValueError. Public errors intentionally
		// omit body prefixes: they can contain a token or site credentials.
		runes := []rune(string(response.Body))
		if len(runes) > 200 {
			runes = runes[:200]
		}
		preview := strings.ReplaceAll(string(runes), "\n", "\\n")
		return fail("json", "请求IYUU失败，响应解析失败：JSONDecodeError 状态码："+itoa(response.Status)+"，响应前缀："+preview)
	}
	result, ok := value.(map[string]any)
	if !ok {
		return APIResult{}, sourceFault("AttributeError", "request.result.get")
	}
	if zero(result["code"]) {
		var rawFields map[string]json.RawMessage
		_ = json.Unmarshal(response.Body, &rawFields)
		keys, _ := objectKeys(rawFields["data"])
		return APIResult{Data: result["data"], ObjectKeys: keys}, nil
	}
	return fail("api", "请求IYUU失败，状态码："+pyString(result["code"])+"，返回信息："+pyString(result["msg"]))
}

func (h *Helper) loadSites(ctx context.Context) error {
	r, err := h.request(ctx, APIRequest{Operation: SitesIndex})
	if err != nil {
		return err
	}
	h.sites = nil
	if !truth(r.Data) {
		return nil
	}
	result, ok := r.Data.(map[string]any)
	if !ok {
		return sourceFault("AttributeError", "sites.result.get")
	}
	var sites []any
	switch x := result["sites"].(type) {
	case []any:
		sites = x
	case string:
		for _, r := range x {
			sites = append(sites, string(r))
		}
	case map[string]any:
		if len(x) > 0 {
			return sourceFault("AttributeError", "site.get")
		}
	default:
		return sourceFault("TypeError", "sites.iter")
	}
	indexes := make(map[string]int)
	for _, raw := range sites {
		site, ok := raw.(map[string]any)
		if !ok {
			return sourceFault("AttributeError", "site.get")
		}
		id := site["id"]
		key, err := scalarKey(id)
		if err != nil {
			return sourceFault("TypeError", "site.id.key")
		}
		if i, ok := indexes[key]; ok {
			h.sites[i].data = site
		} else {
			indexes[key] = len(h.sites)
			h.sites = append(h.sites, siteEntry{key: key, id: id, data: site})
		}
	}
	return nil
}

// TorrentURL retains ID type and field presence from the original API. Returned
// JSON values are copied; callers cannot mutate the helper's cached site map.
func (h *Helper) TorrentURL(ctx context.Context, sid any) (any, any, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !truth(sid) {
		return nil, nil, nil
	}
	if len(h.sites) == 0 {
		if err := h.loadSites(ctx); err != nil {
			return nil, nil, err
		}
	}
	key, err := scalarKey(sid)
	if err != nil {
		return nil, nil, err
	}
	for _, s := range h.sites {
		if s.key == key && truth(s.data) {
			a, e := cloneJSON(s.data["base_url"])
			if e != nil {
				return nil, nil, e
			}
			b, e := cloneJSON(s.data["download_page"])
			return a, b, e
		}
	}
	return nil, nil, nil
}

func (h *Helper) report(ctx context.Context) (any, error) {
	if len(h.sites) == 0 {
		if err := h.loadSites(ctx); err != nil {
			return nil, err
		}
	}
	ids := make([]any, 0, len(h.sites))
	for _, s := range h.sites {
		ids = append(ids, s.id)
	}
	r, err := h.request(ctx, APIRequest{Operation: ReportExisting, Params: map[string]any{"sid_list": ids}})
	if err != nil {
		return nil, err
	}
	if !truth(r.Data) {
		return nil, nil
	}
	m, ok := r.Data.(map[string]any)
	if !ok {
		return nil, sourceFault("AttributeError", "report.result.get")
	}
	return m["sid_sha1"], nil
}

func (h *Helper) reseed(ctx context.Context, compact, digest string) (APIResult, error) {
	var timestamp int64
	var err error
	if clock, ok := h.api.(sourceAPIClock); ok {
		timestamp, err = clock.SourceUnix(ctx)
	} else {
		timestamp, err = sourceUnixTime(h.now())
	}
	if err != nil {
		return APIResult{}, err
	}
	return h.request(ctx, APIRequest{Operation: ReseedIndex, Params: map[string]any{"hash": compact, "sha1": digest, "sid_sha1": h.sidSHA1, "timestamp": json.Number(itoa64(timestamp)), "version": "8.2.0"}})
}

// SeedInfo sorts the caller's list in place, signs the exact Python compact JSON
// bytes, reports cached site IDs, and retries exactly once for the source's
// specific site-hash error phrase. Network errors are never a successful result.
func (h *Helper) SeedInfo(ctx context.Context, hashes []string) (APIResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !truth(h.sidSHA1) {
		v, e := h.report(ctx)
		if e != nil {
			return APIResult{}, e
		}
		h.sidSHA1 = v
	}
	sort.Strings(hashes)
	compact, err := compactHashes(hashes)
	if err != nil {
		return APIResult{}, err
	}
	digest := SHA1(compact)
	r, err := h.reseed(ctx, compact, digest)
	if err != nil {
		return APIResult{}, err
	}
	if r.Failure != nil && r.Failure.RequireSiteHash {
		v, e := h.report(ctx)
		if e != nil {
			return APIResult{}, e
		}
		h.sidSHA1 = v
		return h.reseed(ctx, compact, digest)
	}
	return r, nil
}
func SHA1(s string) string  { v := sha1.Sum([]byte(s)); return hex.EncodeToString(v[:]) }
func itoa(n int) string     { return strconv.Itoa(n) }
func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
