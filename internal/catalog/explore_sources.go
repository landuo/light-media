package catalog

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"light-media/internal/logging"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type ExploreError struct {
	Source, Code string
	StatusCode   int
}

func (e *ExploreError) Error() string {
	name := map[string]string{"douban": "豆瓣", "bangumi": "Bangumi", "anilist": "AniList", "themoviedb": "TMDB"}[e.Source]
	if name == "" {
		name = "探索数据源"
	}
	switch e.Code {
	case "invalid_input":
		return name + "筛选参数无效"
	case "not_found":
		return name + "未找到该条目"
	case "rate_limited":
		return name + "请求过于频繁，请稍后重试"
	case "invalid_response":
		return name + "返回了无法处理的数据"
	case "unauthorized":
		return name + "暂时拒绝了访问请求，请稍后重试"
	case "canceled":
		return name + "请求已取消或超时"
	default:
		return name + "暂时不可用，请稍后重试"
	}
}

// Explorer uses public provider endpoints; no old MoviePilot service or user
// account is needed. A native owner supplies the application's outbound route.
type Explorer struct {
	tmdb *Client
	http *http.Client
}

func NewExplorer(tmdb *Client, transport http.RoundTripper) *Explorer {
	return &Explorer{tmdb: tmdb, http: &http.Client{Timeout: requestLimit, Transport: logging.Transport(transport), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (e *Explorer) CloseIdleConnections() {
	if e != nil && e.http != nil {
		e.http.CloseIdleConnections()
	}
}

var exploreRequestSlots = make(chan struct{}, 4)

func (e *Explorer) request(ctx context.Context, source, method, path string, params url.Values, body any, out any) error {
	errorSource := source
	if strings.HasPrefix(source, "anilist_") {
		errorSource = "anilist"
	}
	failure := func(code string, status int) error {
		return &ExploreError{Source: errorSource, Code: code, StatusCode: status}
	}
	host := map[string]string{"douban": "frodo.douban.com", "bangumi": "api.bgm.tv", "anilist": "graphql.anilist.co", "anilist_proxy": "trace.moe", "anilist_translations": "raw.githubusercontent.com"}[source]
	if ctx == nil || host == "" || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#\r\n") {
		return failure("invalid_input", 0)
	}
	ctx, cancel := context.WithTimeout(ctx, requestLimit)
	defer cancel()
	select {
	case exploreRequestSlots <- struct{}{}:
		defer func() { <-exploreRequestSlots }()
	case <-ctx.Done():
		return failure("canceled", 0)
	}
	query := url.Values{}
	for k, v := range params {
		query[k] = append([]string(nil), v...)
	}
	if source == "douban" {
		// Public client protocol constants used by MoviePilot's Frodo adapter;
		// these identify the client, and are not the user's account credentials.
		ts := time.Now().Format("20060102")
		sign := hmac.New(sha1.New, []byte("bf7dddc7c9cfe6f7"))
		sign.Write([]byte(method + "&" + url.QueryEscape(path) + "&" + ts))
		query.Set("apiKey", "0dad551ec0f84ed02907ff5c42e8ec70")
		query.Set("os_rom", "android")
		query.Set("_ts", ts)
		query.Set("_sig", base64.StdEncoding.EncodeToString(sign.Sum(nil)))
	}
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return failure("invalid_input", 0)
		}
		reader = bytes.NewReader(data)
	}
	u := url.URL{Scheme: "https", Host: host, Path: path, RawQuery: query.Encode()}
	req, err := http.NewRequestWithContext(logging.EndpointContext(ctx, "metadata", u.String()), method, u.String(), reader)
	if err != nil {
		return failure("invalid_input", 0)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "light-media/1.0 (metadata discovery)")
	if source == "douban" {
		req.Header.Set("User-Agent", "api-client/1 com.douban.frodo/7.3.0(207) Android/22 product/MI 9 vendor/Xiaomi model/MI 9 brand/Android rom/miui6 network/wifi platform/mobile nd/1")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := e.http.Do(req)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return failure("canceled", 0)
		}
		return failure("unavailable", 0)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		code := "unavailable"
		switch response.StatusCode {
		case 401, 403:
			code = "unauthorized"
		case 404:
			code = "not_found"
		case 429:
			code = "rate_limited"
		}
		return failure(code, response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil || len(data) > maxBody {
		return failure("invalid_response", 0)
	}
	var envelope struct {
		Code  int             `json:"code"`
		Error json.RawMessage `json:"error"`
	}
	arrayResponse := source == "anilist_translations" || source == "bangumi" && path == "/calendar"
	if !arrayResponse && (decode(data, &envelope) != nil || envelope.Code != 0 || len(envelope.Error) > 0 && string(envelope.Error) != "null" && string(envelope.Error) != "\"\"") {
		return failure("invalid_response", 0)
	}
	if decode(data, out) != nil {
		return failure("invalid_response", 0)
	}
	return nil
}

func (e *Explorer) Explore(ctx context.Context, q ExploreQuery) (ExplorePage, error) {
	switch q.Source {
	case "themoviedb":
		return e.tmdb.Explore(ctx, q)
	case "douban":
		return e.exploreDouban(ctx, q)
	case "bangumi":
		return e.exploreBangumi(ctx, q)
	case "anilist":
		return e.exploreAniList(ctx, q)
	}
	return ExplorePage{}, &ExploreError{Source: q.Source, Code: "invalid_input"}
}

func ValidExploreIdentity(source, kind, id string) bool {
	n, err := strconv.ParseInt(id, 10, 64)
	return err == nil && validID(n) && strconv.FormatInt(n, 10) == id && (source == "douban" && kindOK(kind) || source == "bangumi" || source == "anilist" || source == "themoviedb" && kindOK(kind))
}

func (e *Explorer) Detail(ctx context.Context, source, kind, id string) (ExploreMedia, error) {
	if !ValidExploreIdentity(source, kind, id) {
		return ExploreMedia{}, &ExploreError{Source: source, Code: "invalid_input"}
	}
	switch source {
	case "themoviedb":
		n, _ := strconv.ParseInt(id, 10, 64)
		m, err := e.tmdb.Detail(ctx, kind, n)
		return ExploreMedia{Media: m, Source: source, SourceID: id}, err
	case "douban":
		var raw doubanExploreMedia
		if err := e.request(ctx, source, http.MethodGet, "/api/v2/"+kind+"/"+id, nil, nil, &raw); err != nil {
			return ExploreMedia{}, err
		}
		m, err := decodeDoubanExplore(raw, kind)
		if err == nil && m.SourceID != id {
			err = &ExploreError{Source: source, Code: "invalid_response"}
		}
		return m, err
	case "bangumi":
		var raw bangumiExploreMedia
		if err := e.request(ctx, source, http.MethodGet, "/v0/subjects/"+id, nil, nil, &raw); err != nil {
			return ExploreMedia{}, err
		}
		m, err := decodeBangumiExplore(raw)
		if err == nil && m.SourceID != id {
			err = &ExploreError{Source: source, Code: "invalid_response"}
		}
		return m, err
	case "anilist":
		return e.detailAniList(ctx, id)
	}
	return ExploreMedia{}, &ExploreError{Source: source, Code: "invalid_input"}
}

// Only provider-owned image origins are accepted, both when decoding metadata
// and immediately before an image proxy request. User-supplied hosts are never
// fetched, and credential-bearing URLs and redirects are not followed.
var doubanImageHost = regexp.MustCompile(`^img[1-9]?\.douban(?:io)?\.com$`)

func ExploreImageURL(source, raw string) string {
	if raw == "" || len(raw) > 2048 || strings.ContainsAny(raw, "\r\n\x00") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Scheme != "https" || u.Port() != "" || u.Fragment != "" || u.Path == "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	allowed := source == "douban" && doubanImageHost.MatchString(host) || source == "bangumi" && host == "lain.bgm.tv" || source == "anilist" && (host == "s4.anilist.co" || host == "s3.anilist.co")
	if !allowed {
		return ""
	}
	return u.String()
}

type exploreScalar string

func (s *exploreScalar) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*s = ""
		return nil
	}
	var v string
	if len(data) > 0 && data[0] == '"' {
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
	} else {
		var n json.Number
		if err := json.Unmarshal(data, &n); err != nil {
			return err
		}
		v = n.String()
	}
	*s = exploreScalar(v)
	return nil
}

type doubanExploreMedia struct {
	ID            exploreScalar `json:"id"`
	Type          string        `json:"type"`
	Title         string        `json:"title"`
	OriginalTitle string        `json:"original_title"`
	Year          exploreScalar `json:"year"`
	Intro         string        `json:"intro"`
	CardSubtitle  string        `json:"card_subtitle"`
	CoverURL      string        `json:"cover_url"`
	Pic           struct {
		Large  string `json:"large"`
		Normal string `json:"normal"`
	} `json:"pic"`
	Rating struct {
		Value float64 `json:"value"`
		Count int     `json:"count"`
	} `json:"rating"`
	Genres   []string `json:"genres"`
	Pubdate  []string `json:"pubdate"`
	IMDb     string   `json:"imdb_id"`
	IMDbAlt  string   `json:"imdb"`
	Episodes int      `json:"episodes_count"`
	AKA      []string `json:"aka"`
}

func decodeDoubanExplore(raw doubanExploreMedia, kind string) (ExploreMedia, error) {
	invalid := func() (ExploreMedia, error) {
		return ExploreMedia{}, &ExploreError{Source: "douban", Code: "invalid_response"}
	}
	if !ValidExploreIdentity("douban", kind, string(raw.ID)) || raw.Type != "" && raw.Type != kind || strings.TrimSpace(raw.Title) == "" || raw.Rating.Value < 0 || raw.Rating.Value > 10 || raw.Rating.Count < 0 {
		return invalid()
	}
	clean := (&Client{}).text
	m := Media{Title: clean(raw.Title, 512), OriginalTitle: clean(raw.OriginalTitle, 512), MediaType: kind, Overview: clean(raw.Intro, 32000), VoteAverage: raw.Rating.Value, VoteCount: raw.Rating.Count, NumberOfEpisodes: raw.Episodes, Genres: []Genre{}, Aliases: []string{}}
	if m.Overview == "" {
		m.Overview = clean(raw.CardSubtitle, 4096)
	}
	if n, err := strconv.Atoi(string(raw.Year)); err == nil && n >= 1800 && n <= 2199 {
		m.Year = n
	}
	for _, v := range raw.Pubdate {
		if len(v) >= 10 {
			if d, y, ok := date(v[:10]); ok {
				m.ReleaseDate = d
				m.Year = y
				break
			}
		}
	}
	for _, v := range []string{raw.Pic.Large, raw.Pic.Normal, raw.CoverURL} {
		if m.PosterPath == "" {
			m.PosterPath = ExploreImageURL("douban", v)
		}
	}
	for _, v := range raw.Genres {
		if len(m.Genres) < 50 {
			m.Genres = append(m.Genres, Genre{Name: clean(v, 128)})
		}
	}
	for _, v := range raw.AKA {
		if queryOK(v) && len(m.Aliases) < 16 {
			m.Aliases = append(m.Aliases, clean(v, 512))
		}
	}
	for _, v := range []string{raw.IMDb, raw.IMDbAlt} {
		v = strings.TrimSpace(v)
		if imdbTitleID.MatchString(v) {
			m.IMDbID = v
			break
		}
	}
	return ExploreMedia{Media: m, Source: "douban", SourceID: string(raw.ID)}, nil
}

func (e *Explorer) exploreDouban(ctx context.Context, q ExploreQuery) (ExplorePage, error) {
	var raw struct {
		Items []doubanExploreMedia `json:"items"`
		Total *int                 `json:"total"`
		Start *int                 `json:"start"`
	}
	if err := e.request(ctx, "douban", http.MethodGet, "/api/v2/"+q.Kind+"/recommend", q.Params, nil, &raw); err != nil {
		return ExplorePage{}, err
	}
	if raw.Items == nil || len(raw.Items) > q.Count || raw.Start != nil && *raw.Start != (q.Page-1)*q.Count || raw.Total != nil && *raw.Total < 0 {
		return ExplorePage{}, &ExploreError{Source: "douban", Code: "invalid_response"}
	}
	p := explorePagination(q, len(raw.Items), raw.Total)
	for _, item := range raw.Items {
		m, err := decodeDoubanExplore(item, q.Kind)
		if err != nil {
			return ExplorePage{}, err
		}
		// The original source omits placeholder images, but has_more is based on
		// raw provider count so image omissions cannot truncate the next page.
		if m.PosterPath == "" || strings.Contains(m.PosterPath, "movie_large.jpg") || strings.Contains(m.PosterPath, "tv_normal.") || strings.Contains(m.PosterPath, "tv_large.jpg") {
			continue
		}
		p.Results = append(p.Results, m)
	}
	return p, nil
}

type bangumiExploreMedia struct {
	ID       int64  `json:"id"`
	Type     int    `json:"type"`
	Name     string `json:"name"`
	NameCN   string `json:"name_cn"`
	Summary  string `json:"summary"`
	Date     string `json:"date"`
	Platform string `json:"platform"`
	Images   struct {
		Large  string `json:"large"`
		Common string `json:"common"`
		Medium string `json:"medium"`
	} `json:"images"`
	Rating struct {
		Score float64 `json:"score"`
		Total int     `json:"total"`
	} `json:"rating"`
	Eps           int `json:"eps"`
	TotalEpisodes int `json:"total_episodes"`
	Infobox       []struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	} `json:"infobox"`
}

func decodeBangumiExplore(raw bangumiExploreMedia) (ExploreMedia, error) {
	if !validID(raw.ID) || raw.Type != 2 || raw.Rating.Score < 0 || raw.Rating.Score > 10 || raw.Rating.Total < 0 {
		return ExploreMedia{}, &ExploreError{Source: "bangumi", Code: "invalid_response"}
	}
	clean := (&Client{}).text
	title := raw.NameCN
	if title == "" {
		title = raw.Name
	}
	if strings.TrimSpace(title) == "" {
		return ExploreMedia{}, &ExploreError{Source: "bangumi", Code: "invalid_response"}
	}
	kind := "tv"
	if raw.Platform == "剧场版" || strings.EqualFold(raw.Platform, "Movie") {
		kind = "movie"
	}
	m := Media{Title: clean(title, 512), OriginalTitle: clean(raw.Name, 512), MediaType: kind, Overview: clean(raw.Summary, 32000), VoteAverage: raw.Rating.Score, VoteCount: raw.Rating.Total, Aliases: []string{}, NumberOfEpisodes: raw.TotalEpisodes}
	if m.NumberOfEpisodes == 0 {
		m.NumberOfEpisodes = raw.Eps
	}
	if d, y, ok := date(raw.Date); ok {
		m.ReleaseDate = d
		m.Year = y
	}
	for _, v := range []string{raw.Images.Large, raw.Images.Common, raw.Images.Medium} {
		if m.PosterPath == "" {
			m.PosterPath = ExploreImageURL("bangumi", v)
		}
	}
	for _, box := range raw.Infobox {
		if box.Key == "别名" {
			var names []struct {
				Value string `json:"v"`
			}
			if json.Unmarshal(box.Value, &names) == nil {
				for _, n := range names {
					if queryOK(n.Value) && len(m.Aliases) < 16 {
						m.Aliases = append(m.Aliases, clean(n.Value, 512))
					}
				}
			}
		}
		if strings.EqualFold(box.Key, "imdb") {
			var id string
			if json.Unmarshal(box.Value, &id) == nil && imdbTitleID.MatchString(id) {
				m.IMDbID = id
			}
		}
	}
	return ExploreMedia{Media: m, Source: "bangumi", SourceID: strconv.FormatInt(raw.ID, 10)}, nil
}

func (e *Explorer) exploreBangumi(ctx context.Context, q ExploreQuery) (ExplorePage, error) {
	var raw struct {
		Data   []bangumiExploreMedia `json:"data"`
		Total  *int                  `json:"total"`
		Offset *int                  `json:"offset"`
	}
	if err := e.request(ctx, "bangumi", http.MethodGet, "/v0/subjects", q.Params, nil, &raw); err != nil {
		return ExplorePage{}, err
	}
	if raw.Data == nil || len(raw.Data) > q.Count || raw.Total == nil || *raw.Total < 0 || raw.Offset != nil && *raw.Offset != (q.Page-1)*q.Count {
		return ExplorePage{}, &ExploreError{Source: "bangumi", Code: "invalid_response"}
	}
	p := explorePagination(q, len(raw.Data), raw.Total)
	for _, item := range raw.Data {
		m, err := decodeBangumiExplore(item)
		if err != nil {
			return ExplorePage{}, err
		}
		p.Results = append(p.Results, m)
	}
	return p, nil
}

func explorePagination(q ExploreQuery, count int, total *int) ExplorePage {
	p := ExplorePage{Results: []ExploreMedia{}, Page: q.Page, TotalPages: 0, TotalResults: 0, HasMore: count == q.Count && q.Page < 500}
	if total != nil {
		p.TotalResults = *total
		p.TotalPages = min(500, (*total+q.Count-1)/q.Count)
		p.HasMore = q.Page < p.TotalPages && count > 0
	}
	// Zero total_pages means the source did not report a total. has_more is
	// still authoritative and is based on raw source count, not displayed cards.
	return p
}
