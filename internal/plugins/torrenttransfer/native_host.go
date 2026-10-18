package torrenttransfer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	download "light-media/internal/plugins/iyuuautoseed/native"
)

type NativeBinding struct {
	Service Service
	Config  download.Config
	Options download.IYUUOptions
}
type AuthorityGrant struct{ ServiceRef, BindingDigest, Hash, ReceiptID, Kind, ServiceName string }

func hexDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, e := hex.DecodeString(value)
	return e == nil
}

func NativeBindingDigest(b NativeBinding) string {
	raw, _ := json.Marshal(struct {
		Service                             Service
		Config                              download.Config
		APIKey                              string
		AutoCategory, Sequential, FirstLast bool
	}{b.Service, b.Config, b.Options.APIKey, b.Options.AutoCategory, b.Options.Sequential, b.Options.FirstLast})
	return digest(raw)
}

type NativeHostOptions struct {
	Context  context.Context
	Run      *JournalRun
	Bindings []NativeBinding
	Config   Config
	// The trusted App supplies an owned per-run directory, never a worker path.
	TempDir        string
	PendingProofs  []AuthorityGrant
	NoticeAccepted func(context.Context, string) error
	SystemAccepted func(context.Context, string) error
	Logger         func(context.Context, Log)
}
type nativeService struct {
	binding NativeBinding
	client  *download.IYUUClient
}
type NativeHost struct {
	run                            *JournalRun
	config                         Config
	services                       map[string]*nativeService
	byName                         map[string]*nativeService
	backup, temp                   string
	backupRoot, tempRoot           *os.Root
	backupErr                      error
	mu                             sync.Mutex
	sources                        map[string]Torrent
	contentSource                  map[string]string
	tempSources                    map[string]string
	tags                           map[string]string
	tagReceipts                    map[string]string
	tagDest                        map[string]download.IYUUDestination
	tagAccepted                    map[string]bool
	duplicates                     map[string]bool
	proofs                         map[string]AuthorityGrant
	destinations                   map[string]download.IYUUDestination
	noticeAccepted, systemAccepted func(context.Context, string) error
	logger                         func(context.Context, Log)
}

func NewNativeHost(opt NativeHostOptions) (*NativeHost, error) {
	if opt.Run == nil || opt.TempDir == "" || !filepath.IsAbs(opt.TempDir) {
		return nil, errors.New("transfer durable owner and owned temp root required")
	}
	ctx := opt.Context
	if ctx == nil {
		ctx = context.Background()
	}
	backup, e := opt.Config.string("fromtorrentpath")
	if e != nil {
		return nil, e
	}
	snapshot, e := opt.Config.Snapshot()
	if e != nil {
		return nil, e
	}
	type frozen struct {
		Service                             Service
		Config                              download.Config
		APIKey                              string
		AutoCategory, Sequential, FirstLast bool
	}
	bs := []frozen{}
	for _, b := range opt.Bindings {
		bs = append(bs, frozen{b.Service, b.Config, b.Options.APIKey, b.Options.AutoCategory, b.Options.Sequential, b.Options.FirstLast})
	}
	if e = opt.Run.bind(ctx, "transfer-native-host", struct {
		Bindings []frozen
		Config   map[string]any
		Temp     string
		Proofs   []AuthorityGrant
	}{bs, snapshot, opt.TempDir, opt.PendingProofs}); e != nil {
		return nil, e
	}
	h := &NativeHost{run: opt.Run, config: opt.Config, services: map[string]*nativeService{}, byName: map[string]*nativeService{}, backup: filepath.Clean(backup), temp: filepath.Clean(opt.TempDir), sources: map[string]Torrent{}, contentSource: map[string]string{}, tempSources: map[string]string{}, tags: map[string]string{}, tagReceipts: map[string]string{}, tagDest: map[string]download.IYUUDestination{}, tagAccepted: map[string]bool{}, duplicates: map[string]bool{}, proofs: map[string]AuthorityGrant{}, destinations: map[string]download.IYUUDestination{}, noticeAccepted: opt.NoticeAccepted, systemAccepted: opt.SystemAccepted, logger: opt.Logger}
	if backup != "" {
		h.backupRoot, h.backupErr = os.OpenRoot(backup)
	}
	if e = os.MkdirAll(opt.TempDir, 0700); e != nil {
		h.Close()
		return nil, e
	}
	h.tempRoot, e = os.OpenRoot(opt.TempDir)
	if e != nil {
		h.Close()
		return nil, e
	}
	for _, b := range opt.Bindings {
		if b.Service.Name == "" || b.Service.Ref == "" || b.Config.Kind != b.Service.Type || h.services[b.Service.Ref] != nil || h.byName[b.Service.Name] != nil {
			h.Close()
			return nil, errors.New("invalid transfer native binding")
		}
		b.Options.Guard = opt.Run.guard
		c, e := download.NewIYUUClient(b.Config, b.Options)
		if e != nil {
			h.Close()
			return nil, e
		}
		s := &nativeService{b, c}
		h.services[b.Service.Ref] = s
		h.byName[b.Service.Name] = s
	}
	for _, p := range opt.PendingProofs {
		if p.ServiceRef == "" || p.ServiceName == "" || p.Kind != "add" || !hexDigest(p.BindingDigest) || !download.TransferValidHash(p.Hash) || p.ReceiptID == "" {
			h.Close()
			return nil, ErrReplayDiverged
		}
		h.proofs[p.ServiceRef+"/"+p.Hash] = p
	}
	return h, nil
}
func (h *NativeHost) Close() {
	for _, s := range h.services {
		s.client.Close()
	}
	if h.backupRoot != nil {
		_ = h.backupRoot.Close()
	}
	if h.tempRoot != nil {
		_ = h.tempRoot.Close()
	}
}
func (h *NativeHost) Log(ctx context.Context, l Log) {
	if h.logger != nil {
		h.logger(ctx, l)
	}
}
func (h *NativeHost) entry(s Service) (*nativeService, error) {
	v := h.services[s.Ref]
	if v == nil || v.binding.Service != s {
		return nil, errors.New("transfer service is not in current owner inventory")
	}
	return v, nil
}
func (h *NativeHost) Service(ctx context.Context, name string) (*Service, error) {
	if e := h.run.guard(ctx); e != nil {
		return nil, e
	}
	s := h.byName[name]
	if s == nil {
		return nil, nil
	}
	v := s.binding.Service
	if !v.Inactive {
		var active bool
		e := h.run.step(ctx, "transfer-service-connected", v.Ref, false, &active, func() (any, string, error) {
			e := s.client.TransferConnected(ctx)
			if ctx.Err() != nil {
				return nil, "read", ctx.Err()
			}
			return e == nil, "read", nil
		})
		if e != nil {
			return nil, e
		}
		v.Inactive = !active
	}
	return &v, nil
}
func (h *NativeHost) file(p string) (*os.Root, string, string, error) {
	p = filepath.Clean(p)
	if p == h.backup {
		return h.backupRoot, ".", "root", nil
	}
	if filepath.Dir(p) == h.backup {
		base := filepath.Base(p)
		ext := filepath.Ext(base)
		hash := strings.TrimSuffix(base, ext)
		if (ext != ".torrent" && ext != ".fastresume") || !download.TransferValidHash(hash) {
			return nil, "", "", errors.New("transfer file selector is not a source torrent hash")
		}
		h.mu.Lock()
		_, ok := h.sources[hash]
		h.mu.Unlock()
		if !ok {
			return nil, "", "", errors.New("transfer backup file has no completed source observation")
		}
		return h.backupRoot, base, hash, nil
	}
	if filepath.Dir(p) == h.temp {
		base := filepath.Base(p)
		h.mu.Lock()
		hash := h.tempSources[base]
		h.mu.Unlock()
		if hash == "" {
			return nil, "", "", errors.New("transfer temporary file has no rebuild receipt")
		}
		return h.tempRoot, base, hash, nil
	}
	return nil, "", "", errors.New("transfer file is outside exact configured backup and owned temp roots")
}
func (h *NativeHost) Exists(ctx context.Context, p string) (bool, error) {
	var out bool
	e := h.run.step(ctx, "transfer-file-exists", p, false, &out, func() (any, string, error) {
		root, name, _, e := h.file(p)
		if e != nil {
			return nil, "read", e
		}
		if root == nil {
			if os.IsNotExist(h.backupErr) {
				return false, "read", nil
			}
			if filepath.Clean(p) == h.backup {
				_, e = os.Stat(h.backup)
				if os.IsNotExist(e) {
					return false, "read", nil
				}
				return e == nil, "read", e
			}
			return false, "read", h.backupErr
		}
		_, e = root.Stat(name)
		if os.IsNotExist(e) {
			return false, "read", nil
		}
		return e == nil, "read", e
	})
	return out, e
}
func (h *NativeHost) ReadFile(ctx context.Context, p string) ([]byte, error) {
	root, name, source, e := h.file(p)
	if e != nil {
		return nil, e
	}
	var out []byte
	e = h.run.step(ctx, "transfer-read-backup", p, false, &out, func() (any, string, error) {
		if root == nil {
			return nil, "read", h.backupErr
		}
		f, e := root.Open(name)
		if e != nil {
			return nil, "read", e
		}
		defer f.Close()
		st, e := f.Stat()
		if e != nil || !st.Mode().IsRegular() || st.Size() > 16<<20 {
			return nil, "read", errors.New("transfer backup is not bounded regular file")
		}
		b, e := io.ReadAll(io.LimitReader(f, (16<<20)+1))
		if e != nil || len(b) > 16<<20 {
			return nil, "read", errors.New("transfer backup exceeds read bound")
		}
		return b, "read", nil
	})
	if e == nil && strings.HasSuffix(p, ".torrent") {
		h.mu.Lock()
		h.contentSource[digest(out)] = source
		h.mu.Unlock()
	}
	return out, e
}
func (h *NativeHost) WriteTemp(ctx context.Context, name string, content []byte) (string, error) {
	if filepath.Base(name) != name || !strings.HasSuffix(name, ".torrent") || !download.TransferValidHash(strings.TrimSuffix(name, ".torrent")) {
		return "", errors.New("transfer rebuilt torrent selector denied")
	}
	hash := strings.TrimSuffix(name, ".torrent")
	h.mu.Lock()
	_, observed := h.sources[hash]
	h.mu.Unlock()
	if !observed {
		return "", errors.New("transfer rebuilt torrent has no source")
	}
	var out string
	e := h.run.step(ctx, "transfer-rebuilt-torrent", struct {
		Name    string
		Content []byte
	}{name, content}, false, &out, func() (any, string, error) {
		if e := h.run.guard(ctx); e != nil {
			return nil, "read", e
		}
		f, e := h.tempRoot.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if errors.Is(e, os.ErrExist) {
			f, e = h.tempRoot.Open(name)
			if e != nil {
				return nil, "read", e
			}
			b, e := io.ReadAll(io.LimitReader(f, (16<<20)+1))
			f.Close()
			if e != nil || digest(b) != digest(content) {
				return nil, "read", ErrReplayDiverged
			}
			return filepath.Join(h.temp, name), "read", nil
		}
		if e != nil {
			return nil, "read", e
		}
		_, e = f.Write(content)
		if e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e == nil {
			e = ce
		}
		return filepath.Join(h.temp, name), "read", e
	})
	if e == nil {
		h.mu.Lock()
		h.tempSources[name] = hash
		h.contentSource[digest(content)] = hash
		h.mu.Unlock()
	}
	return out, e
}
func nativeRows(raw []byte) ([]Torrent, error) {
	v, e := decode(raw)
	if e != nil {
		return nil, e
	}
	a, ok := v.([]any)
	if !ok {
		return nil, errors.New("invalid transfer native object list")
	}
	out := []Torrent{}
	for _, v := range a {
		r, ok := v.(map[string]any)
		if !ok {
			return nil, errors.New("invalid transfer torrent object")
		}
		out = append(out, Torrent(download.TransferObservedTask(r)))
	}
	return out, nil
}
func (h *NativeHost) readTorrents(ctx context.Context, s Service, ids []string, completed bool) ([]Torrent, error) {
	entry, e := h.entry(s)
	if e != nil {
		return nil, e
	}
	var out []Torrent
	e = h.run.step(ctx, "transfer-native-torrents", struct {
		Ref       string
		IDs       []string
		Completed bool
	}{s.Ref, ids, completed}, false, &out, func() (any, string, error) {
		raw, e := entry.client.TransferTorrents(ctx, ids, completed)
		if e != nil {
			if denied := h.run.guard(ctx); denied != nil {
				return nil, "read", denied
			}
			return nil, "read", nil
		}
		rows, e := nativeRows(raw)
		return rows, "read", e
	})
	if e == nil && completed {
		h.mu.Lock()
		for _, t := range out {
			hash := GetHash(t, s.Type)
			if !download.TransferValidHash(hash) {
				h.mu.Unlock()
				return nil, errors.New("native completed hash invalid")
			}
			h.sources[hash] = t
		}
		h.mu.Unlock()
	}
	return out, e
}
func (h *NativeHost) Completed(ctx context.Context, s Service) ([]Torrent, error) {
	if s.Name != text(h.config.values["fromdownloader"]) {
		return nil, errors.New("transfer completed read wrong source")
	}
	return h.readTorrents(ctx, s, nil, true)
}
func (h *NativeHost) Torrents(ctx context.Context, s Service, ids []string) ([]Torrent, error) {
	if s.Name != text(h.config.values["todownloader"]) {
		return nil, errors.New("transfer target read wrong service")
	}
	rows, e := h.readTorrents(ctx, s, ids, false)
	if e == nil {
		h.mu.Lock()
		for _, row := range rows {
			hash := GetHash(row, s.Type)
			if contains(ids, hash) {
				h.duplicates[hash] = true
			}
		}
		h.mu.Unlock()
	}
	return rows, e
}
func (h *NativeHost) AddPaused(ctx context.Context, s Service, spec AddSpec) (AddResult, error) {
	entry, e := h.entry(s)
	if e != nil {
		return AddResult{}, e
	}
	if s.Name != text(h.config.values["todownloader"]) {
		return AddResult{}, errors.New("transfer add wrong destination")
	}
	h.mu.Lock()
	source := h.contentSource[digest(spec.Content)]
	t, ok := h.sources[source]
	h.mu.Unlock()
	if !ok || source == "" {
		return AddResult{}, errors.New("transfer add has no source backup content")
	}
	a, _ := h.config.string("frompath")
	b, _ := h.config.string("topath")
	from := h.byName[text(h.config.values["fromdownloader"])]
	if from == nil {
		return AddResult{}, errors.New("transfer source binding missing")
	}
	expected := ConvertSavePath(GetSavePath(t, from.binding.Service.Type), a, b)
	save, ok := spec.SavePath.(string)
	if !ok || expected == nil || *expected != save || save == "" {
		return AddResult{}, errors.New("transfer add path differs from configured source conversion")
	}
	var cat *string
	if spec.Category != nil {
		v, ok := spec.Category.(string)
		if !ok {
			return AddResult{}, errors.New("invalid transfer category")
		}
		cat = &v
	}
	native := download.IYUUAddSpec{Torrent: spec.Content, SavePath: &save, Category: cat, Tags: spec.Tags, IgnoreCategoryCheck: true}
	if s.Type == "qbittorrent" {
		skip := spec.SkipChecking
		native.SkipChecking = &skip
	}
	var destination *download.IYUUDestination
	if entry.client.DestinationRequired(native) {
		var d download.IYUUDestination
		e = h.run.step(ctx, "transfer-daemon-destination", struct {
			Ref  string
			Spec AddSpec
		}{s.Ref, spec}, false, &d, func() (any, string, error) {
			v, e := entry.client.EffectiveAddDestination(ctx, native)
			return v, "read", e
		})
		if e != nil {
			return AddResult{}, e
		}
		destination = &d
	}
	var out AddResult
	e = h.run.step(ctx, "transfer-native-add", struct {
		Ref, Source, Binding string
		Spec                 AddSpec
		Destination          *download.IYUUDestination
	}{s.Ref, source, NativeBindingDigest(entry.binding), spec, destination}, true, &out, func() (any, string, error) {
		ids, materialErr := download.TransferContentHashes(spec.Content, s.Type)
		if materialErr != nil || !contains(ids, source) {
			return AddResult{SourceTruthy: s.Type == "qbittorrent", Outcome: "rejected"}, "rejected", nil
		}
		var v download.IYUUAddResult
		var e error
		if destination != nil {
			v, e = entry.client.AddPausedAt(ctx, native, *destination)
		} else {
			v, e = entry.client.AddPaused(ctx, native)
		}
		if e != nil {
			v.Outcome, e = nativeMutationOutcome(e)
		}
		return AddResult{v.Accepted, v.SourceTruthy, v.Hash, v.Outcome}, v.Outcome, e
	})
	receipt := h.run.ReceiptID()
	if e != nil {
		return out, e
	}
	h.mu.Lock()
	for _, tag := range spec.Tags {
		if _, generated := h.tags[tag]; generated {
			h.tags[tag] = s.Ref
			h.tagReceipts[tag] = receipt
			h.tagAccepted[tag] = out.Accepted
			if destination != nil {
				h.tagDest[tag] = *destination
			}
		}
	}
	h.mu.Unlock()
	if out.Accepted && out.Hash != "" {
		if e = h.verifyAdded(ctx, entry, out.Hash, source, destination); e != nil {
			return out, e
		}
		h.mu.Lock()
		h.proofs[s.Ref+"/"+out.Hash] = AuthorityGrant{s.Ref, NativeBindingDigest(entry.binding), out.Hash, receipt, "add", s.Name}
		h.mu.Unlock()
	}
	return out, nil
}
func (h *NativeHost) verifyAdded(ctx context.Context, s *nativeService, hash, source string, destination *download.IYUUDestination) error {
	if hash != source {
		return errors.New("transfer added task hash differs from observed source backup")
	}
	if destination == nil {
		return nil
	}
	rows, e := h.readTorrents(ctx, s.binding.Service, []string{hash}, false)
	if e != nil {
		return e
	}
	for _, t := range rows {
		if GetHash(t, s.binding.Service.Type) == hash && GetSavePath(t, s.binding.Service.Type) == destination.EffectivePath {
			return nil
		}
	}
	return errors.New("transfer actual daemon destination differs from confirmed add projection")
}
func (h *NativeHost) FindByTag(ctx context.Context, s Service, tag string) (string, error) {
	entry, e := h.entry(s)
	if e != nil {
		return "", e
	}
	h.mu.Lock()
	bound := h.tags[tag] == s.Ref
	receipt := h.tagReceipts[tag]
	d, has := h.tagDest[tag]
	accepted := h.tagAccepted[tag]
	h.mu.Unlock()
	if !bound || receipt == "" {
		return "", errors.New("transfer temporary tag is not tied to addition")
	}
	var hash string
	e = h.run.step(ctx, "transfer-poll-tag", struct{ Ref, Tag string }{s.Ref, tag}, false, &hash, func() (any, string, error) {
		v, e := entry.client.PollByTag(ctx, tag)
		if e != nil {
			if denied := h.run.guard(ctx); denied != nil {
				return nil, "read", denied
			}
			return "", "read", nil
		}
		return v, "read", nil
	})
	if e != nil || hash == "" {
		return hash, e
	}
	h.mu.Lock()
	_, exists := h.sources[hash]
	h.mu.Unlock()
	if !exists || !accepted {
		return "", errors.New("transfer discovered tag has no exact accepted source addition")
	}
	var dest *download.IYUUDestination
	if has {
		dest = &d
	}
	if e = h.verifyAdded(ctx, entry, hash, hash, dest); e != nil {
		return "", e
	}
	var effect EffectResult
	e = h.run.step(ctx, "transfer-remove-temporary-tag", struct{ Ref, Hash, Tag string }{s.Ref, hash, tag}, true, &effect, func() (any, string, error) {
		r, e := entry.client.RemoveTag(ctx, hash, tag)
		if e != nil {
			r.Outcome, e = nativeMutationOutcome(e)
		}
		return EffectResult{r.Accepted, r.Outcome}, r.Outcome, e
	})
	if e != nil {
		return "", e
	}
	h.mu.Lock()
	h.proofs[s.Ref+"/"+hash] = AuthorityGrant{s.Ref, NativeBindingDigest(entry.binding), hash, receipt, "add", s.Name}
	h.mu.Unlock()
	return hash, nil
}
func (h *NativeHost) effect(ctx context.Context, s Service, ids []string, op string) (EffectResult, error) {
	entry, e := h.entry(s)
	if e != nil {
		return EffectResult{}, e
	}
	h.mu.Lock()
	allowed := len(ids) > 0
	for _, id := range ids {
		if op == "delete" {
			_, ok := h.sources[id]
			target := h.byName[text(h.config.values["todownloader"])]
			confirmed := false
			if target != nil {
				_, confirmed = h.proofs[target.binding.Service.Ref+"/"+id]
			}
			purpose := (h.config.flag("deleteduplicate") && h.duplicates[id]) || (h.config.flag("deletesource") && confirmed)
			allowed = allowed && ok && purpose && s.Name == text(h.config.values["fromdownloader"])
		} else {
			p, ok := h.proofs[s.Ref+"/"+id]
			allowed = allowed && ok && p.BindingDigest == NativeBindingDigest(entry.binding) && s.Name == text(h.config.values["todownloader"])
		}
	}
	h.mu.Unlock()
	if !allowed {
		return EffectResult{}, errors.New("transfer control lacks exact source/addition authority")
	}
	var out EffectResult
	e = h.run.step(ctx, "transfer-native-"+op, struct {
		Ref string
		IDs []string
	}{s.Ref, ids}, true, &out, func() (any, string, error) {
		var r download.IYUUReceipt
		var e error
		switch op {
		case "start":
			r, e = entry.client.Start(ctx, ids)
		case "recheck":
			r, e = entry.client.Recheck(ctx, ids)
		case "delete":
			if len(ids) != 1 {
				return nil, "rejected", errors.New("invalid transfer delete selector")
			}
			r, e = entry.client.TransferDeleteTask(ctx, ids[0])
		default:
			return nil, "rejected", errors.New("unsupported transfer action")
		}
		if e != nil {
			r.Outcome, e = nativeMutationOutcome(e)
		}
		return EffectResult{r.Accepted, r.Outcome}, r.Outcome, e
	})
	return out, e
}
func (h *NativeHost) Start(ctx context.Context, s Service, ids []string) (EffectResult, error) {
	return h.effect(ctx, s, ids, "start")
}
func (h *NativeHost) Recheck(ctx context.Context, s Service, ids []string) (EffectResult, error) {
	return h.effect(ctx, s, ids, "recheck")
}
func (h *NativeHost) DeleteSource(ctx context.Context, s Service, id string) (EffectResult, error) {
	return h.effect(ctx, s, []string{id}, "delete")
}
func (h *NativeHost) SaveData(ctx context.Context, key string, v any) error {
	return h.run.localWrite(ctx, "source-history", "history/"+key, v)
}
func (h *NativeHost) SaveConfig(ctx context.Context, v map[string]any) error {
	return h.run.SourceConfig(ctx, v)
}
func (h *NativeHost) SystemMessage(ctx context.Context, title, text string) error {
	key := "system/" + h.run.key + "/" + fmt.Sprint(h.run.seq+1)
	if e := h.run.localWrite(ctx, "source-system-message", key, map[string]any{"title": title, "text": text}); e != nil {
		return e
	}
	if h.systemAccepted != nil {
		return h.systemAccepted(ctx, key)
	}
	return nil
}
func (h *NativeHost) Notify(ctx context.Context, n Notice) error {
	key := "notice/" + h.run.key + "/" + fmt.Sprint(h.run.seq+1)
	if e := h.run.localWrite(ctx, "source-notice", key, map[string]string{"type": n.Type, "title": n.Title, "text": n.Text}); e != nil {
		return e
	}
	if h.noticeAccepted != nil {
		return h.noticeAccepted(ctx, key)
	}
	return nil
}
func (h *NativeHost) RandomTag(ctx context.Context, n int) (string, error) {
	if n != 10 {
		return "", errors.New("invalid transfer random tag length")
	}
	var tag string
	e := h.run.step(ctx, "source-random-tag", n, false, &tag, func() (any, string, error) {
		b := make([]byte, n)
		if _, e := rand.Read(b); e != nil {
			return nil, "read", e
		}
		const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
		out := make([]byte, n)
		for i, c := range b {
			out[i] = chars[int(c)%len(chars)]
		}
		return string(out), "read", nil
	})
	if e == nil {
		h.mu.Lock()
		h.tags[tag] = ""
		h.mu.Unlock()
	}
	return tag, e
}
func (h *NativeHost) PendingProofs(queue map[string][]string) ([]AuthorityGrant, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []AuthorityGrant{}
	for name, ids := range queue {
		for _, id := range unique(ids) {
			found := false
			for _, p := range h.proofs {
				if p.ServiceName == name && p.Hash == id {
					out = append(out, p)
					found = true
				}
			}
			if !found {
				return nil, ErrReplayDiverged
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return fmt.Sprint(out[i]) < fmt.Sprint(out[j]) })
	return out, nil
}
