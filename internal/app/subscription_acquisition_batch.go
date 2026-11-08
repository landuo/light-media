package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"light-media/internal/acquisitionbatch"
	sourcejson "light-media/internal/jinja/exec"
	"light-media/internal/metainfo"
	"light-media/internal/releasemeta"
	"light-media/internal/sites"
	"light-media/internal/subscriptionquality"
)

func subscriptionAcquisitionSource(subscribe json.RawMessage) (string, error) {
	var row map[string]any
	if cleanupJournalStrict(subscribe, &row) != nil {
		return "", acquisitionEvidence()
	}
	d := &sourcejson.Dict{}
	for _, key := range []string{"id", "name", "year", "type", "season", "episode_group", "tmdbid", "imdbid", "tvdbid", "doubanid", "bangumiid", "anilistid", "media_source", "media_id"} {
		v, ok := row[key]
		if !ok {
			return "", acquisitionEvidence()
		}
		d.Pairs = append(d.Pairs, &sourcejson.Pair{Key: sourcejson.ToValue(key), Value: sourcejson.ToValue(v)})
	}
	raw, e := sourcejson.PythonDumps(d, sourcejson.JSONOptions{})
	if e != nil {
		return "", e
	}
	return "Subscribe|" + raw, nil
}
func isSubscriptionAcquisitionSource(source string) bool {
	return strings.HasPrefix(source, "Subscribe|")
}
func acquisitionResourceKey(resource sites.Resource) string {
	if resource.ID != "" {
		return resource.ID
	}
	if resource.Hash != "" {
		return resource.Hash
	}
	if key := acquisitionResourceURLKey(resource.DetailsURL); key != "" {
		return key
	}
	size := ""
	if resource.Size != 0 {
		size = strconv.FormatInt(resource.Size, 10)
	}
	return fmt.Sprintf("title=%s|size=%s", resource.Title, size)
}

var acquisitionIndirectURL = regexp.MustCompile(`\[(.*?)](.*)`)

func acquisitionResourceURLKey(address string) string {
	if m := acquisitionIndirectURL.FindStringSubmatch(address); m != nil {
		address = m[2]
	}
	u, err := url.Parse(address)
	if err != nil || u.User != nil {
		return ""
	}
	q := u.Query()
	for _, key := range []string{"id", "torrentid", "torrent_id", "tid", "hash"} {
		for _, value := range q[key] {
			if value != "" {
				return u.Host + ":" + key + "=" + value
			}
		}
	}
	if path := u.EscapedPath(); u.Host != "" && path != "" {
		return u.Host + path
	}
	return ""
}
func (a *App) beginSubscriptionAcquisition(ctx context.Context, j Job, s Subscription, request SearchRequest, candidates []Candidate) (*subscriptionAcquisition, error) {
	subscribe, e := a.subscriptionSourceProjection(ctx, s)
	if e != nil {
		return nil, e
	}
	source, e := subscriptionAcquisitionSource(subscribe)
	if e != nil {
		return nil, e
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	old, e := a.readAcquisition(ctx, tx, j.ID)
	if e == nil {
		if _, e = a.acquisitionScopeTx(ctx, tx, &old); e != nil {
			return nil, e
		}
		return &old, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	var count int
	if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE kind='intent' AND json_extract(payload,'$.subscription_acquisition')=?`, j.ID).Scan(&count); e != nil {
		return nil, e
	}
	if count != 0 {
		return nil, acquisitionEvidence()
	}
	actual, e := scanJob(tx.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, j.ID))
	if e != nil {
		return nil, e
	}
	if actual.Kind != "subscription" || actual.State != "running" || actual.Generation != j.Generation || !slices.Equal(actual.Payload, j.Payload) {
		return nil, acquisitionEvidence()
	}
	recognition, ok := ctx.Value(resourceRecognitionKey{}).(resourceRecognition)
	if !ok {
		return nil, acquisitionEvidence()
	}
	parser, e := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: recognition.options})
	if e != nil {
		return nil, e
	}
	r := subscriptionAcquisition{Source: source, Subscribe: subscribe, Version: 1, ID: j.ID, JobSHA: tokenHash(string(j.Payload)), JobWorkflowID: j.WorkflowID, Generation: j.Generation, RulesSHA: qualityRuleSHA(a.Settings()), Subscription: s, Request: request, Options: recognition.options, Candidates: []Candidate{}, Metas: []json.RawMessage{}, Steps: []subscriptionAcquisitionStep{}}
	if providerNativeMedia(s.Media) {
		if request.originalSearchInfo == nil {
			return nil, acquisitionEvidence()
		}
		r.ProviderMedia = append(json.RawMessage(nil), request.originalSearchInfo.MediaInfo...)
		if e = r.restoreProviderSearch(); e != nil {
			return nil, e
		}
	}
	if latest := originalChatLatestFromContext(ctx); latest != nil && latest.Mode == "rss" {
		owned, e := a.originalChatRSSOwnsSearch(ctx, tx, actual, s)
		if e != nil {
			return nil, e
		}
		if owned {
			r.RSSBatchID = latest.id
		}
	}
	for _, c := range candidates {
		if (!c.Accepted || c.NeedsConfirmation) && !(s.Media.MediaType == "tv" && automaticPackCandidate(c, s.Season)) {
			continue
		}
		subtitle := c.Resource.SourceSubtitle
		if subtitle == nil && c.Resource.Subtitle != "" {
			subtitle = &c.Resource.Subtitle
		}
		meta, e := parser.ParseTitle(c.Resource.Title, subtitle, nil)
		if e != nil {
			return nil, e
		}
		raw, e := meta.EncodeState()
		if e != nil {
			return nil, e
		}
		if len(c.originalSelectionMeta) > 0 {
			raw = append([]byte(nil), c.originalSelectionMeta...)
		}
		if r.RSSBatchID != "" {
			// Persisted acquisition and download snapshots canonicalize JSON.
			// Use that same immutable representation for the title proof SHA.
			raw, e = cleanupJournalJSON(json.RawMessage(raw))
			if e != nil {
				return nil, e
			}
		}
		r.Candidates = append(r.Candidates, c)
		r.Metas = append(r.Metas, raw)
		r.SelectionContexts = append(r.SelectionContexts, append(json.RawMessage(nil), c.originalSelectionContext...))
	}
	if !slices.ContainsFunc(r.SelectionContexts, func(raw json.RawMessage) bool { return len(raw) > 0 && string(raw) != "null" }) {
		r.SelectionContexts = nil
	}
	if e = a.saveAcquisitionTx(ctx, tx, r); e != nil {
		return nil, e
	}
	if _, e = a.acquisitionScopeTx(ctx, tx, &r); e != nil {
		return nil, e
	}
	return &r, tx.Commit()
}

func (a *App) acquisitionStep(ctx context.Context, r *subscriptionAcquisition, input subscriptionAcquisitionInput) (context.Context, *subscriptionAcquisitionStep, error) {
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return ctx, nil, e
	}
	defer tx.Rollback()
	current, e := a.acquisitionScopeTx(ctx, tx, r)
	if e != nil {
		return ctx, nil, e
	}
	sha := tokenHash(string(mustCleanSeedJSON(input)))
	index := -1
	for i, s := range current.Steps {
		if s.InputSHA == sha {
			index = i
			break
		}
	}
	if index < 0 {
		index = len(current.Steps)
		current.Steps = append(current.Steps, subscriptionAcquisitionStep{Input: input, InputSHA: sha, Phase: "intent"})
		if e = a.saveAcquisitionTx(ctx, tx, current); e != nil {
			return ctx, nil, e
		}
	}
	if e = tx.Commit(); e != nil {
		return ctx, nil, e
	}
	*r = current
	ctx = context.WithValue(ctx, subscriptionAcquisitionKey{}, &subscriptionAcquisitionGrant{a, r, index})
	ctx = context.WithValue(ctx, jobTraceKey{}, jobTrace{Job: r.ID})
	if providerNativeMedia(r.Subscription.Media) {
		ctx = subscriptionContext(ctx, r.Subscription.ID, r.Subscription.Revision)
	}
	return ctx, &r.Steps[index], nil
}
func (a *App) acquisitionCandidate(ctx context.Context, r *subscriptionAcquisition, c Candidate, episodes, selected []int, material string, meta json.RawMessage, allowExtra bool, listed *subscriptionAcquisitionRSSListing) (bool, error) {
	input := subscriptionAcquisitionInput{AllowWholeExtra: allowExtra, Candidate: c, Episodes: qualityCopyNote(episodes), Selected: qualityCopyNote(selected), Meta: append(json.RawMessage(nil), meta...), Material: material, RSSListed: listed}
	ctx, step, e := a.acquisitionStep(ctx, r, input)
	if e != nil {
		return false, e
	}
	if step.Phase == "rejected" || step.Phase == "preview_rejected" {
		return false, nil
	}
	if step.Phase == "accepted" {
		var in Intent
		if e = a.Store.Get(ctx, "intent", step.IntentID, &in); e != nil {
			return false, e
		}
		if in.SubscriptionAcquisition != r.ID || acquisitionIntentSHA(in) != step.IntentSHA {
			return false, acquisitionEvidence()
		}
		tx, e := a.Store.DB.BeginTx(ctx, nil)
		if e != nil {
			return false, e
		}
		defer tx.Rollback()
		if e = a.guardAcceptedDownloadProgressTx(ctx, tx, in); e != nil {
			return false, e
		}
		return true, nil
	}
	var in Intent
	if step.Phase == "confirmed" {
		if e = a.Store.Get(ctx, "intent", step.IntentID, &in); e != nil {
			return false, e
		}
		if acquisitionIntentSHA(in) != step.IntentSHA {
			return false, acquisitionEvidence()
		}
	} else if step.Phase == "intent" {
		req := r.Request
		req.Episodes = qualityCopyNote(episodes)
		preview, e := a.previewDownload(ctx, PreviewRequest{SearchRequest: req, sourceContext: c.originalSelectionContext, sourceMaterial: material, sourceMeta: meta, automaticSeasonPack: c.Resource.ID != "" && r.Subscription.Media.MediaType == "tv", qualitySelection: true, SiteID: c.Resource.SiteID, ResourceID: c.Resource.ID, DownloaderID: r.Subscription.DownloaderID, RootID: subscriptionLegacyRoot(r.Subscription), DirectoryID: r.Subscription.DirectoryID, DownloadPath: r.Subscription.DownloadPath, SubscriptionID: r.Subscription.ID, Revision: r.Subscription.Revision}, true)
		if e != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			// A preview has no external daemon write. Record the exact failure rather
			// than counting a queued intent or guessing that an Add was rejected.
			tx, te := a.Store.DB.BeginTx(ctx, nil)
			if te != nil {
				return false, te
			}
			defer tx.Rollback()
			g, current, te := a.acquisitionGrantTx(ctx, tx)
			if te != nil {
				return false, te
			}
			current.Steps[g.ordinal].Phase, current.Steps[g.ordinal].Error = "preview_rejected", a.clean(e.Error())
			if te = a.saveAcquisitionTx(ctx, tx, current); te != nil {
				return false, te
			}
			if te = tx.Commit(); te != nil {
				return false, te
			}
			*r = current
			a.event(ctx, Job{ID: r.ID, WorkflowID: r.JobWorkflowID}, "WARN", "subscriptions", "quality_candidate_rejected", e.Error())
			if pluginErrorCodeInternal(e) == "REVIEW_DIRECTORY_OVERWRITE_PENDING" {
				return false, e
			}
			return false, nil
		}
		in, e = a.confirmDownload(ctx, preview.ID, false)
		if e != nil {
			return false, e
		}
	} else {
		return false, acquisitionEvidence()
	}
	accepted, e := a.submitAcquisitionChild(ctx, in)
	if e != nil {
		var known *subscriptionAcquisitionNoEffect
		if errors.As(e, &known) {
			return false, nil
		}
		return false, e
	}
	return accepted.State == "accepted", nil
}

type subscriptionDownloadFailure struct {
	Version                      int
	Fingerprint, Source, Message string
	RetryAt                      int64
	Count                        int
}

func (a *App) acquisitionFingerprint(ctx context.Context, r *subscriptionAcquisition, c Candidate, meta *releasemeta.MetaObject) (string, error) {
	return a.subscriptionFailureFingerprint(ctx, r.Subscription, c.Resource.SiteID, acquisitionResourceKey(c.Resource), meta)
}

func (a *App) subscriptionFailureFingerprint(ctx context.Context, s Subscription, siteID, resource string, meta *releasemeta.MetaObject) (string, error) {
	// Numeric source identities are authenticated mappings. Test/legacy sites
	// without a mapping retain the source's site_name fallback, never an App ID.
	siteName := ""
	for _, site := range a.Settings().Sites {
		if site.ID == siteID {
			siteName = site.Name
			break
		}
	}
	numeric, e := a.Store.nativeSiteByAppID(ctx, siteID)
	if e == nil {
		siteName = strconv.FormatInt(numeric.SourceID, 10)
	} else if !errors.Is(e, sql.ErrNoRows) {
		return "", e
	}
	kind := subscriptionquality.Movie
	if s.Media.MediaType == "tv" {
		kind = subscriptionquality.TV
	}
	identity := "themoviedb:" + strconv.FormatInt(s.Media.ID, 10)
	if providerNativeMedia(s.Media) {
		identity = s.Media.ProviderSource + ":" + s.Media.ProviderID
	}
	values := map[string]string{"episodes": meta.Episode(), "media_key": identity, "media_type": kind, "resource": resource, "season": meta.Season(), "site": siteName}
	raw, e := sourcejson.PythonDumps(values, sourcejson.JSONOptions{SortKeys: true})
	if e != nil {
		return "", e
	}
	return tokenHash(raw), nil
}
func acquisitionFailureTTL(message string) time.Duration {
	ttl := time.Hour
	lower := strings.ToLower(message)
	for _, word := range []string{"无法读取种子文件", "下载种子内容为空", "无法获取下载地址", "种子下载失败", "torrent not found", "not found", "404", "deleted", "invalid torrent"} {
		if strings.Contains(lower, word) {
			return 24 * time.Hour
		}
	}
	return ttl
}
func (a *App) recordAcquisitionFailure(ctx context.Context, r *subscriptionAcquisition, fingerprint, message string) error {
	if !isSubscriptionAcquisitionSource(r.Source) {
		return acquisitionEvidence()
	}
	if fingerprint == "" {
		return nil
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = a.acquisitionScopeTx(ctx, tx, r); e != nil {
		return e
	}
	if e = a.recordSubscriptionFailureTx(ctx, tx, r.Source, fingerprint, message); e != nil {
		return e
	}
	return tx.Commit()
}

func (a *App) recordSubscriptionFailureTx(ctx context.Context, tx *sql.Tx, source, fingerprint, message string) error {
	out := subscriptionDownloadFailure{Version: 1, Fingerprint: fingerprint, Source: source, Message: a.clean(message), RetryAt: time.Now().Add(acquisitionFailureTTL(message)).Unix(), Count: 1}
	var old subscriptionDownloadFailure
	e := a.readQualitySealed(ctx, tx, "subscription_download_failure", fingerprint, &old)
	if e == nil {
		if old.Version != 1 || old.Fingerprint != fingerprint {
			return acquisitionEvidence()
		}
		out.Count = old.Count + 1
	} else if !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	return a.writeQualitySealedTx(ctx, tx, "subscription_download_failure", fingerprint, out)
}

func (a *App) subscriptionFailureActive(ctx context.Context, fingerprint string, now int64) (bool, error) {
	var failure subscriptionDownloadFailure
	err := a.readQualitySealed(ctx, a.Store.DB, "subscription_download_failure", fingerprint, &failure)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if failure.Version != 1 || failure.Fingerprint != fingerprint {
		return false, acquisitionEvidence()
	}
	return failure.RetryAt > now, nil
}

func (a *App) runSubscriptionAcquisition(ctx context.Context, j Job, s Subscription, request SearchRequest, candidates []Candidate, state subscriptionquality.State, available []int) (int, error) {
	r, e := a.beginSubscriptionAcquisition(ctx, j, s, request, candidates)
	if e != nil {
		return 0, e
	}
	latest := originalChatLatestFromContext(ctx)
	if r.RSSBatchID != "" && (latest == nil || latest.Mode != "rss" || latest.id != r.RSSBatchID || !latest.ownsRSSRoot(j)) {
		return 0, acquisitionEvidence()
	}
	rss := latest != nil && latest.Mode == "rss"
	state = subscriptionQualityState(r.Subscription)
	available = qualityCopyNote(r.Request.Episodes)
	ctx, e = withResourceRecognition(ctx, r.Options)
	if e != nil {
		return 0, e
	}
	byID := map[string]Candidate{}
	metas := map[string]*releasemeta.MetaObject{}
	refs := map[string]string{}
	actualEpisodes := map[string][]int{}
	values := []*acquisitionbatch.Candidate{}
	for i, c := range r.Candidates {
		if len(r.SelectionContexts) > 0 {
			if len(r.SelectionContexts) != len(r.Candidates) {
				return 0, acquisitionEvidence()
			}
			c.originalSelectionContext = append(json.RawMessage(nil), r.SelectionContexts[i]...)
			if string(c.originalSelectionContext) == "null" {
				c.originalSelectionContext = nil
			}
			if len(c.originalSelectionContext) > 0 {
				var context originalDownloadContext
				if e = json.Unmarshal(c.originalSelectionContext, &context); e != nil {
					return 0, e
				}
				if context.AllowedEpisodes != nil {
					raw, e := json.Marshal(context.AllowedEpisodes)
					if e != nil {
						return 0, e
					}
					var allowed []int
					if e = json.Unmarshal(raw, &allowed); e != nil {
						return 0, e
					}
					if allowed == nil {
						allowed = []int{}
					}
					c.originalSelectionAllowed = &allowed
				}
			}
		}
		meta, e := releasemeta.DecodeMetaObjectState(r.Metas[i])
		if e != nil {
			return 0, e
		}
		seasons, episodes, e := larkMetaNumbers(meta)
		if e != nil {
			return 0, e
		}
		quality := subscriptionquality.Resource{Type: state.Type, Seasons: seasons, Episodes: episodes, Priority: c.Resource.PriorityOrder}
		selection := state.Select(quality)
		if !selection.Accepted {
			continue
		}
		id := strconv.Itoa(i)
		byID[id], metas[id] = c, meta
		value := &acquisitionbatch.Candidate{ID: id, Kind: s.Media.MediaType, MediaKey: mediaKey(s.Media), TitleYear: s.Media.Title, Seasons: seasons, Episodes: episodes}
		if s.Media.MediaType == "tv" && !state.Full() {
			allowed := qualityCopyNote(selection.AllowedEpisodes)
			value.AllowedEpisodes = &allowed
		}
		if c.originalSelectionAllowed != nil {
			allowed := append([]int{}, (*c.originalSelectionAllowed)...)
			if value.AllowedEpisodes != nil {
				allowed = slices.DeleteFunc(allowed, func(ep int) bool { return !slices.Contains(*value.AllowedEpisodes, ep) })
			}
			value.AllowedEpisodes = &allowed
		}
		values = append(values, value)
	}
	mutate := func(c *acquisitionbatch.Candidate) error {
		if len(c.Episodes) > 0 {
			first, last := c.Episodes[0], c.Episodes[0]
			for _, n := range c.Episodes {
				first = min(first, n)
				last = max(last, n)
			}
			return metas[c.ID].SetEpisodes(first, last)
		}
		return nil
	}
	fingerprint := func(c *acquisitionbatch.Candidate) string {
		if mutate(c) != nil {
			return ""
		}
		key, _ := a.acquisitionFingerprint(ctx, r, byID[c.ID], metas[c.ID])
		return key
	}
	deps := acquisitionbatch.Dependencies{
		Sort: func(ctx context.Context, vs []*acquisitionbatch.Candidate) ([]*acquisitionbatch.Candidate, error) {
			cs, ms := []Candidate{}, []json.RawMessage{}
			for _, v := range vs {
				cs = append(cs, byID[v.ID])
				raw, e := metas[v.ID].EncodeState()
				if e != nil {
					return nil, e
				}
				ms = append(ms, raw)
			}
			sorted, _, e := a.larkSortCandidateMetas(ctx, cs, ms, s.Media)
			if e != nil {
				return nil, e
			}
			order := map[string]int{}
			for i, c := range sorted {
				order[c.Resource.SiteID+":"+c.Resource.ID] = i
			}
			out := append([]*acquisitionbatch.Candidate{}, vs...)
			sort.SliceStable(out, func(i, j int) bool {
				return order[byID[out[i].ID].Resource.SiteID+":"+byID[out[i].ID].Resource.ID] < order[byID[out[j].ID].Resource.SiteID+":"+byID[out[j].ID].Resource.ID]
			})
			return out, nil
		},
		Fingerprint: fingerprint,
		ActiveFailures: func(ctx context.Context, vs []*acquisitionbatch.Candidate) (map[string]bool, error) {
			out := map[string]bool{}
			now := time.Now().Unix()
			for _, v := range vs {
				key := fingerprint(v)
				if key == "" {
					return nil, acquisitionEvidence()
				}
				active, e := a.subscriptionFailureActive(ctx, key, now)
				if e != nil {
					return nil, e
				}
				if active {
					out[key] = true
				}
			}
			return out, nil
		},
		Stopped: func() bool { return a.jobCancelled(ctx, j) != nil || a.guardSubscriptionContext(ctx) != nil },
		Torrent: func(ctx context.Context, c *acquisitionbatch.Candidate) (acquisitionbatch.Material, error) {
			original := byID[c.ID]
			site, e := a.site(original.Resource.SiteID)
			if e != nil {
				return acquisitionbatch.Material{}, e
			}
			var raw []byte
			var magnet string
			var proof *originalChatLatestDownloadProof
			if rss {
				proof, e = a.originalChatLatestSelect(latest, site, original.Resource.ID)
				if e != nil {
					return acquisitionbatch.Material{}, e
				}
			}
			e = a.withSite(ctx, site, false, func(client *sites.Client) error {
				if proof != nil {
					material, err := a.originalChatLatestMaterial(ctx, proof, site, client)
					raw, magnet = material.Torrent, material.Magnet
					return err
				}
				var e error
				raw, e = client.Torrent(ctx, original.Resource.ID)
				return e
			})
			if e != nil {
				if ctx.Err() != nil {
					return acquisitionbatch.Material{}, ctx.Err()
				}
				if rss {
					if sites.IsSubscriptionMaterialFailure(e) {
						return acquisitionbatch.Material{}, nil
					}
					return acquisitionbatch.Material{}, e
				}
				if e = a.recordAcquisitionFailure(ctx, r, fingerprint(c), e.Error()); e != nil {
					return acquisitionbatch.Material{}, e
				}
				return acquisitionbatch.Material{}, nil
			}
			if magnet != "" {
				return acquisitionbatch.Material{Magnet: true}, nil
			}
			if len(raw) == 0 {
				return acquisitionbatch.Material{}, nil
			}
			m, e := metainfo.ParseMaterial(raw)
			if e != nil {
				if rss {
					return acquisitionbatch.Material{}, nil
				}
				if e = a.recordAcquisitionFailure(ctx, r, fingerprint(c), "invalid torrent"); e != nil {
					return acquisitionbatch.Material{}, e
				}
				return acquisitionbatch.Material{}, nil
			}
			ref, e := a.persistMaterial(ctx, raw, m.Hash)
			if e != nil {
				return acquisitionbatch.Material{}, e
			}
			refs[c.ID] = ref
			options, e := a.globalRecognitionOptions(ctx)
			if e != nil {
				return acquisitionbatch.Material{}, e
			}
			parser, e := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: options})
			if e != nil {
				return acquisitionbatch.Material{}, e
			}
			episodes := []int{}
			for _, f := range m.Files {
				if !isVideo(f.Path) {
					continue
				}
				meta, e := parser.ParseTitle(filepath.Base(f.Path), nil, nil)
				if e != nil {
					return acquisitionbatch.Material{}, e
				}
				if n := meta.Number("begin_episode"); n == nil || n.Sign() == 0 {
					continue
				}
				_, eps, e := larkMetaNumbers(meta)
				if e != nil {
					return acquisitionbatch.Material{}, e
				}
				for _, n := range eps {
					if !slices.Contains(episodes, n) {
						episodes = append(episodes, n)
					}
				}
			}
			actualEpisodes[c.ID] = qualityCopyNote(episodes)
			return acquisitionbatch.Material{Content: raw, Episodes: episodes}, nil
		},
		Single: func(ctx context.Context, d acquisitionbatch.Download) (bool, error) {
			c := d.Candidate
			if fingerprint(c) == "" {
				return false, acquisitionEvidence()
			}
			if len(c.Seasons) > 1 {
				return false, bad("SUBSCRIPTION_BATCH_DEPENDENCY_PENDING", "多季整包的跨季下载及整理范围尚未接入")
			}
			if e := mutate(c); e != nil {
				return false, e
			}
			raw, e := metas[c.ID].EncodeState()
			if e != nil {
				return false, e
			}
			episodes := qualityCopyNote(c.Episodes)
			if d.Episodes == nil && len(actualEpisodes[c.ID]) > 0 {
				episodes = qualityCopyNote(actualEpisodes[c.ID])
				sort.Ints(episodes)
			}
			if s.Media.MediaType == "tv" && len(episodes) == 0 {
				episodes = state.Targets()
			}
			var selected []int
			if d.Episodes != nil {
				selected = qualityCopyNote(*d.Episodes)
				episodes = qualityCopyNote(selected)
			}
			allowExtra := false
			if s.Media.MediaType == "tv" && d.Episodes == nil {
				for _, ep := range episodes {
					if !slices.Contains(state.Targets(), ep) {
						allowExtra = true
					}
				}
			}
			var listed *subscriptionAcquisitionRSSListing
			if r.RSSBatchID != "" && s.Media.MediaType == "tv" && d.Episodes == nil && len(d.Content) == 0 && refs[c.ID] == "" {
				idx, err := strconv.Atoi(c.ID)
				if err != nil || idx < 0 || idx >= len(r.Metas) {
					return false, acquisitionEvidence()
				}
				original := r.Metas[idx]
				meta, err := releasemeta.DecodeMetaObjectState(original)
				if err != nil {
					return false, err
				}
				seasons, declared, err := larkMetaNumbers(meta)
				if err != nil {
					return false, err
				}
				if len(seasons) == 1 && seasons[0] == s.Season && len(declared) > 0 && slices.Equal(declared, episodes) {
					candidate := byID[c.ID].Resource
					site, err := a.site(candidate.SiteID)
					if err != nil {
						return false, err
					}
					proof, err := a.originalChatLatestSelect(latest, site, candidate.ID)
					if err != nil {
						return false, err
					}
					raw = original
					listed = &subscriptionAcquisitionRSSListing{BatchID: r.RSSBatchID, SiteKey: proof.SiteKey, ResourceID: candidate.ID,
						SourceMetaSHA: tokenHash(string(raw)), ProofSHA: tokenHash(string(mustCleanSeedJSON(proof))),
						Season: s.Season, Priority: candidate.PriorityOrder, Episodes: qualityCopyNote(declared)}
				}
			}
			accepted, e := a.acquisitionCandidate(ctx, r, byID[c.ID], episodes, selected, refs[c.ID], raw, allowExtra, listed)
			if e == nil && !accepted {
				for i := len(r.Steps) - 1; i >= 0; i-- {
					step := r.Steps[i]
					if step.Input.Candidate.Resource.SiteID != byID[c.ID].Resource.SiteID || step.Input.Candidate.Resource.ID != byID[c.ID].Resource.ID {
						continue
					}
					if step.Phase == "rejected" {
						if e = a.recordAcquisitionFailure(ctx, r, fingerprint(c), step.Error); e != nil {
							return false, e
						}
					}
					break
				}
			}
			return accepted, e
		},
		RecordFailure: func(ctx context.Context, c *acquisitionbatch.Candidate, message string) error {
			return a.recordAcquisitionFailure(ctx, r, fingerprint(c), message)
		},
	}
	needs := []acquisitionbatch.MediaNeed{}
	if s.Media.MediaType == "tv" {
		wanted := []int{}
		for _, n := range state.Pending() {
			if slices.Contains(available, n) {
				wanted = append(wanted, n)
			}
		}
		n := acquisitionbatch.Need{Season: s.Season, Episodes: wanted, TotalEpisode: state.TotalEpisode, StartEpisode: state.StartEpisode}
		if state.Full() {
			n.Episodes = []int{}
			n.RequireCompleteCoverage = true
		}
		needs = []acquisitionbatch.MediaNeed{{MediaKey: mediaKey(s.Media), Seasons: []acquisitionbatch.Need{n}}}
	}
	if s.Media.MediaType == "tv" && !state.Full() {
		full := []*acquisitionbatch.Candidate{}
		for _, v := range values {
			q := subscriptionquality.Resource{Type: state.Type, Seasons: v.Seasons, Episodes: v.Episodes, Priority: byID[v.ID].Resource.PriorityOrder}
			if state.FullSeason(q) && state.Select(q).PreferFullPack {
				full = append(full, v)
			}
		}
		if len(full) > 0 {
			n := acquisitionbatch.Need{Season: s.Season, Episodes: []int{}, TotalEpisode: state.TotalEpisode, StartEpisode: state.StartEpisode, RequireCompleteCoverage: true}
			out, e := acquisitionbatch.Run(ctx, full, []acquisitionbatch.MediaNeed{{MediaKey: mediaKey(s.Media), Seasons: []acquisitionbatch.Need{n}}}, deps)
			if e != nil {
				return len(out.Downloaded), e
			}
			if len(out.Downloaded) > 0 {
				return len(out.Downloaded), nil
			}
		}
	}
	out, e := acquisitionbatch.Run(ctx, values, needs, deps)
	if e != nil {
		return len(out.Downloaded), e
	}
	if e = a.jobCancelled(ctx, j); e != nil {
		return len(out.Downloaded), e
	}
	if e = a.guardSubscriptionContext(ctx); e != nil {
		return len(out.Downloaded), e
	}
	return len(out.Downloaded), nil
}
