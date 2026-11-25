package torrenttransfer

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"light-media/builtinplugins/internal/sdk"
)

// sdkHost exists only during one SDK handler. All downloader credentials and
// daemon HTTP calls remain behind the host's canonical connection guard.
type sdkHost struct{ caller plugins.HostCaller }

func call(ctx context.Context, h plugins.HostCaller, method string, in, out any) error {
	if h == nil {
		return errors.New("active host caller required")
	}
	raw, e := json.Marshal(in)
	if e != nil {
		return e
	}
	raw, e = h(ctx, method, raw)
	if e != nil {
		return e
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}
func configurations(ctx context.Context, h plugins.HostCaller) (plugins.DownloaderConfigurationsV1, error) {
	return (plugins.Downloaders{Host: h}).Configurations(ctx)
}
func canonicalKind(kind string) string {
	switch kind {
	case "qb":
		return "qbittorrent"
	case "tr":
		return "transmission"
	}
	return kind
}
func (h sdkHost) Service(ctx context.Context, name string) (*Service, error) {
	configs, e := configurations(ctx, h.caller)
	if e != nil {
		return nil, e
	}
	var metadata *plugins.BrushDownloaderReferenceV1
	runtime := map[string]plugins.BrushDownloaderReferenceV1{}
	for _, ref := range configs.Downloaders {
		if !ref.Enabled || ref.Name == "" || ref.Kind == "" || ref.Name != name {
			continue
		}
		value := ref
		metadata = &value
		kind := canonicalKind(ref.Kind)
		if kind == "qbittorrent" || kind == "transmission" {
			runtime[kind] = ref
		}
	}
	if metadata == nil {
		return nil, nil
	}
	// Actual ModuleHelper/pkgutil iteration yields QB before TR. Each module's
	// name dictionary retains the last eligible config of its own kind, while
	// ServiceInfo.config/type comes from the last global name configuration.
	for _, kind := range []string{"qbittorrent", "transmission"} {
		ref, ok := runtime[kind]
		if !ok {
			continue
		}
		connection, e := (plugins.Downloaders{Host: h.caller}).SourceConnection(ctx, plugins.DownloaderConnectionRequestV1{DownloaderID: ref.ID})
		if e != nil {
			return nil, e
		}
		if !connection.Exists {
			continue
		}
		if connection.Kind != kind {
			return nil, errors.New("downloader runtime kind changed")
		}
		return &Service{Name: name, Type: canonicalKind(metadata.Kind), RuntimeKind: kind, Ref: ref.ID, Inactive: connection.Inactive}, nil
	}
	return nil, nil
}
func (h sdkHost) Exists(ctx context.Context, path string) (bool, error) {
	if e := ctx.Err(); e != nil {
		return false, e
	}
	if strings.ContainsRune(path, 0) {
		return false, nil
	}
	_, e := os.Stat(path)
	if e == nil {
		return true, nil
	}
	for _, ignored := range []error{syscall.ENOENT, syscall.ENOTDIR, syscall.EBADF, syscall.ELOOP} {
		if errors.Is(e, ignored) {
			return false, nil
		}
	}
	return false, e
}
func (h sdkHost) ReadFile(ctx context.Context, path string) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	return os.ReadFile(path)
}
func (h sdkHost) WriteTemp(ctx context.Context, name string, raw []byte) (string, error) {
	if filepath.Base(name) != name {
		return "", errors.New("invalid temporary torrent name")
	}
	root, e := (plugins.Base{Host: h.caller}).GetDataPath(ctx)
	if e != nil {
		return "", e
	}
	if !filepath.IsAbs(root) {
		return "", errors.New("instance data path must be absolute")
	}
	if e = os.MkdirAll(root, 0700); e != nil {
		return "", e
	}
	namespace, e := os.OpenRoot(root)
	if e != nil {
		return "", e
	}
	defer namespace.Close()
	if e = namespace.MkdirAll("tmp", 0700); e != nil {
		return "", e
	}
	relative := filepath.Join("tmp", name)
	// The host instance namespace is also the filesystem boundary: an existing
	// symlink cannot redirect a clone's repaired torrent into a sibling instance.
	if e = namespace.WriteFile(relative, raw, 0600); e != nil {
		return "", e
	}
	path := filepath.Join(root, relative)
	return path, nil
}
func projectTasks(rows plugins.DownloaderTasksV1) []Torrent {
	out := []Torrent{}
	for _, row := range rows.Tasks {
		t := Torrent{}
		if q := row.QBittorrent; q != nil {
			t["hash"] = row.Hash
			if q.SavePath != nil {
				t["save_path"] = *q.SavePath
			}
			if q.Tags != nil {
				t["tags"] = *q.Tags
			}
			if q.Category != nil {
				t["category"] = *q.Category
			}
			if q.State != nil {
				t["state"] = *q.State
			}
		} else if tr := row.Transmission; tr != nil {
			t["hashString"] = row.Hash
			if tr.DownloadDir != nil {
				t["download_dir"] = *tr.DownloadDir
			}
			if tr.Labels != nil {
				t["labels"] = *tr.Labels
			}
			if tr.Status != nil {
				t["status"] = map[string]any{"stopped": tr.Status.String() == "0"}
			}
			if tr.PercentDone != nil {
				t["percent_done"] = *tr.PercentDone
			}
		}
		out = append(out, t)
	}
	return out
}
func (h sdkHost) Completed(ctx context.Context, s Service) ([]Torrent, error) {
	out, e := (plugins.Downloaders{Host: h.caller}).SourceCompleted(ctx, plugins.DownloaderCompletedRequestV1{DownloaderID: s.Ref})
	if e != nil {
		return nil, e
	}
	if out.Kind != s.actualKind() {
		return nil, errors.New("downloader kind changed")
	}
	return projectTasks(out.DownloaderTasksV1), nil
}
func (h sdkHost) Torrents(ctx context.Context, s Service, ids []string) ([]Torrent, error) {
	out, e := (plugins.Downloaders{Host: h.caller}).SourceTasks(ctx, plugins.DownloaderTasksRequestV1{DownloaderID: s.Ref, Hashes: ids})
	if e != nil {
		return nil, e
	}
	if out.Kind != s.actualKind() {
		return nil, errors.New("downloader kind changed")
	}
	return projectTasks(out.DownloaderTasksV1), nil
}
func mutationError(out plugins.DownloaderMutationV1, e error) error {
	if e != nil {
		return e
	}
	if out.FailureCode != "" {
		return errors.New(out.FailureCode)
	}
	// Plain unknown retains the host's fact and is never retried here. The
	// source wrapper still returns its false/tuple outcome; only scoped errors
	// and explicit post-mutation/partial fences terminate the source chain.
	return nil
}
func (h sdkHost) AddPaused(ctx context.Context, s Service, spec AddSpec) (AddResult, error) {
	if s.Type == "qbittorrent" && s.actualKind() == "transmission" {
		// The source chooses QB kwargs from metadata, but the actual TR method
		// has no **kwargs. Binding rejects tag before entering its native body.
		return AddResult{}, errors.New("Transmission.add_torrent() got an unexpected keyword argument 'tag'")
	}
	save, e := asString(spec.SavePath)
	if e != nil {
		return AddResult{}, e
	}
	req := plugins.DownloaderAddRequestV1{DownloaderID: s.Ref, Torrent: spec.Content, Paused: true, SavePath: &save, Tags: spec.Tags}
	sourceReq := plugins.DownloaderSourceAddRequestV1{DownloaderAddRequestV1: req}
	if s.Type == "transmission" && s.actualKind() == "qbittorrent" {
		sourceReq.Tags = nil
		sourceReq.QBLabels = append([]string{}, spec.Tags...)
	}
	if s.Type == "qbittorrent" {
		wire, e := sourceSkipWire(spec.SkipCheckingRaw, spec.SkipChecking)
		if e != nil {
			return AddResult{SourceTruthy: true, Outcome: "rejected"}, nil
		}
		sourceReq.SkipCheckingWire = wire
		if spec.Category != nil {
			v, e := asString(spec.Category)
			if e != nil {
				return AddResult{}, e
			}
			sourceReq.Category = &v
		}
	}
	out, e := (plugins.Downloaders{Host: h.caller}).SourceAdd(ctx, sourceReq)
	if e = mutationError(out.DownloaderMutationV1, e); e != nil {
		return AddResult{}, e
	}
	// The installed qB Python wrapper always returns a two-element tuple,
	// including (False,[]). Every well-formed return is truthy to this plugin.
	return AddResult{Accepted: out.Accepted, SourceTruthy: s.actualKind() == "qbittorrent" || out.Accepted && !out.NativeError, Hash: out.Hash, Outcome: out.Outcome}, nil
}
func (h sdkHost) FindByTag(ctx context.Context, s Service, tag string) (string, error) {
	// The finite host helper owns nine 3-second attempts and marker removal.
	out, e := (plugins.Downloaders{Host: h.caller}).SourceFindByTag(ctx, plugins.DownloaderFindByTagRequestV1{DownloaderID: s.Ref, Tags: []string{tag}})
	if e != nil || out.Hash == nil {
		return "", e
	}
	return *out.Hash, nil
}
func (h sdkHost) effect(ctx context.Context, s Service, ids []string, action string) (EffectResult, error) {
	out, e := (plugins.Downloaders{Host: h.caller}).SourceControl(ctx, plugins.DownloaderControlRequestV1{DownloaderID: s.Ref, Hashes: ids, Action: action})
	if e = mutationError(out.DownloaderMutationV1, e); e != nil {
		return EffectResult{}, e
	}
	return EffectResult{Accepted: out.Accepted && !out.NativeError, Outcome: out.Outcome}, nil
}
func (h sdkHost) Recheck(ctx context.Context, s Service, ids []string) (EffectResult, error) {
	return h.effect(ctx, s, ids, "recheck")
}
func (h sdkHost) Start(ctx context.Context, s Service, ids []string) (EffectResult, error) {
	return h.effect(ctx, s, ids, "start")
}
func (h sdkHost) DeleteSource(ctx context.Context, s Service, id string) (EffectResult, error) {
	return h.effect(ctx, s, []string{id}, "remove_task")
}
func (h sdkHost) SaveData(ctx context.Context, key string, value any) error {
	return (plugins.Base{Host: h.caller}).SaveData(ctx, key, value)
}
func (h sdkHost) SaveConfig(ctx context.Context, value map[string]any) error {
	_, e := (plugins.Base{Host: h.caller}).UpdateConfig(ctx, value)
	return e
}
func (h sdkHost) SystemMessage(ctx context.Context, title, message string) error {
	return call(ctx, h.caller, plugins.SystemMessagePutCapability, map[string]any{"title": title, "message": message}, nil)
}
func (h sdkHost) Notify(ctx context.Context, n Notice) error {
	_, e := (plugins.Base{Host: h.caller}).PostMessage(ctx, map[string]any{"mtype": n.Type, "title": n.Title, "text": n.Text})
	return e
}
func (h sdkHost) RandomTag(ctx context.Context, n int) (string, error) {
	const chars = "ABCDEFGHIGKLMNOPQRSTUVWXYZabcdefghigklmnopqrstuvwxyz0123456789"
	out := make([]byte, n)
	// Sample the source's 62 positions: G/g are duplicated and J/j absent.
	for i := 0; i < n; {
		var b [1]byte
		if _, e := rand.Read(b[:]); e != nil {
			return "", e
		}
		if b[0] >= 248 {
			continue
		}
		out[i] = chars[int(b[0])%len(chars)]
		i++
	}
	return string(out), ctx.Err()
}
func (h sdkHost) Log(ctx context.Context, l Log) {
	_ = (plugins.Logger{Host: h.caller}).Log(ctx, plugins.LogLevel(l.Level), l.Message)
}
