// Explicit isolated M0 experiment CLI. This is not a production fnOS writer.
// Only a UUID-prefixed new Mix library can be created/scanned/deleted.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"light-media/internal/fnos"
	"light-media/internal/logging"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

type config struct {
	Host          string `json:"host"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	ExperimentID  string `json:"experiment_id"`
	FilesPrepared bool   `json:"files_prepared"`
	FixtureDir    string `json:"fixture_dir,omitempty"`
}
type output struct {
	TraceTargetID     string           `json:"trace_target_id,omitempty"`
	Status            string           `json:"status"`
	Code              string           `json:"code,omitempty"`
	Action            string           `json:"action"`
	ExperimentID      string           `json:"experiment_id,omitempty"`
	Name              string           `json:"name,omitempty"`
	Root              string           `json:"root,omitempty"`
	OwnedGUID         string           `json:"owned_guid,omitempty"`
	Requests          int              `json:"requests"`
	BaselineUnchanged bool             `json:"baseline_unchanged"`
	Candidates        []library        `json:"candidates,omitempty"`
	Items             []map[string]any `json:"items,omitempty"`
	Payload           any              `json:"planned_create_body,omitempty"`
	Note              string           `json:"note,omitempty"`
}
type api struct {
	host, token string
	client      *http.Client
	requests    int
}
type envelope struct {
	Code *int            `json:"code"`
	Data json.RawMessage `json:"data"`
}

func createBody(r receipt) map[string]any {
	return map[string]any{"name": r.Name, "category": "Mix", "dir_list": []string{r.Root}, "include_adult": false, "skip_filesize": 0, "auto_progress_thumb": 0, "prefer_local_nfo": 1, "auto_scrap_subtitle": 0}
}
func normalizeHost(s string) (string, error) {
	u, e := url.Parse(s)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || (u.Path != "" && u.Path != "/" && u.Path != "/v" && u.Path != "/v/") {
		return "", errors.New("INVALID_HOST")
	}
	u.Path = "/v"
	return u.String(), nil
}
func (a *api) call(ctx context.Context, method, endpoint string, body any, dst any) error {
	if a.requests >= 32 {
		return errors.New("REQUEST_LIMIT")
	}
	var payload []byte
	var e error
	if body != nil {
		payload, e = json.Marshal(body)
		if e != nil {
			return errors.New("INVALID_BODY")
		}
	}
	n, e := rand.Int(rand.Reader, big.NewInt(900000))
	if e != nil {
		return errors.New("NONCE_FAILED")
	}
	req, e := http.NewRequestWithContext(logging.EndpointContext(ctx, "fnos", endpoint), method, a.host+"/api/v1"+endpoint, bytes.NewReader(payload))
	if e != nil {
		return errors.New("INVALID_REQUEST")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Referer", a.host)
	req.Header.Set("User-Agent", "light-media-m0-isolated-experiment/0.1")
	req.Header.Set("authx", fnos.Signature("/v/api/v1"+endpoint, string(payload), fmt.Sprint(n.Int64()+100000), fmt.Sprint(time.Now().UnixMilli())))
	if a.token != "" {
		req.Header.Set("Authorization", a.token)
	}
	a.requests++
	resp, e := a.client.Do(req)
	if e != nil {
		return errors.New("TRANSPORT_FAILED")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP_%d", resp.StatusCode)
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
	if e != nil {
		return errors.New("BODY_FAILED")
	}
	if len(b) > 2*1024*1024 {
		return errors.New("BODY_LIMIT")
	}
	var env envelope
	if json.Unmarshal(b, &env) != nil || env.Code == nil {
		return errors.New("INVALID_ENVELOPE")
	}
	if *env.Code != 0 {
		return fmt.Errorf("BUSINESS_%d", *env.Code)
	}
	// Mutations are not considered verified by code=0: callers always read back.
	if dst == nil {
		return nil
	}
	if len(env.Data) == 0 || bytes.Equal(bytes.TrimSpace(env.Data), []byte("null")) || json.Unmarshal(env.Data, dst) != nil {
		return errors.New("INVALID_DATA")
	}
	return nil
}
func (a *api) libraries(ctx context.Context) ([]library, error) {
	var libs []library
	if e := a.call(ctx, "GET", "/mdb/list", nil, &libs); e != nil {
		return nil, e
	}
	if libs == nil || len(libs) > 200 {
		return nil, errors.New("INVALID_LIBRARY_LIST")
	}
	seen := map[string]bool{}
	for _, l := range libs {
		if !guidPattern.MatchString(l.GUID) || seen[l.GUID] || l.Name == "" || len(l.Name) > 4096 || len(l.Dirs) > 32 {
			return nil, errors.New("INVALID_LIBRARY")
		}
		seen[l.GUID] = true
	}
	return libs, nil
}
func (a *api) verifyOwn(ctx context.Context, r receipt, requireSettings bool) (library, []library, error) {
	libs, e := a.libraries(ctx)
	if e != nil {
		return library{}, nil, e
	}
	l, e := ownLibrary(r, libs)
	if e != nil {
		return library{}, libs, e
	}
	var detail struct {
		library
		Prefer *int `json:"prefer_local_nfo"`
		Skip   *int `json:"skip_filesize"`
	}
	if e = a.call(ctx, "GET", "/mdb/"+l.GUID, nil, &detail); e != nil {
		return l, libs, e
	}
	if detail.GUID != l.GUID || detail.Name != l.Name || detail.Category != "Mix" || len(detail.Dirs) != 1 || !samePath(detail.Dirs[0], r.Root) {
		return l, libs, errors.New("OWNED_DETAIL_SCOPE_MISMATCH")
	}
	if requireSettings && (detail.Prefer == nil || *detail.Prefer != 1 || detail.Skip == nil || *detail.Skip != 0) {
		return l, libs, errors.New("TEST_LIBRARY_SETTINGS_NOT_CONFIRMED")
	}
	return l, libs, nil
}
func unchanged(r receipt, libs []library) bool {
	for _, old := range r.Baseline {
		found := false
		for _, l := range libs {
			if l.GUID == old.GUID {
				b, _ := json.Marshal(l)
				o, _ := json.Marshal(old)
				if !bytes.Equal(b, o) {
					return false
				}
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return len(r.Baseline) == len(r.BaselineIDs)
}
func run(ctx context.Context, c config, action string, apply bool) (out output) {
	out = output{Status: "failed", Action: action}
	ctx, out.TraceTargetID = logging.Target(ctx, "fnos_experiment")
	if !uuidPattern.MatchString(c.ExperimentID) {
		out.Code = "INVALID_EXPERIMENT_ID"
		return
	}
	if action != "prepare" && action != "scan" && action != "observe" && action != "cleanup" && action != "diagnose" && action != "image_shape" && action != "images" {
		out.Code = "INVALID_ACTION"
		return
	}
	host, e := normalizeHost(c.Host)
	if e != nil {
		out.Code = e.Error()
		return
	}
	out.ExperimentID = c.ExperimentID
	out.Name = prefix + c.ExperimentID
	out.Root = parentRoot + "/" + out.Name
	if !apply {
		out.Status = "dry_run"
		out.Payload = createBody(receipt{Name: out.Name, Root: out.Root})
		out.Note = "No network or file writes. Apply requires exclusive synthetic directory prepared without symlinks; no existing library may overlap."
		return
	}
	if c.Username == "" || c.Password == "" || len(c.Username) > 4096 || len(c.Password) > 4096 {
		out.Code = "CREDENTIALS_REQUIRED"
		return
	}
	// Serialize this experiment even across runner processes. A stale lock fails closed.
	lock, e := os.OpenFile(receiptPath(c.ExperimentID)+".lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		out.Code = "EXPERIMENT_LOCKED"
		return
	}
	lock.Close()
	defer os.Remove(receiptPath(c.ExperimentID) + ".lock")
	a := api{host: host, client: &http.Client{Timeout: 12 * time.Second, Transport: logging.Transport(nil), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	defer func() { out.Requests = a.requests; sanitize(&out, []string{c.Username, c.Password, a.token}) }()
	var auth struct {
		Token string `json:"token"`
	}
	if e = a.call(ctx, "POST", "/login", map[string]string{"username": c.Username, "password": c.Password, "app_name": "trimemedia-web"}, &auth); e != nil {
		out.Code = e.Error()
		return
	}
	if auth.Token == "" || len(auth.Token) > 8192 || strings.ContainsAny(auth.Token, "\r\n") {
		out.Code = "INVALID_TOKEN"
		return
	}
	a.token = auth.Token
	var user struct {
		Admin *int `json:"is_admin"`
	}
	if e = a.call(ctx, "GET", "/user/info", nil, &user); e != nil {
		out.Code = e.Error()
		return
	}
	if user.Admin == nil || *user.Admin != 1 {
		out.Code = "ADMIN_REQUIRED"
		return
	}
	var r receipt
	if action == "prepare" {
		if !c.FilesPrepared {
			out.Code = "EXCLUSIVE_SYNTHETIC_DIRECTORY_REQUIRED"
			return
		}
		if _, e = os.Lstat(receiptPath(c.ExperimentID)); e == nil {
			out.Code = "RECEIPT_EXISTS_USE_OBSERVE_OR_CLEANUP"
			return
		} else if !os.IsNotExist(e) {
			out.Code = "RECEIPT_UNAVAILABLE"
			return
		}
		libs, err := a.libraries(ctx)
		if err != nil {
			out.Code = err.Error()
			return
		}
		r, err = newReceipt(c.ExperimentID, host, libs)
		if err != nil {
			out.Code = err.Error()
			return
		}
		if err = saveReceipt(r); err != nil {
			out.Code = err.Error()
			return
		}
		// Never retry create. If transport is ambiguous, use observe to reconcile the receipt.
		if e = a.call(ctx, "PUT", "/mdb/create", createBody(r), nil); e != nil {
			out.Code = e.Error()
			out.Note = "Create outcome may be unknown; receipt retained. Observe or cleanup; never repeat prepare."
			return
		}
	} else {
		r, e = loadReceipt(c.ExperimentID, host)
		if e != nil {
			out.Code = e.Error()
			return
		}
		if r.Phase == "cleaned" {
			out.Code = "ALREADY_CLEANED"
			return
		}
	}
	if action == "diagnose" {
		libs, err := a.libraries(ctx)
		if err != nil {
			out.Code = err.Error()
			return
		}
		out.BaselineUnchanged = unchanged(r, libs)
		for _, l := range libs {
			if strings.HasPrefix(l.Name, r.Name) {
				out.Candidates = append(out.Candidates, l)
			}
		}
		out.Status = "diagnosed"
		out.Note = "Read-only candidate scope only; no library create, scan, update or delete. Receipt was not changed."
		return
	}
	// Reconcile a lost delete response without issuing a second delete.
	if action == "cleanup" && (r.Phase == "delete_requested" || r.Phase == "planned") {
		current, err := a.libraries(ctx)
		if err != nil {
			out.Code = err.Error()
			return
		}
		present := false
		for _, l := range current {
			if l.Name == r.Name || (r.OwnedGUID != "" && l.GUID == r.OwnedGUID) {
				present = true
			}
		}
		if !present {
			out.BaselineUnchanged = unchanged(r, current)
			if !out.BaselineUnchanged {
				out.Code = "BASELINE_CHANGED"
				return
			}
			r.Phase = "cleaned"
			if err = saveReceipt(r); err != nil {
				out.Code = err.Error()
				return
			}
			out.Status = "cleaned"
			out.Note = "Library absence reconciled without further mutation; only own synthetic files may be removed."
			return
		}
	}
	l, libs, e := a.verifyOwn(ctx, r, action == "prepare" || action == "scan")
	if e != nil {
		out.Code = e.Error()
		return
	}
	r.OwnedGUID = l.GUID
	out.OwnedGUID = l.GUID
	out.BaselineUnchanged = unchanged(r, libs)
	if !out.BaselineUnchanged {
		out.Code = "BASELINE_CHANGED"
		return
	}
	if e = saveReceipt(r); e != nil {
		out.Code = e.Error()
		return
	}
	switch action {
	case "prepare":
		r.Phase = "prepared"
		out.Status = "prepared"
		out.Note = "Only the isolated library was created. Creation may automatically scan it. No scanall or existing-library write was requested."
	case "scan":
		if r.Phase == "scan_requested" || r.Phase == "scanned" {
			out.Code = "SCAN_ALREADY_REQUESTED"
			return
		}
		r.Phase = "scan_requested"
		if e = saveReceipt(r); e != nil {
			out.Code = e.Error()
			return
		}
		if e = a.call(ctx, "POST", "/mdb/scan/"+r.OwnedGUID, map[string]any{"dir_list": []string{r.Root}}, nil); e != nil {
			out.Code = e.Error()
			return
		}
		r.Phase = "scanned"
		out.Status = "scan_accepted"
		out.Note = "Accepted only; scan completion and NFO import require observe."
	case "image_shape", "images":
		out.Items, e = a.images(ctx, r, action == "images", c.FixtureDir)
		if e != nil {
			out.Code = e.Error()
			return
		}
		out.Status = "observed"
		out.Note = "Only image references returned by this owned library were inspected. A nonempty reference is not byte verification; each fetched image reports a separate hash/match result."
	case "observe":
		out.Items, e = a.observe(ctx, r)
		if e != nil {
			out.Code = e.Error()
			return
		}
		out.Status = "observed"
		out.Note = "Synthetic experiment observations only; no real metadata IDs were supplied. Empty or mismatched metadata is not an NFO pass."
	case "cleanup":
		r.Phase = "delete_requested"
		if e = saveReceipt(r); e != nil {
			out.Code = e.Error()
			return
		}
		if e = a.call(ctx, "DELETE", "/mdb/"+r.OwnedGUID, nil, nil); e != nil {
			out.Code = e.Error()
			return
		}
		remaining, err := a.libraries(ctx)
		if err != nil {
			out.Code = err.Error()
			return
		}
		for _, v := range remaining {
			if v.GUID == r.OwnedGUID || v.Name == r.Name {
				out.Code = "DELETE_NOT_CONFIRMED"
				return
			}
		}
		out.BaselineUnchanged = unchanged(r, remaining)
		if !out.BaselineUnchanged {
			out.Code = "BASELINE_CHANGED"
			return
		}
		r.Phase = "cleaned"
		out.Status = "cleaned"
		out.Note = "Temporary library absence verified. Runner does not delete files; remove only this experiment's own synthetic directory separately."
	}
	if e = saveReceipt(r); e != nil {
		out.Status = "failed"
		out.Code = e.Error()
	}
	return
}

type observedNode struct {
	guid, parent, kind string
	depth              int
}

func stringField(m map[string]json.RawMessage, k string) string {
	var s string
	_ = json.Unmarshal(m[k], &s)
	return s
}
func (a *api) observe(ctx context.Context, r receipt) ([]map[string]any, error) {
	var page struct {
		List []map[string]json.RawMessage `json:"list"`
	}
	if e := a.call(ctx, "POST", "/item/list", map[string]any{"ancestor_guid": r.OwnedGUID, "tags": map[string]any{"type": []string{"Movie", "TV", "Video"}}, "sort_type": "DESC", "sort_column": "create_time", "page": 1, "page_size": 8, "exclude_grouped_video": 0}, &page); e != nil {
		return nil, e
	}
	if page.List == nil || len(page.List) > 8 {
		return nil, errors.New("INVALID_OR_EXCESS_ITEMS")
	}
	queue := []observedNode{}
	seen := map[string]observedNode{}
	for _, v := range page.List {
		queue = append(queue, observedNode{guid: stringField(v, "guid"), parent: r.OwnedGUID, kind: stringField(v, "type")})
	}
	out := []map[string]any{}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if !guidPattern.MatchString(n.guid) {
			return out, errors.New("INVALID_ITEM_GUID")
		}
		if old, ok := seen[n.guid]; ok {
			if old.parent != n.parent || old.depth != n.depth || old.kind != n.kind {
				return out, errors.New("ITEM_TREE_CONFLICT")
			}
			continue
		}
		seen[n.guid] = n
		if len(seen) > 10 {
			return out, errors.New("ITEM_TREE_LIMIT")
		}
		var detail map[string]json.RawMessage
		if e := a.call(ctx, "GET", "/item/"+n.guid, nil, &detail); e != nil {
			return out, e
		}
		if detail == nil {
			return out, errors.New("INVALID_ITEM_DETAIL")
		}
		if err := verifyItemScope(detail, r, n); err != nil {
			return append(out, scopeDiagnostic(detail, n)), err
		}
		kind := stringField(detail, "type")
		summary := selected(detail, []string{"guid", "type", "title", "original_title", "tv_title", "trim_id", "overview", "plot", "tagline", "release_date", "premiere_date", "first_air_date", "air_date", "year", "season_number", "episode_number", "vote_average", "runtime", "genres", "genre", "country", "studio", "uniqueid", "uniqueids", "poster_width", "poster_height", "backdrop_width", "backdrop_height", "number_of_seasons", "number_of_episodes", "local_number_of_seasons", "local_number_of_episodes"})
		summary["parent_guid"] = stringField(detail, "parent_guid")
		summary["ancestor_guid"] = stringField(detail, "ancestor_guid")
		summary["scope_verified"] = true
		summary["tree_depth"] = n.depth
		keys := []string{}
		for k := range detail {
			if len(k) <= 64 && fieldPattern.MatchString(k) {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		if len(keys) > 80 {
			keys = keys[:80]
		}
		summary["detail_keys"] = keys
		images := map[string]any{}
		for _, k := range []string{"poster", "posters", "poster_path", "backdrop", "backdrops", "backdrop_path", "fanart", "thumb", "stills", "logo"} {
			if b, ok := detail[k]; ok {
				var s string
				var list []json.RawMessage
				v := map[string]any{"present": !bytes.Equal(bytes.TrimSpace(b), []byte("null"))}
				if json.Unmarshal(b, &s) == nil {
					v["kind"] = "string"
					v["present"] = s != ""
				} else if json.Unmarshal(b, &list) == nil && list != nil {
					v["kind"] = "array"
					v["count"] = len(list)
					v["present"] = len(list) > 0
				} else {
					v["kind"] = "other"
				}
				images[k] = v
			}
		}
		summary["image_fields"] = images
		summary["image_bytes_verified"] = false
		out = append(out, summary)
		var persons struct {
			List []map[string]json.RawMessage `json:"list"`
		}
		if e := a.call(ctx, "POST", "/person/list/"+n.guid, map[string]int{"page": 1, "page_size": 20}, &persons); e != nil {
			summary["persons_status"] = e.Error()
			return out, e
		}
		if persons.List == nil || len(persons.List) > 20 {
			summary["persons_status"] = "invalid_shape"
			return out, errors.New("INVALID_OR_EXCESS_PERSONS")
		}
		cast := []map[string]any{}
		for _, p := range persons.List {
			v := selected(p, []string{"name", "original_name", "person_guid", "job", "role", "order"})
			v["profile_present"] = stringField(p, "profile_path") != ""
			cast = append(cast, v)
		}
		summary["persons"] = cast
		summary["persons_status"] = "observed"
		endpoint := ""
		expected := ""
		if kind == "TV" && n.depth == 0 {
			endpoint = "/season/list/" + n.guid
			expected = "Season"
		} else if kind == "Season" && n.depth == 1 {
			endpoint = "/episode/list/" + n.guid
			expected = "Episode"
		}
		if endpoint != "" {
			var children []map[string]json.RawMessage
			if e := a.call(ctx, "GET", endpoint, nil, &children); e != nil {
				return out, e
			}
			if children == nil || len(children) > 3 {
				return out, errors.New("INVALID_OR_EXCESS_CHILDREN")
			}
			summary["children_count"] = len(children)
			for _, child := range children {
				if stringField(child, "type") != "" && stringField(child, "type") != expected {
					return out, errors.New("CHILD_TYPE_MISMATCH")
				}
				queue = append(queue, observedNode{guid: stringField(child, "guid"), parent: n.guid, kind: expected, depth: n.depth + 1})
			}
		}
	}
	return out, nil
}

var fieldPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
var remoteURL = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)

func selected(m map[string]json.RawMessage, keys []string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		b, ok := m[k]
		if !ok {
			continue
		}
		var s string
		if json.Unmarshal(b, &s) == nil {
			if len(s) > 8192 {
				s = "[OVERSIZED]"
			}
			out[k] = s
			continue
		}
		var n float64
		if json.Unmarshal(b, &n) == nil && n >= 0 && n <= 1e9 {
			out[k] = n
			continue
		}
		var list []string
		if json.Unmarshal(b, &list) == nil && list != nil && len(list) <= 20 {
			out[k] = list
		}
	}
	return out
}
func sanitize(out *output, secrets []string) {
	variants := []string{}
	for _, s := range secrets {
		if s != "" {
			variants = append(variants, s, url.QueryEscape(s), url.PathEscape(s))
		}
	}
	sort.Slice(variants, func(i, j int) bool { return len(variants[i]) > len(variants[j]) })
	clean := func(s string) string {
		if len(s) > 8192 {
			return "[OVERSIZED]"
		}
		for _, v := range variants {
			s = strings.ReplaceAll(s, v, "[REDACTED]")
		}
		s = remoteURL.ReplaceAllString(s, "[URL REDACTED]")
		s = strings.Map(func(r rune) rune {
			if r < 32 || r == 127 {
				return ' '
			}
			return r
		}, s)
		r := []rune(s)
		if len(r) > 512 {
			s = string(r[:512]) + "…"
		}
		return s
	}
	var walk func(any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case string:
			return clean(x)
		case []string:
			for i := range x {
				x[i] = clean(x[i])
			}
			return x
		case []map[string]any:
			for _, m := range x {
				for k, v := range m {
					m[k] = walk(v)
				}
			}
			return x
		case map[string]any:
			for k, v := range x {
				x[k] = walk(v)
			}
			return x
		}
		return v
	}
	for _, m := range out.Items {
		walk(m)
	}
	for i := range out.Candidates {
		l := &out.Candidates[i]
		l.Name = clean(l.Name)
		l.Category = clean(l.Category)
		l.GUID = clean(l.GUID)
		for j := range l.Dirs {
			l.Dirs[j] = clean(l.Dirs[j])
		}
	}
	out.OwnedGUID = clean(out.OwnedGUID)
	out.Name = clean(out.Name)
	out.Root = clean(out.Root)
	out.Note = clean(out.Note)
}
func main() {
	trace, err := logging.NewRun(os.Stderr)
	if err != nil {
		json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "trace_initialization_failed"})
		os.Exit(1)
	}
	flags := flag.NewFlagSet("fnos-nfo-experiment", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	apply := flags.Bool("apply", false, "apply isolated action")
	action := flags.String("action", "prepare", "prepare, scan, observe, cleanup, diagnose, image_shape, images")
	var c config
	b, e := io.ReadAll(io.LimitReader(os.Stdin, 65537))
	if flags.Parse(os.Args[1:]) != nil || flags.NArg() != 0 || e != nil || len(b) > 65536 || json.Unmarshal(b, &c) != nil {
		_ = trace.Report(os.Stdout, output{Status: "failed", Code: "INVALID_INPUT"}, "invalid_input")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(trace.Context(context.Background()), 90*time.Second)
	defer cancel()
	out := run(ctx, c, *action, *apply)
	if trace.Report(os.Stdout, out, out.Status, c.Username, c.Password) != nil {
		os.Exit(1)
	}
	if out.Status == "failed" {
		os.Exit(1)
	}
}
