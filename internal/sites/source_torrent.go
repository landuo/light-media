package sites

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"light-media/internal/releasemeta/legacyanime"
	"light-media/internal/rsscontext"
)

var ErrSourceTorrentDependency = errors.New("source_torrent_dependency")

// A fresh adapter response stays private. Neither the raw source row nor its
// config binding survives Resource's public JSON serialization/cache boundary.
// In particular source enclosure may contain the API key and is never a public
// download URL. The host must supply its independently verified indexer/site
// identity; this snapshot cannot prove an original profile or allocate an ID.
type sourceTorrentRecord struct {
	row                      json.RawMessage
	configHash, resourceHash [32]byte
}

func sourceDigest(value any) [32]byte {
	raw, _ := json.Marshal(value)
	return sha256.Sum256(raw)
}

func (c *Client) captureMTeamSource(resource *Resource, row map[string]any) {
	raw, err := json.Marshal(row)
	if err == nil {
		resource.sourceRecord = &sourceTorrentRecord{row: raw, configHash: sourceDigest(c.config), resourceHash: sourceDigest(resource)}
	}
}

// SourceMTeamTorrent uses a fresh bound response and a trusted full indexer.
// domain is StringUtils.get_url_domain(indexer['domain']), not a guessed native
// API origin. proxy is the selected source settings.PROXY and must be known if
// indexer.proxy is true. location is the captured OS local timezone. An absent
// dependency is distinct from a real source None. No request is performed here.
func (c *Client) SourceMTeamTorrent(resource Resource, sourceID int64, indexer map[string]any, domain string, proxy any, proxyKnown bool, location *time.Location) (*rsscontext.TorrentInfo, []string, error) {
	// sourceID comes from the host's active native_site_identities lookup, not
	// a title, site priority, public Resource or an allocated fallback zero.
	id, idErr := sourceInteger(indexer["id"])
	key, keyOK := indexer["apikey"].(string)
	if sourceID < 1 || idErr != nil || !id.IsInt64() || id.Int64() != sourceID || !keyOK || key != c.config.APIKey {
		return nil, nil, ErrSourceTorrentDependency
	}
	r := resource.sourceRecord
	if c.config.Kind != "mteam" || r == nil || r.configHash != sourceDigest(c.config) || r.resourceHash != sourceDigest(resource) || resource.SiteID != c.config.ID {
		return nil, nil, ErrSourceTorrentDependency
	}
	var row map[string]any
	if !decodeJSON(r.row, &row) {
		return nil, nil, ErrSourceTorrentDependency
	}
	return BuildSourceMTeamTorrent(row, indexer, domain, proxy, proxyKnown, location)
}

// BuildSourceMTeamTorrent ports the installed MTorrentSpider result method and
// IndexerModule's seven site fields. It consumes source-shaped values, without
// the public Resource's normalized/augmented subtitle and dates. Missing raw
// keys are known None; missing indexer/global provenance is a host dependency.
func BuildSourceMTeamTorrent(row, indexer map[string]any, domain string, proxy any, proxyKnown bool, location *time.Location) (*rsscontext.TorrentInfo, []string, error) {
	if row == nil || indexer == nil || domain == "" || location == nil || sourceTruth(indexer["proxy"]) && !proxyKnown {
		return nil, nil, ErrSourceTorrentDependency
	}
	diagnostics := []string{}
	category := "未知"
	if value, ok := row["category"].(string); ok {
		if strings.Contains("|401|419|420|421|439|405|404|", "|"+value+"|") && value != "" {
			category = "电影"
		} else if strings.Contains("|403|402|435|438|404|405|", "|"+value+"|") && value != "" {
			category = "电视剧"
		}
	}
	labels := any([]any{})
	if sourceTruth(row["labelsNew"]) {
		labels = row["labelsNew"]
	} else {
		key := row["labels"]
		if !sourceTruth(key) {
			key = "0"
		}
		if !sourceHashable(key) {
			return nil, diagnostics, errors.New("TypeError")
		}
		if key, ok := key.(string); ok {
			value := map[string]string{"1": "DIY", "2": "国配", "3": "DIY 国配", "4": "中字", "5": "DIY 中字", "6": "国配 中字", "7": "DIY 国配 中字"}[key]
			for _, part := range strings.Fields(value) {
				labels = append(labels.([]any), part)
			}
		}
	}
	// The source evaluates dictionary values in this order; conversion failures
	// must not be replaced by a later status.get failure or a compact UI value.
	enclosure, err := sourceMTeamEnclosure(row["id"], indexer, domain, proxy)
	if err != nil {
		return nil, diagnostics, err
	}
	pubdate := sourceTimestamp(row["createdDate"], location, &diagnostics)
	size, err := sourceIntOrZero(row["size"])
	if err != nil {
		return nil, diagnostics, err
	}
	status := map[string]any{}
	if value, exists := row["status"]; exists {
		var ok bool
		status, ok = value.(map[string]any)
		if !ok {
			return nil, diagnostics, errors.New("AttributeError")
		}
	}
	counts := map[string]any{}
	for _, key := range []string{"seeders", "leechers", "timesCompleted"} {
		counts[key], err = sourceIntOrZero(status[key])
		if err != nil {
			return nil, diagnostics, err
		}
	}
	download, err := sourceDiscount(status["discount"], false)
	if err != nil {
		return nil, diagnostics, err
	}
	upload, err := sourceDiscount(status["discount"], true)
	if err != nil {
		return nil, diagnostics, err
	}
	imdb := ""
	if sourceTruth(row["imdb"]) {
		text, ok := row["imdb"].(string)
		if !ok {
			return nil, diagnostics, errors.New("TypeError")
		}
		imdb = sourceIMDB.FindString(text)
	}
	url, ok := indexer["domain"].(string)
	if !ok {
		return nil, diagnostics, ErrSourceTorrentDependency
	}
	fields := map[string]any{
		"title": row["name"], "description": row["smallDescr"], "enclosure": enclosure,
		"pubdate": pubdate, "size": size, "seeders": counts["seeders"], "peers": counts["leechers"], "grabs": counts["timesCompleted"],
		"downloadvolumefactor": download, "uploadvolumefactor": upload,
		"page_url": url + "detail/" + sourceString(row["id"]), "imdbid": imdb, "labels": labels, "category": category,
	}
	if sourceTruth(status["discountEndTime"]) {
		fields["freedate"] = sourceTimestamp(status["discountEndTime"], location, &diagnostics)
	}
	if value := status["promotionRule"]; sourceTruth(value) {
		promotion, ok := value.(map[string]any)
		if !ok {
			return nil, diagnostics, errors.New("AttributeError")
		}
		fields["downloadvolumefactor"], err = sourceDiscount(promotion["discount"], false)
		if err != nil {
			return nil, diagnostics, err
		}
		if sourceTruth(promotion["endTime"]) {
			fields["freedate"] = sourceTimestamp(promotion["endTime"], location, &diagnostics)
		}
	}
	if value := status["mallSingleFree"]; sourceTruth(value) {
		mall, ok := value.(map[string]any)
		if !ok {
			return nil, diagnostics, errors.New("AttributeError")
		}
		if mall["status"] == "ONGOING" {
			fields["downloadvolumefactor"] = 0
			if sourceTruth(mall["endDate"]) {
				fields["freedate"] = sourceTimestamp(mall["endDate"], location, &diagnostics)
			}
		}
	}
	for key, source := range map[string]string{"site": "id", "site_name": "name", "site_cookie": "cookie", "site_ua": "ua", "site_proxy": "proxy", "site_order": "pri", "site_downloader": "downloader"} {
		fields[key] = indexer[source]
	}
	torrent, err := rsscontext.NewTorrentInfo(fields)
	return torrent, diagnostics, err
}

var sourceIMDB = regexp.MustCompile(`tt\p{Nd}+`)

func sourceTruth(v any) bool {
	switch v := v.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case json.Number:
		if !strings.ContainsAny(string(v), ".eE") {
			n, ok := new(big.Int).SetString(string(v), 10)
			return ok && n.Sign() != 0
		}
		f, e := strconv.ParseFloat(string(v), 64)
		return (e == nil || math.IsInf(f, 0)) && f != 0
	case int:
		return v != 0
	case int64:
		return v != 0
	case float64:
		return v != 0
	case map[string]any:
		return len(v) != 0
	case []any:
		return len(v) != 0
	case []string:
		return len(v) != 0
	}
	return true
}

func sourceHashable(v any) bool {
	switch v.(type) {
	case map[string]any, []any, []string:
		return false
	}
	return true
}

func sourceInteger(v any) (*big.Int, error) {
	switch v := v.(type) {
	case nil:
		return nil, errors.New("TypeError")
	case bool:
		if v {
			return big.NewInt(1), nil
		}
		return big.NewInt(0), nil
	case string:
		n, e := legacyanime.Integer(v)
		if e != nil {
			return nil, errors.New("ValueError")
		}
		return n, nil
	case int:
		return big.NewInt(int64(v)), nil
	case int64:
		return big.NewInt(v), nil
	case json.Number:
		if !strings.ContainsAny(string(v), ".eE") {
			n, ok := new(big.Int).SetString(string(v), 10)
			if ok {
				return n, nil
			}
		}
		f, e := strconv.ParseFloat(string(v), 64)
		if e != nil && !math.IsInf(f, 0) {
			return nil, errors.New("ValueError")
		}
		return sourceInteger(f)
	case float64:
		if math.IsNaN(v) {
			return nil, errors.New("ValueError")
		}
		if math.IsInf(v, 0) {
			return nil, errors.New("OverflowError")
		}
		n, _ := new(big.Float).SetFloat64(v).Int(nil)
		return n, nil
	}
	return nil, errors.New("TypeError")
}

func sourceIntOrZero(v any) (any, error) {
	if !sourceTruth(v) {
		return json.Number("0"), nil
	}
	n, e := sourceInteger(v)
	if e != nil {
		return nil, e
	}
	return json.Number(n.String()), nil
}

func sourceTimestamp(v any, location *time.Location, diagnostics *[]string) any {
	if text, ok := v.(string); ok {
		if !legacyanime.IsDigit(text) {
			return v
		}
	}
	n, e := sourceInteger(v)
	if e != nil {
		*diagnostics = append(*diagnostics, e.Error())
		return v
	}
	if !n.IsInt64() {
		*diagnostics = append(*diagnostics, "OverflowError")
		return v
	}
	at := time.Unix(n.Int64(), 0).In(location)
	if at.Year() < 1 || at.Year() > 9999 {
		*diagnostics = append(*diagnostics, "ValueError")
		return v
	}
	return strconv.Itoa(at.Year()) + at.Format("-01-02 15:04:05")
}

func sourceDiscount(v any, upload bool) (any, error) {
	if !sourceTruth(v) {
		return 1, nil
	}
	if !sourceHashable(v) {
		return nil, errors.New("TypeError")
	}
	text, _ := v.(string)
	if upload {
		if text == "_2X" || text == "_2X_FREE" || text == "_2X_PERCENT_50" {
			return float64(2), nil
		}
		return 1, nil
	}
	switch text {
	case "FREE", "_2X_FREE":
		return 0, nil
	case "PERCENT_50", "_2X_PERCENT_50":
		return .5, nil
	case "PERCENT_70":
		return .3, nil
	}
	return 1, nil
}

func sourceString(v any) string {
	switch v := v.(type) {
	case nil:
		return "None"
	case string:
		return v
	case bool:
		if v {
			return "True"
		}
		return "False"
	case json.Number:
		return string(v)
	}
	return fmt.Sprint(v)
}

func sourceJSONString(v any) (string, error) {
	if text, ok := v.(string); ok && !utf8.ValidString(text) {
		return "", ErrSourceTorrentDependency
	}
	if v == nil {
		return "null", nil
	}
	if text, ok := v.(string); ok {
		var b strings.Builder
		b.WriteByte('"')
		for _, r := range text {
			switch r {
			case '"', '\\':
				b.WriteByte('\\')
				b.WriteRune(r)
			case '\b':
				b.WriteString(`\b`)
			case '\f':
				b.WriteString(`\f`)
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			default:
				if r < 32 || r > 126 {
					if r <= 0xffff {
						fmt.Fprintf(&b, `\u%04x`, r)
					} else {
						a, c := utf16.EncodeRune(r)
						fmt.Fprintf(&b, `\u%04x\u%04x`, a, c)
					}
				} else {
					b.WriteRune(r)
				}
			}
		}
		b.WriteByte('"')
		return b.String(), nil
	}
	switch v := v.(type) {
	case bool:
		return strconv.FormatBool(v), nil
	case json.Number:
		if json.Valid([]byte(v)) {
			return string(v), nil
		}
	case int:
		return strconv.Itoa(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	}
	return "", ErrSourceTorrentDependency
}

func sourceMTeamEnclosure(id any, indexer map[string]any, domain string, proxy any) (string, error) {
	idJSON, e := sourceJSONString(id)
	if e != nil {
		return "", e
	}
	ua, e := sourceJSONString(sourceString(indexer["ua"]))
	if e != nil {
		return "", e
	}
	key, e := sourceJSONString(indexer["apikey"])
	if e != nil {
		return "", e
	}
	params := `{"method": "post", "cookie": false, "params": {"id": ` + idJSON + `}, "header": {"User-Agent": ` + ua + `, "Accept": "application/json, text/plain, */*", "x-api-key": ` + key + `}, "proxy": ` + strconv.FormatBool(sourceTruth(indexer["proxy"]) && sourceTruth(proxy)) + `, "result": "data"}`
	return "[" + base64.StdEncoding.EncodeToString([]byte(params)) + "]https://api." + domain + "/api/torrent/genDlToken", nil
}

// encoding/json replaces isolated UTF-16 surrogates and invalid UTF-8. The
// ordinary UI can retain its existing decoder, but an altered value cannot be
// claimed as a faithful source object. Valid surrogate pairs remain supported.
func sourceJSONLossless(raw []byte) bool {
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return false
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if raw[i] != 'u' {
			continue
		}
		n, e := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if e != nil {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, e := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if e != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
