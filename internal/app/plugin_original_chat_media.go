package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"light-media/internal/catalog"
	"light-media/internal/releasemeta"
)

const originalChatMediaPageSize = 8

type originalChatMediaInteractions struct {
	mu     sync.Mutex
	byUser map[string]*originalChatMediaSession
}

type originalChatMediaSession struct {
	mu                              sync.Mutex
	id                              string
	created                         time.Time
	action, phase, title            string
	meta                            *releasemeta.MetaObject
	media                           []catalog.ExploreMedia
	selected                        catalog.Media
	selectedDisplay                 catalog.Media
	request                         SearchRequest
	candidates                      []Candidate
	page, torrentPage, pendingIndex int
	dirs                            []originalChatMediaDirectory
}

type originalChatMediaDirectory struct {
	ID, Name, Path, Downloader, RootID string
}

func (m *originalChatMediaInteractions) lock(user string) *originalChatMediaSession {
	m.mu.Lock()
	s := m.byUser[user]
	m.mu.Unlock()
	if s == nil {
		return nil
	}
	s.mu.Lock()
	m.mu.Lock()
	current := m.byUser[user] == s
	if current && time.Since(s.created) >= 24*time.Hour {
		delete(m.byUser, user)
		current = false
	}
	m.mu.Unlock()
	if !current {
		s.mu.Unlock()
		return nil
	}
	return s
}

func (m *originalChatMediaInteractions) set(user string, s, previous *originalChatMediaSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.byUser == nil {
		m.byUser = make(map[string]*originalChatMediaSession)
	}
	for key, old := range m.byUser {
		if time.Since(old.created) >= 24*time.Hour {
			delete(m.byUser, key)
		}
	}
	if s == nil {
		if m.byUser[user] == previous {
			delete(m.byUser, user)
		}
		return
	}
	m.byUser[user] = s
}

var (
	originalChatMediaSubscribe = regexp.MustCompile(`订阅[:：\s\p{Z}]*`)
	originalChatMediaWash      = regexp.MustCompile(`洗版[:：\s\p{Z}]*`)
	originalChatMediaSearch    = regexp.MustCompile(`(搜索|下载)[:：\s\p{Z}]*`)
	originalChatMediaLink      = regexp.MustCompile(`^(?:https?|ftps?|sftp|wss?)://|^[a-zA-Z0-9.-]+$`)
	originalChatMediaNonTitle  = regexp.MustCompile(`^#|^请[问帮你]|[?？]$|^继续$|帮我|请问|怎么|如何|为什么|可以|能否|推荐|介绍|谢谢|想看|找一下|搜一下|[，。！？!?,；;]`)
	originalChatMediaFeature   = regexp.MustCompile(`(?i)第\s*[0-9一二三四五六七八九十百零]+\s*[季集]|S\d{1,2}(?:E\d{1,4})?|E\d{1,4}|(?:19|20)\d{2}`)
	originalChatMediaSeparator = regexp.MustCompile(`[\s\p{Z}\-_.:：·'"()\[\]【】]+`)
	originalChatMediaWords     = regexp.MustCompile(`[\x{4e00}-\x{9fa5}]|[a-zA-Z]+`)
	originalChatMediaTitleChar = regexp.MustCompile(`[\x{4e00}-\x{9fff}A-Za-z]`)
)

func originalChatMediaAction(text string) (string, string) {
	switch {
	case strings.HasPrefix(text, "订阅"):
		return "Subscribe", originalChatMediaSubscribe.ReplaceAllString(text, "")
	case strings.HasPrefix(text, "洗版"):
		return "ReSubscribe", originalChatMediaWash.ReplaceAllString(text, "")
	case strings.HasPrefix(text, "搜索") || strings.HasPrefix(text, "下载"):
		return "ReSearch", originalChatMediaSearch.ReplaceAllString(text, "")
	}
	text = strings.Join(strings.Fields(text), " ")
	if text == "" || strings.Contains(text, "://") || strings.HasPrefix(text, "magnet:?") ||
		originalChatMediaLink.MatchString(text) || originalChatMediaNonTitle.MatchString(text) || len(originalChatMediaWords.FindAllStringIndex(text, 11)) > 10 {
		return "", ""
	}
	title := originalChatMediaSeparator.ReplaceAllString(originalChatMediaFeature.ReplaceAllString(text, ""), "")
	if utf8.RuneCountInString(title) < 2 || !originalChatMediaTitleChar.MatchString(title) {
		return "", ""
	}
	return "Search", text
}

func originalChatMediaIndex(value string) int {
	if value == "" || originalSiteDigits.FindString(value) != value {
		return -1
	}
	n, err := strconv.Atoi(originalSiteDecimal(value))
	if err != nil {
		return -1
	}
	return n
}

func (a *App) originalChatMediaText(ctx context.Context, reply originalSlashReply, text string) (bool, error) {
	text = originalIncomingStrip(text)
	action := ""
	switch strings.ToLower(text) {
	case "退出", "关闭", "q", "quit", "exit":
		action = "close"
	case "p", "prev", "上一页":
		action = "page-prev"
	case "n", "next", "下一页":
		action = "page-next"
	}
	index := originalChatMediaIndex(text)
	if action != "" || index >= 0 {
		s := a.pluginOriginalChatMedia.lock(reply.user)
		if s != nil {
			defer s.mu.Unlock()
			if action == "close" {
				a.pluginOriginalChatMedia.set(reply.user, nil, s)
				return true, reply.post(ctx, a, "媒体交互已结束", false)
			}
			if index >= 0 {
				action = s.selectionAction()
			}
			return true, a.originalChatMediaStep(ctx, reply, s, action, index, false)
		}
		if action != "close" {
			return true, reply.post(ctx, a, "输入有误！", false)
		}
	}
	action, keyword := originalChatMediaAction(text)
	if action == "" {
		return false, nil
	}
	meta, medias, err := a.originalChatSearchMedia(ctx, keyword)
	if err != nil {
		return true, err
	}
	if meta.Name() == "" {
		return true, reply.post(ctx, a, "无法识别输入内容！", false)
	}
	if len(medias) == 0 {
		return true, reply.post(ctx, a, meta.Name()+" 没有找到对应的媒体信息！", false)
	}
	var nonce [6]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return true, err
	}
	s := &originalChatMediaSession{id: hex.EncodeToString(nonce[:]), created: time.Now(), action: action, phase: "media", title: meta.Name(), meta: meta, media: medias}
	s.mu.Lock()
	defer s.mu.Unlock()
	a.pluginOriginalChatMedia.set(reply.user, s, nil)
	return true, a.renderOriginalChatMedia(ctx, reply, s, false)
}

func originalChatMediaCallback(data string) (id, action string, index int) {
	if strings.HasPrefix(data, "media:") {
		parts := strings.SplitN(data, ":", 5)
		if len(parts) < 3 {
			return "", "", -1
		}
		index := -1
		if len(parts) >= 4 {
			index = originalChatMediaIndex(parts[3])
		}
		return parts[1], parts[2], index
	}
	switch data {
	case "page_p":
		return "", "page-prev", -1
	case "page_n":
		return "", "page-next", -1
	}
	action, value, _ := strings.Cut(data, "_")
	if index := originalChatMediaIndex(value); index >= 0 && (action == "select" || action == "download") {
		return "", action, index
	}
	return "", "", -1
}

func (a *App) originalChatMediaCallback(ctx context.Context, reply originalSlashReply, data string) (bool, error) {
	id, action, index := originalChatMediaCallback(data)
	if action == "" {
		return false, nil
	}
	s := a.pluginOriginalChatMedia.lock(reply.user)
	if s != nil {
		defer s.mu.Unlock()
	}
	if s == nil || strings.HasPrefix(data, "media:") && id != s.id {
		return true, reply.post(ctx, a, "交互已失效，请重新搜索或订阅", false)
	}
	switch action {
	case "page-prev", "page-next", "select", "download", "download-dir":
		return true, a.originalChatMediaStep(ctx, reply, s, action, index, true)
	default:
		return false, nil
	}
}

func (s *originalChatMediaSession) selectionAction() string {
	switch s.phase {
	case "torrent":
		return "download"
	case "download-dir":
		return "download-dir"
	default:
		return "select"
	}
}

func (a *App) originalChatMediaStep(ctx context.Context, reply originalSlashReply, s *originalChatMediaSession, action string, index int, callback bool) error {
	start, end, pages := s.bounds()
	switch action {
	case "page-prev":
		if s.page == 0 {
			return reply.post(ctx, a, "已经是第一页了！", false)
		}
		s.page--
		return a.renderOriginalChatMedia(ctx, reply, s, callback)
	case "page-next":
		if s.page+1 >= pages {
			return reply.post(ctx, a, "已经是最后一页了！", false)
		}
		s.page++
		return a.renderOriginalChatMedia(ctx, reply, s, callback)
	}
	if action != s.selectionAction() || index > end-start || index < 1 && (action != "download" || index != 0) {
		return reply.post(ctx, a, "输入有误！", false)
	}
	switch action {
	case "select":
		return a.selectOriginalChatMedia(ctx, reply, s, s.media[start+index-1], callback)
	case "download-dir":
		dir := s.dirs[start+index-1]
		pending := s.pendingIndex
		s.phase, s.page, s.dirs = "torrent", s.torrentPage, nil
		return a.runOriginalChatMediaDownload(ctx, reply, s, pending, dir)
	default:
		candidate := -1
		if index > 0 {
			candidate = start + index - 1
		}
		return a.chooseOriginalChatMediaDirectory(ctx, reply, s, candidate, false)
	}
}

func (a *App) selectOriginalChatMedia(ctx context.Context, reply originalSlashReply, s *originalChatMediaSession, selection catalog.ExploreMedia, callback bool) error {
	selected, provider, primary, err := a.originalChatResolveMediaSelection(ctx, selection)
	if err != nil {
		return reply.post(ctx, a, a.clean(err.Error()), false)
	}
	ctx = originalChatWithMediaProvider(ctx, provider)
	if s.action == "Subscribe" || s.action == "ReSubscribe" {
		message, err := a.originalChatSubscribeMedia(ctx, reply, s.meta, selected, primary.OriginalMedia, s.action == "ReSubscribe")
		if err != nil {
			if pluginErrorCodeInternal(err) == "MEDIA_NOT_FOUND" || pluginErrorCodeInternal(err) == "MEDIA_SOURCE_CHANGED" {
				return reply.post(ctx, a, a.clean(err.Error()), false)
			}
			return err
		}
		if message != "" {
			return reply.post(ctx, a, message, false)
		}
		return nil
	}
	if selected.ID <= 0 && !provider.sourceOnly() {
		return reply.post(ctx, a, "无法识别所选媒体！", false)
	}
	title := originalChatMediaTitle(primary.Media)
	kind := "电视剧"
	if selected.MediaType == "movie" {
		kind = "电影"
	}
	notifySearch := func(seasons []originalChatMediaSeason) error {
		if selected.MediaType == "tv" {
			missing := originalChatMediaSeasonNotice(seasons, s.action == "Search")
			if missing != "" {
				if err := reply.post(ctx, a, title+"：\n"+missing, false); err != nil {
					return err
				}
			}
		}
		return reply.post(ctx, a, fmt.Sprintf("开始搜索 %s %s ...", kind, title), false)
	}
	request, candidates, err := a.originalChatMediaResources(ctx, s.meta, selected, primary.OriginalMedia, s.action == "ReSearch", notifySearch)
	season := ""
	if s.meta.Number("begin_season") != nil {
		season = s.meta.Season()
	}
	if errors.Is(err, errOriginalChatMediaExists) {
		return reply.post(ctx, a, "【"+title+season+" 媒体库中已存在，如需重新下载请发送：搜索 名称 或 下载 名称】", false)
	}
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		return reply.post(ctx, a, primary.Title+season+" 未搜索到需要的资源！", false)
	}
	s.selected, s.selectedDisplay, s.request, s.candidates = selected, primary.Media, request, candidates
	s.phase, s.page, s.title = "torrent", 0, primary.Title
	automatic := a.Settings().AutoDownloadUser
	if automatic == "all" || slices.Contains(strings.Split(automatic, ","), reply.user) {
		return a.chooseOriginalChatMediaDirectory(ctx, reply, s, -1, callback)
	}
	return a.renderOriginalChatMedia(ctx, reply, s, callback)
}

func (a *App) chooseOriginalChatMediaDirectory(ctx context.Context, reply originalSlashReply, s *originalChatMediaSession, index int, callback bool) error {
	dirs := a.originalChatMediaDirectories(s)
	if len(dirs) > 1 {
		s.torrentPage, s.pendingIndex = s.page, index
		s.phase, s.page = "download-dir", 0
		s.dirs = append([]originalChatMediaDirectory{{Name: "自动匹配目录"}}, dirs...)
		return a.renderOriginalChatMedia(ctx, reply, s, callback)
	}
	return a.runOriginalChatMediaDownload(ctx, reply, s, index, originalChatMediaDirectory{})
}
