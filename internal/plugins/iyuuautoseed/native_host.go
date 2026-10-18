package iyuuautoseed

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"sync"
	"time"

	download "light-media/internal/plugins/iyuuautoseed/native"
)

type NativeBinding struct {
	Service Service
	Config  download.Config
	Options download.IYUUOptions
	// Retained in the immutable historical binding snapshot. It does not grant
	// native daemon paths or App filesystem access; paths come from native IO.
	AllowedSaveRoots []string
	AllowDefaultPath bool
}
type AuthorityGrant struct{ ServiceRef, BindingDigest, Hash, ReceiptID, Kind string }

func NativeBindingDigest(b NativeBinding) string {
	raw, _ := json.Marshal(struct {
		Service                                          Service
		Config                                           download.Config
		Roots                                            []string
		DefaultPath, AutoCategory, Sequential, FirstLast bool
		APIKey                                           string
	}{b.Service, b.Config, b.AllowedSaveRoots, b.AllowDefaultPath, b.Options.AutoCategory, b.Options.Sequential, b.Options.FirstLast, b.Options.APIKey})
	return digest(raw)
}

type NativeHostOptions struct {
	Context  context.Context
	Run      *JournalRun
	Bindings []NativeBinding
	Sites    []Site
	SiteHTTP *NativeSiteHTTP
	Cache    TorrentCache
	Limiters map[string]*SiteRateLimiter
	// PendingAuthorities are owner imported prior confirmed addition receipts for
	// persisted recheck queues, never just arbitrary hashes supplied by a worker.
	PendingAuthorities map[string][]string
	PendingProofs      []AuthorityGrant
	// Called only after the local receipt transaction has committed and released
	// the journal locks. The native owner routes this accepted source notice.
	NoticeAccepted func(context.Context, string) error
}
type nativeHostService struct {
	binding NativeBinding
	client  *download.IYUUClient
}
type NativeHost struct {
	run            *JournalRun
	services       map[string]*nativeHostService
	inventory      []Service
	sites          map[string]Site
	siteHTTP       *NativeSiteHTTP
	cache          TorrentCache
	limiters       map[string]*SiteRateLimiter
	mu             sync.Mutex
	sourcePaths    map[string]bool
	contents       map[string]bool
	tags           map[string]string
	added          map[string]bool
	urls           map[string]string
	proofs         map[string]AuthorityGrant
	tagReceipts    map[string]string
	destinations   map[string]download.IYUUDestination
	noticeAccepted func(context.Context, string) error
}

func NewNativeHost(opt NativeHostOptions) (*NativeHost, error) {
	if opt.Run == nil || opt.SiteHTTP == nil || opt.Cache == nil {
		return nil, errors.New("IYUU durable owner, site HTTP and scoped cache required")
	}
	type frozenDownloader struct {
		Service                                          Service
		Config                                           download.Config
		Roots                                            []string
		DefaultPath, AutoCategory, Sequential, FirstLast bool
		APIKey                                           string
	}
	bindings := []frozenDownloader{}
	for _, b := range opt.Bindings {
		bindings = append(bindings, frozenDownloader{b.Service, b.Config, b.AllowedSaveRoots, b.AllowDefaultPath, b.Options.AutoCategory, b.Options.Sequential, b.Options.FirstLast, b.Options.APIKey})
	}
	ctx := opt.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if e := opt.Run.bind(ctx, "native-host", struct {
		Downloaders []frozenDownloader
		Sites       []Site
		Pending     map[string][]string
		Proofs      []AuthorityGrant
	}{bindings, opt.Sites, opt.PendingAuthorities, opt.PendingProofs}); e != nil {
		return nil, e
	}
	transportBindings := map[string]SiteTransportBinding{}
	for ref, entry := range opt.SiteHTTP.entries {
		transportBindings[ref] = entry.binding
	}
	if e := opt.Run.bind(ctx, "site-http", struct {
		Sites map[string]SiteTransportBinding
		UA    string
	}{transportBindings, opt.SiteHTTP.defaultUA}); e != nil {
		return nil, e
	}
	h := &NativeHost{run: opt.Run, services: map[string]*nativeHostService{}, sites: map[string]Site{}, siteHTTP: opt.SiteHTTP, cache: opt.Cache, limiters: opt.Limiters, sourcePaths: map[string]bool{}, contents: map[string]bool{}, tags: map[string]string{}, added: map[string]bool{}, urls: map[string]string{}, proofs: map[string]AuthorityGrant{}, tagReceipts: map[string]string{}, destinations: map[string]download.IYUUDestination{}}
	h.noticeAccepted = opt.NoticeAccepted
	for _, binding := range opt.Bindings {
		s := binding.Service
		if s.Ref == "" || s.Name == "" || h.services[s.Ref] != nil || s.Kind != binding.Config.Kind {
			return nil, errors.New("invalid native service binding")
		}
		binding.Options.Guard = opt.Run.guard
		c, e := download.NewIYUUClient(binding.Config, binding.Options)
		if e != nil {
			h.Close()
			return nil, e
		}
		h.services[s.Ref] = &nativeHostService{binding, c}
		h.inventory = append(h.inventory, s)
	}
	for _, s := range opt.Sites {
		f, e := cloneMap(s.Fields)
		if e != nil {
			return nil, e
		}
		s.Fields = f
		address, ok := s.Fields["url"].(string)
		if !ok {
			return nil, errors.New("invalid native site")
		}
		d, e := SourceDomain(address)
		if e != nil {
			return nil, e
		}
		if _, ok := h.sites[d]; ok {
			return nil, errors.New("ambiguous authorized site domain")
		}
		h.sites[d] = s
	}
	for _, proof := range opt.PendingProofs {
		entry := h.services[proof.ServiceRef]
		if entry == nil || proof.BindingDigest != NativeBindingDigest(entry.binding) || proof.Hash == "" || proof.ReceiptID == "" || (proof.Kind != "add" && proof.Kind != "observed_tag") {
			h.Close()
			return nil, errors.New("prior addition authority does not match current downloader binding")
		}
		h.proofs[proof.ServiceRef+"/"+proof.Hash] = proof
	}
	for ref, hashes := range opt.PendingAuthorities {
		if h.services[ref] == nil {
			return nil, errors.New("unknown prior receipt service")
		}
		for _, hash := range hashes {
			if _, ok := h.proofs[ref+"/"+hash]; !ok {
				h.Close()
				return nil, errors.New("prior hash has no authenticated addition authority")
			}
			h.added[ref+"/"+hash] = true
		}
	}
	return h, nil
}
func (h *NativeHost) Close() {
	for _, s := range h.services {
		s.client.Close()
	}
}
func (h *NativeHost) service(s Service) (*nativeHostService, error) {
	binding := h.services[s.Ref]
	if binding == nil || binding.binding.Service != s {
		return nil, errors.New("native service ref is not granted")
	}
	return binding, nil
}
func (h *NativeHost) Inventory(ctx context.Context) ([]Service, error) {
	var out []Service
	e := h.run.step(ctx, "inventory", nil, false, &out, func() (any, string, error) { return h.inventory, "read", nil })
	return out, e
}
func nativeResult(raw []byte) ([]NativeTorrent, error) {
	var rows []map[string]any
	if e := sourceDecode(raw, &rows); e != nil {
		return nil, e
	}
	out := make([]NativeTorrent, 0, len(rows))
	for _, r := range rows {
		out = append(out, NativeTorrent{Fields: r})
	}
	return out, nil
}
func (h *NativeHost) Completed(ctx context.Context, s Service) ([]NativeTorrent, error) {
	entry, e := h.service(s)
	if e != nil {
		return nil, e
	}
	var out []NativeTorrent
	e = h.run.step(ctx, "native-completed", s.Ref, false, &out, func() (any, string, error) {
		raw, e := entry.client.Torrents(ctx, nil, true)
		if e != nil {
			return []NativeTorrent(nil), "read", nil
		}
		rows, e := nativeResult(raw)
		if e != nil {
			return []NativeTorrent(nil), "read", nil
		}
		return rows, "read", nil
	})
	if e == nil {
		h.mu.Lock()
		for _, t := range out {
			v := nativeField(t, s.Kind, "save_path", "download_dir")
			if p, ok := v.(string); ok {
				h.sourcePaths[p] = true
			}
		}
		h.mu.Unlock()
	}
	return out, e
}
func (h *NativeHost) Torrents(ctx context.Context, s Service, hashes []string) ([]NativeTorrent, error) {
	entry, e := h.service(s)
	if e != nil {
		return nil, e
	}
	var out []NativeTorrent
	e = h.run.step(ctx, "native-torrents", struct {
		Ref    string
		Hashes []string
	}{s.Ref, hashes}, false, &out, func() (any, string, error) {
		raw, e := entry.client.Torrents(ctx, hashes, false)
		if e != nil {
			return []NativeTorrent{}, "read", nil
		}
		rows, e := nativeResult(raw)
		if e != nil {
			return []NativeTorrent{}, "read", nil
		}
		return rows, "read", nil
	})
	return out, e
}
func (h *NativeHost) Indexer(ctx context.Context, domain string) (*Site, error) {
	var out *Site
	e := h.run.step(ctx, "site-indexer", domain, false, &out, func() (any, string, error) {
		s, ok := h.sites[domain]
		if !ok {
			return (*Site)(nil), "read", nil
		}
		return &s, "read", nil
	})
	return out, e
}
func (h *NativeHost) CheckSite(ctx context.Context, domain string) (bool, error) {
	var out bool
	e := h.run.step(ctx, "site-limit", domain, false, &out, func() (any, string, error) {
		l := h.limiters[domain]
		if l == nil {
			return false, "read", nil
		}
		limited, message := l.Check()
		if limited {
			sourceLog(ctx, 1132, message)
		}
		return limited, "read", nil
	})
	return out, e
}
func (h *NativeHost) authorizeSite(site Site) error {
	for _, trusted := range h.sites {
		if trusted.Ref == site.Ref {
			a, _ := json.Marshal(trusted)
			b, _ := json.Marshal(site)
			if string(a) == string(b) {
				return nil
			}
			break
		}
	}
	return errors.New("site snapshot differs from owner binding")
}
func (h *NativeHost) DownloadURL(ctx context.Context, seed map[string]any, site Site, template string) (string, error) {
	if e := h.authorizeSite(site); e != nil {
		return "", e
	}
	var out string
	e := h.run.step(ctx, "resolve", struct {
		Seed          map[string]any
		Ref, Template string
	}{seed, site.Ref, template}, false, &out, func() (any, string, error) {
		v, e := (Resolver{HTTP: h.siteHTTP}).Resolve(ctx, seed, site, template)
		return v, "read", e
	})
	if e == nil && out != "" {
		h.mu.Lock()
		h.urls[site.Ref+"/"+out] = out
		h.urls[site.Ref+"/"+out+"?https=1"] = out
		h.urls[site.Ref+"/"+out+"&https=1"] = out
		h.mu.Unlock()
	}
	return out, e
}
func (h *NativeHost) Torrent(ctx context.Context, site Site, address string) (TorrentContent, error) {
	if e := h.authorizeSite(site); e != nil {
		return TorrentContent{}, e
	}
	h.mu.Lock()
	_, ok := h.urls[site.Ref+"/"+address]
	h.mu.Unlock()
	if !ok {
		return TorrentContent{}, errors.New("torrent URL has no resolver lineage")
	}
	var out TorrentContent
	e := h.run.step(ctx, "torrent", struct{ Ref, URL string }{site.Ref, address}, false, &out, func() (any, string, error) {
		client, e := h.siteHTTP.TorrentHTTP(site.Ref)
		if e != nil {
			return nil, "read", e
		}
		r, e := (TorrentHelper{HTTP: client, Cache: h.cache}).Download(ctx, address, true)
		return TorrentContent{Bytes: r.Content, Magnet: r.Magnet, ErrorMessage: r.ErrorMessage}, "read", e
	})
	if e == nil && (len(out.Bytes) > 0 || out.Magnet != "") {
		h.mu.Lock()
		h.contents[contentKey(out.Bytes, out.Magnet)] = true
		h.mu.Unlock()
	}
	return out, e
}
func contentKey(b []byte, magnet string) string {
	if magnet != "" {
		return "magnet/" + digest([]byte(magnet))
	}
	return "bytes/" + digest(b)
}
func (h *NativeHost) AddPaused(ctx context.Context, s Service, spec AddSpec) (AddReceipt, error) {
	entry, e := h.service(s)
	if e != nil {
		return AddReceipt{}, e
	}
	if !spec.Paused {
		return AddReceipt{}, errors.New("IYUU only grants paused additions")
	}
	h.mu.Lock()
	contentOK := h.contents[contentKey(spec.Content, spec.Magnet)]
	h.mu.Unlock()
	if !contentOK {
		return AddReceipt{}, errors.New("addition has no authorized torrent content")
	}
	native := download.IYUUAddSpec{Torrent: spec.Content, Magnet: spec.Magnet, Tags: spec.Tags, IgnoreCategoryCheck: spec.IgnoreCategoryCheck}
	if spec.SavePath != nil {
		p, ok := spec.SavePath.(string)
		if !ok {
			return AddReceipt{}, sourceFault("TypeError", "download directory")
		}
		if p != "" {
			h.mu.Lock()
			observed := h.sourcePaths[p]
			h.mu.Unlock()
			if !observed {
				return AddReceipt{}, errors.New("save path was not observed")
			}
		}
		native.SavePath = &p
	}
	if spec.Category != nil {
		v, ok := spec.Category.(string)
		if !ok {
			return AddReceipt{}, sourceFault("TypeError", "category")
		}
		native.Category = &v
	}
	if spec.SkipChecking != nil {
		v := truth(spec.SkipChecking)
		native.SkipChecking = &v
	}
	var destination *download.IYUUDestination
	if entry.client.DestinationRequired(native) {
		if !entry.binding.AllowDefaultPath {
			return AddReceipt{}, errors.New("daemon destination observation not granted")
		}
		var observed download.IYUUDestination
		e = h.run.step(ctx, "native-add-destination", struct {
			Ref, Binding        string
			SavePath, Category  any
			IgnoreCategoryCheck bool
		}{s.Ref, NativeBindingDigest(entry.binding), spec.SavePath, spec.Category, spec.IgnoreCategoryCheck}, false, &observed, func() (any, string, error) {
			value, err := entry.client.EffectiveAddDestination(ctx, native)
			return value, "read", err
		})
		if e != nil {
			return AddReceipt{}, e
		}
		destination = &observed
	}
	h.mu.Lock()
	for _, tag := range spec.Tags {
		if ref, ok := h.tags[tag]; ok && ref != "" && ref != s.Ref {
			h.mu.Unlock()
			return AddReceipt{}, errors.New("temporary tag bound to another service")
		}
	}
	h.mu.Unlock()
	var out AddReceipt
	e = h.run.step(ctx, "native-add", struct {
		Ref         string
		Spec        AddSpec
		Destination *download.IYUUDestination
	}{s.Ref, spec, destination}, true, &out, func() (any, string, error) {
		var r download.IYUUAddResult
		var e error
		if destination == nil {
			r, e = entry.client.AddPaused(ctx, native)
		} else {
			r, e = entry.client.AddPausedAt(ctx, native, *destination)
		}
		if e != nil {
			outcome, known := nativeMutationOutcome(e)
			r.Outcome = outcome
			e = known
		}
		return AddReceipt{SourceTruthy: r.SourceTruthy, Accepted: r.Accepted, Hash: r.Hash, Outcome: r.Outcome}, r.Outcome, e
	})
	out.ReceiptID = h.run.ReceiptID()
	if e == nil && out.Accepted && out.Hash != "" && destination != nil {
		e = h.verifyDaemonDestination(ctx, entry, out.Hash, *destination)
	}
	if e == nil {
		h.mu.Lock()
		for _, tag := range spec.Tags {
			if _, ok := h.tags[tag]; ok {
				h.tags[tag] = s.Ref
				h.tagReceipts[tag] = out.ReceiptID
				if destination != nil {
					h.destinations[s.Ref+"/"+tag] = *destination
				}
			}
		}
		if out.Accepted && out.Hash != "" {
			h.added[s.Ref+"/"+out.Hash] = true
			h.proofs[s.Ref+"/"+out.Hash] = AuthorityGrant{s.Ref, NativeBindingDigest(entry.binding), out.Hash, out.ReceiptID, "add"}
		}
		h.mu.Unlock()
	}
	return out, e
}
func (h *NativeHost) FindByTag(ctx context.Context, s Service, tag string) (string, error) {
	entry, e := h.service(s)
	if e != nil {
		return "", e
	}
	h.mu.Lock()
	bound := h.tags[tag] == s.Ref
	h.mu.Unlock()
	if !bound {
		return "", errors.New("temporary tag has no addition receipt")
	}
	var hash string
	e = h.run.step(ctx, "native-poll-tag", struct{ Ref, Tag string }{s.Ref, tag}, false, &hash, func() (any, string, error) {
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
	destination, needsCheck := h.destinations[s.Ref+"/"+tag]
	h.mu.Unlock()
	if needsCheck {
		if e := h.verifyDaemonDestination(ctx, entry, hash, destination); e != nil {
			return "", e
		}
	}
	h.mu.Lock()
	h.added[s.Ref+"/"+hash] = true
	h.proofs[s.Ref+"/"+hash] = AuthorityGrant{s.Ref, NativeBindingDigest(entry.binding), hash, h.tagReceipts[tag], "observed_tag"}
	h.mu.Unlock()
	// The original source ignores removeTags' bool. Preserve the source control
	// flow for known responses while giving this separate mutation its own intent.
	var receipt EffectReceipt
	e = h.run.step(ctx, "native-remove-tag", struct{ Ref, Hash, Tag string }{s.Ref, hash, tag}, true, &receipt, func() (any, string, error) {
		r, e := entry.client.RemoveTag(ctx, hash, tag)
		if e != nil {
			outcome, known := nativeMutationOutcome(e)
			r.Outcome = outcome
			e = known
		}
		return EffectReceipt{Accepted: r.Accepted, Outcome: r.Outcome}, r.Outcome, e
	})
	return hash, e
}

// Persist only queue identities still needing controls. Each grant pins the
// original daemon/credential/options binding and originating durable receipt.
func (h *NativeHost) AuthoritiesForQueue(queue map[string][]string) (map[string][]string, []AuthorityGrant) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ids := map[string][]string{}
	proofs := []AuthorityGrant{}
	for ref, entry := range h.services {
		for _, hash := range queue[entry.binding.Service.Name] {
			if proof, ok := h.proofs[ref+"/"+hash]; ok {
				ids[ref] = append(ids[ref], hash)
				proofs = append(proofs, proof)
			}
		}
		sort.Strings(ids[ref])
	}
	sort.Slice(proofs, func(i, j int) bool {
		if proofs[i].ServiceRef != proofs[j].ServiceRef {
			return proofs[i].ServiceRef < proofs[j].ServiceRef
		}
		return proofs[i].Hash < proofs[j].Hash
	})
	return ids, proofs
}
func (h *NativeHost) effect(ctx context.Context, s Service, op string, ids []string, category any) (EffectReceipt, error) {
	entry, e := h.service(s)
	if e != nil {
		return EffectReceipt{}, e
	}
	if len(ids) == 0 {
		return EffectReceipt{}, errors.New("empty effect selector")
	}
	h.mu.Lock()
	for _, id := range ids {
		if !h.added[s.Ref+"/"+id] {
			h.mu.Unlock()
			return EffectReceipt{}, errors.New("effect identity has no confirmed addition lineage")
		}
	}
	h.mu.Unlock()
	var out EffectReceipt
	e = h.run.step(ctx, "native-"+op, struct {
		Ref      string
		IDs      []string
		Category any
	}{s.Ref, ids, category}, true, &out, func() (any, string, error) {
		var r download.IYUUReceipt
		var e error
		switch op {
		case "category":
			v, ok := category.(string)
			if !ok {
				return nil, "rejected", sourceFault("TypeError", "category")
			}
			r, e = entry.client.SetCategory(ctx, ids[0], v)
		case "recheck":
			r, e = entry.client.Recheck(ctx, ids)
		case "start":
			r, e = entry.client.Start(ctx, ids)
		default:
			return nil, "unknown", errors.New("invalid effect")
		}
		if e != nil {
			outcome, known := nativeMutationOutcome(e)
			r.Outcome = outcome
			e = known
		}
		return EffectReceipt{Accepted: r.Accepted, Outcome: r.Outcome}, r.Outcome, e
	})
	out.ReceiptID = h.run.ReceiptID()
	return out, e
}
func (h *NativeHost) SetCategory(ctx context.Context, s Service, id string, category any) (EffectReceipt, error) {
	return h.effect(ctx, s, "category", []string{id}, category)
}
func (h *NativeHost) Recheck(ctx context.Context, s Service, ids []string) (EffectReceipt, error) {
	return h.effect(ctx, s, "recheck", ids, nil)
}
func (h *NativeHost) Start(ctx context.Context, s Service, ids []string) (EffectReceipt, error) {
	return h.effect(ctx, s, "start", ids, nil)
}
func (h *NativeHost) LoadHistory(ctx context.Context, key string) (any, error) {
	return h.run.readValue(ctx, "history/"+key)
}
func (h *NativeHost) SaveHistory(ctx context.Context, key string, value any) error {
	return h.run.localWrite(ctx, "history", "history/"+key, value)
}
func (h *NativeHost) SaveConfig(ctx context.Context, value map[string]any) error {
	return h.run.localWrite(ctx, "config", "config", value)
}
func (h *NativeHost) Notify(ctx context.Context, title, text string) error {
	key := "outbox/" + h.run.key + "/" + strconv.Itoa(h.run.seq+1)
	if err := h.run.localWrite(ctx, "notification-outbox", key, map[string]string{"type": "SiteMessage", "title": title, "text": text}); err != nil {
		return err
	}
	if h.noticeAccepted != nil {
		return h.noticeAccepted(ctx, key)
	}
	return nil
}
func (h *NativeHost) RandomTag(ctx context.Context, n int) (string, error) {
	if n < 1 || n > 128 {
		return "", errors.New("invalid random tag length")
	}
	var out string
	e := h.run.step(ctx, "random-tag", n, false, &out, func() (any, string, error) {
		b := make([]byte, (n+1)/2)
		if _, e := rand.Read(b); e != nil {
			return nil, "read", e
		}
		return hex.EncodeToString(b)[:n], "read", nil
	})
	if e == nil {
		h.mu.Lock()
		if _, ok := h.tags[out]; !ok {
			h.tags[out] = ""
		}
		h.mu.Unlock()
	}
	return out, e
}

// DurableAPI binds the helper's real HTTP adapter to the same replay log.
// reportExisting is conservatively an external mutation. A lost reply blocks
// automatic continuation rather than repeating a possibly accepted report.
type DurableAPI struct {
	run       *JournalRun
	transport *APITransport
	clock     func() time.Time
}

func NewDurableAPI(ctx context.Context, run *JournalRun, transport *APITransport) (*DurableAPI, error) {
	if run == nil || transport == nil {
		return nil, errors.New("durable API binding required")
	}
	if e := run.bind(ctx, "api", struct{ Origin, Token string }{transport.origin, transport.token}); e != nil {
		return nil, e
	}
	return &DurableAPI{run: run, transport: transport, clock: time.Now}, nil
}

func (a DurableAPI) Call(ctx context.Context, request APIRequest) (*APIResponse, error) {
	var out *APIResponse
	mutation := request.Operation == ReportExisting
	e := a.run.step(ctx, "iyuu-api", request, mutation, &out, func() (any, string, error) {
		r, e := a.transport.Call(ctx, request)
		outcome := "read"
		if mutation {
			outcome = "confirmed"
			if r == nil || e != nil {
				outcome = "unknown"
			}
		}
		return r, outcome, e
	})
	return out, e
}
