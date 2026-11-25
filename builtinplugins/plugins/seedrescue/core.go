package seedrescue

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const PluginID = "SeedRescue"
const Version = "1.0.0"
const Free = "free"
const NonFree = "non_free"
const Unknown = "unknown"

type Object = map[string]any

type Match struct {
	SID       any    `json:"sid"`
	TorrentID string `json:"torrent_id"`
	InfoHash  string `json:"info_hash"`
}
type Candidate struct {
	Match          Match
	SiteID         int
	SiteName       string
	Seeders        *int
	DownloadFactor *float64
	Priority       int
	Torrent        Object
	MetadataStatus string
	MetadataError  *string
	LookupMethod   *string
}
type SourceError struct{ Class, Operation, Message string }

func (e *SourceError) Error() string { return e.Message }
func ErrorClass(err error) string {
	var e *SourceError
	if errors.As(err, &e) {
		return e.Class
	}
	return "HostError"
}
func fault(class, op, msg string) error { return &SourceError{class, op, msg} }

var integerSyntax = regexp.MustCompile(`^[+-]?[0-9]+(?:_[0-9]+)*$`)

func byteInteger(raw []byte) (*big.Int, bool) {
	s := strings.Trim(string(raw), " \t\n\r\v\f")
	if !integerSyntax.MatchString(s) {
		return nil, false
	}
	s = strings.ReplaceAll(s, "_", "")
	digits := strings.TrimLeft(s, "+-")
	if len(digits) > 4300 {
		return nil, false
	}
	return new(big.Int).SetString(s, 10)
}

func readBytes(data []byte, offset int) ([]byte, int, error) {
	if offset < 0 || offset > len(data) {
		return nil, 0, fault("TorrentFormatError", "bencode", "无效的字符串长度")
	}
	colon := bytes.IndexByte(data[offset:], ':')
	if colon < 0 {
		return nil, 0, fault("TorrentFormatError", "bencode", "无效的字符串长度")
	}
	colon += offset
	n, valid := byteInteger(data[offset:colon])
	if !valid {
		return nil, 0, fault("TorrentFormatError", "bencode", "无效的字符串长度")
	}
	start := colon + 1
	if n.Sign() < 0 || !n.IsInt64() || n.Int64() > int64(len(data)-start) {
		return nil, 0, fault("TorrentFormatError", "bencode", "字符串超出种子数据范围")
	}
	return data[start : start+int(n.Int64())], start + int(n.Int64()), nil
}
func skipValue(data []byte, offset, depth int) (int, error) {
	if depth > 1000 {
		return 0, fault("RecursionError", "bencode", "bencode递归超出限制")
	}
	if offset >= len(data) {
		return 0, fault("TorrentFormatError", "bencode", "种子数据意外结束")
	}
	switch data[offset] {
	case 'i':
		end := bytes.IndexByte(data[offset+1:], 'e')
		if end < 0 {
			return 0, fault("TorrentFormatError", "bencode", "无效的整数")
		}
		end += offset + 1
		if _, ok := byteInteger(data[offset+1 : end]); !ok {
			return 0, fault("TorrentFormatError", "bencode", "无效的整数")
		}
		return end + 1, nil
	case 'l', 'd':
		token := data[offset]
		offset++
		for offset < len(data) && data[offset] != 'e' {
			var e error
			if token == 'd' {
				_, offset, e = readBytes(data, offset)
				if e != nil {
					return 0, e
				}
			}
			offset, e = skipValue(data, offset, depth+1)
			if e != nil {
				return 0, e
			}
		}
		if offset >= len(data) {
			word := "列表"
			if token == 'd' {
				word = "字典"
			}
			return 0, fault("TorrentFormatError", "bencode", word+"没有结束标记")
		}
		return offset + 1, nil
	default:
		if data[offset] >= '0' && data[offset] <= '9' {
			_, end, e := readBytes(data, offset)
			return end, e
		}
	}
	return 0, fault("TorrentFormatError", "bencode", "未知的 bencode 类型")
}
func InfoHashFromTorrent(content []byte) (string, error) {
	if len(content) == 0 || content[0] != 'd' {
		return "", fault("TorrentFormatError", "bencode", "种子顶层必须是字典")
	}
	offset := 1
	for offset < len(content) && content[offset] != 'e' {
		key, end, e := readBytes(content, offset)
		if e != nil {
			return "", e
		}
		start := end
		offset, e = skipValue(content, start, 0)
		if e != nil {
			return "", e
		}
		if string(key) == "info" {
			h := sha1.Sum(content[start:offset])
			return hex.EncodeToString(h[:]), nil
		}
	}
	return "", fault("TorrentFormatError", "bencode", "种子缺少 info 字典")
}
func floatValue(v any) (float64, bool) {
	switch x := v.(type) {
	case nil:
		return 0, false
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		n, e := x.Float64()
		return n, e == nil
	case string:
		n, e := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return n, e == nil
	}
	return 0, false
}
func PromotionKind(v any) string {
	if v == nil || v == "" {
		return Unknown
	}
	f, ok := floatValue(v)
	if !ok {
		return Unknown
	}
	if f == 0 {
		return Free
	}
	if f > 0 {
		return NonFree
	}
	return Unknown
}
func NormalizePromotions(v any) []string {
	values := array(v)
	if s, ok := v.(string); ok {
		values = []any{s}
	}
	seen := map[string]bool{}
	out := []string{}
	for _, x := range values {
		s := pyString(x)
		if (s == Free || s == NonFree) && !seen[s] {
			out = append(out, s)
			seen[s] = true
		}
	}
	sort.Strings(out)
	return out
}
func RankedSources(candidates []Candidate, minimum *int, allowed []string) []Candidate {
	out := []Candidate{}
	for _, c := range candidates {
		if c.MetadataStatus == "ready" && (minimum == nil || (c.Seeders != nil && *c.Seeders >= *minimum)) && contains(allowed, PromotionKind(deref(c.DownloadFactor))) {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		ai, bi := 0, 0
		if a.Seeders != nil {
			ai = *a.Seeders
		}
		if b.Seeders != nil {
			bi = *b.Seeders
		}
		if ai != bi {
			return ai > bi
		}
		af, bf := PromotionKind(deref(a.DownloadFactor)) == Free, PromotionKind(deref(b.DownloadFactor)) == Free
		if af != bf {
			return af
		}
		return a.Priority < b.Priority
	})
	return out
}
func MatchesForHash(payload any, target string) []Match {
	result := []Match{}
	root, ok := payload.(map[string]any)
	if !ok || target == "" {
		return result
	}
	seed, exists := root[target]
	if !exists || seed == nil {
		for k, v := range root {
			if strings.ToLower(k) == strings.ToLower(target) {
				seed = v
				break
			}
		}
	}
	row, ok := seed.(map[string]any)
	if !ok {
		return result
	}
	values := array(row["torrent"])
	if one, ok := row["torrent"].(map[string]any); ok {
		values = []any{one}
	}
	seen := map[string]bool{}
	for _, v := range values {
		r, ok := v.(map[string]any)
		if !ok {
			continue
		}
		sid, tid := r["sid"], r["torrent_id"]
		hash := strings.ToLower(strOr(r["info_hash"], ""))
		if sid == nil || sid == "" || tid == nil || tid == "" || utf8.RuneCountInString(hash) != 40 {
			continue
		}
		identity := numericKey(sid) + ":" + pyString(tid) + ":" + hash
		if seen[identity] {
			continue
		}
		seen[identity] = true
		result = append(result, Match{sid, pyString(tid), hash})
	}
	return result
}
func numericKey(v any) string {
	if f, ok := floatValue(v); ok {
		if _, str := v.(string); !str {
			return "n:" + strconv.FormatFloat(f, 'g', -1, 64)
		}
	}
	return fmt.Sprintf("%T:%s", v, pyString(v))
}
func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !unicode.IsDigit(c) {
			return false
		}
	}
	return true
}
func ExtractTorrentID(raw string) string {
	if raw == "" {
		return ""
	}
	unescaped, e := url.PathUnescape(raw)
	if e != nil {
		unescaped = raw
	}
	u, e := url.Parse(unescaped)
	if e != nil {
		return ""
	}
	query := u.Query()
	for _, key := range []string{"id", "torrentid", "torrent_id", "tid"} {
		if values := query[key]; len(values) > 0 && digits(values[0]) {
			return values[0]
		}
	}
	parts := strings.Split(u.Path, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if digits(parts[i]) {
			return parts[i]
		}
	}
	return ""
}

var sensitiveKeys = map[string]bool{"apikey": true, "auth": true, "authkey": true, "key": true, "passkey": true, "rsskey": true, "token": true, "torrent_pass": true}

func sourceFold(s string) string {
	return strings.ToLower(strings.NewReplacer("ſ", "s", "K", "k", "ß", "ss", "ẞ", "ss").Replace(s))
}
func SafePageURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, e := url.Parse(raw)
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return ""
	}
	parts := []string{}
	for _, piece := range strings.Split(u.RawQuery, "&") {
		if piece == "" {
			continue
		}
		key, value, _ := strings.Cut(piece, "=")
		k := forgivingUnquote(key, true)
		v := forgivingUnquote(value, true)
		if !sensitiveKeys[sourceFold(k)] {
			parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(v))
		}
	}
	u.RawQuery = strings.Join(parts, "&")
	u.ForceQuery = false
	u.Fragment = ""
	return u.String()
}

var titleBoundary = regexp.MustCompile(`(?i)[\s._-]+(?:19\d{2}|20\d{2}|S\d{1,2}(?:E\d{1,3})?)([\s._-]|$)`)
var titleNormalize = regexp.MustCompile(`[._-]+`)
var titleSeason = regexp.MustCompile(`(?i)(?:^|[\s._-])(S\d{1,2})(?:E\d{1,3})?([\s._-]|$)`)
var titleYear = regexp.MustCompile(`(?:^|[\s._-])((?:19|20)\d{2})([\s._-]|$)`)

func SearchKeywords(title string) []string {
	title = strings.TrimSpace(title)
	if title == "" {
		return []string{}
	}
	prefix := title
	if loc := titleBoundary.FindStringIndex(title); loc != nil {
		prefix = title[:loc[0]]
	}
	normalized := strings.TrimSpace(titleNormalize.ReplaceAllString(prefix, " "))
	out := []string{}
	if utf8.RuneCountInString(normalized) >= 2 && sourceFold(normalized) != sourceFold(title) {
		season, year := titleSeason.FindStringSubmatch(title), titleYear.FindStringSubmatch(title)
		if len(season) > 1 {
			out = append(out, normalized+" "+strings.ToUpper(season[1]))
		} else if len(year) > 1 {
			out = append(out, normalized+" "+strings.ToUpper(year[1]))
		}
		out = append(out, normalized)
	}
	return uniqueStrings(append(out, title))
}
func TorrentIsComplete(t Object) bool {
	if t == nil {
		return false
	}
	state := strings.ToLower(strOr(t["state"], ""))
	if strings.HasPrefix(state, "checking") || contains([]string{"error", "missingfiles", "unknown"}, state) {
		return false
	}
	if left, exists := t["amount_left"]; exists && left != nil {
		f, ok := floatValue(left)
		if !ok || f != 0 {
			return false
		}
	}
	if progress, exists := t["progress"]; exists && progress != nil {
		f, ok := floatValue(progress)
		return ok && (f == 1 || f == 100)
	}
	return contains([]string{"uploading", "stalledup", "forcedup", "seeding"}, state)
}
func TargetTorrentStatus(t Object) (string, *string) {
	state := strings.ToLower(strOr(t["state"], ""))
	if strings.HasPrefix(state, "checking") {
		return "TARGET_RECHECKING", ptr("目标种正在校验")
	}
	if contains([]string{"error", "missingfiles", "unknown"}, state) {
		return "TARGET_ERROR", ptr("目标种存在下载器错误或文件缺失，请在 qBittorrent 检查")
	}
	if strings.HasPrefix(state, "paused") || strings.HasPrefix(state, "stopped") {
		return "TARGET_PAUSED", ptr("目标种已暂停，不会自动恢复")
	}
	if TorrentIsComplete(t) {
		if contains([]string{"uploading", "stalledup", "forcedup", "seeding"}, state) {
			return "TARGET_SEEDING", nil
		}
		return "TARGET_COMPLETE", ptr("数据已完整，等待下载器进入做种状态")
	}
	return "TARGET_DOWNLOADING", nil
}
func FeedKey(enclosure, link, title string) string {
	raw := enclosure
	if raw == "" {
		raw = link
	}
	if raw == "" {
		raw = title
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
func truth(v any) bool {
	if v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	case []string:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	case float64:
		return x != 0
	case int:
		return x != 0
	case json.Number:
		f, e := x.Float64()
		return e != nil || f != 0
	}
	return true
}
func pyString(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case string:
		return x
	case bool:
		if x {
			return "True"
		}
		return "False"
	case json.Number:
		return x.String()
	case float64:
		if math.IsInf(x, 1) {
			return "inf"
		}
		if math.IsInf(x, -1) {
			return "-inf"
		}
		if math.IsNaN(x) {
			return "nan"
		}
		return strconv.FormatFloat(x, 'g', -1, 64)
	case int:
		return strconv.Itoa(x)
	}
	b, _ := json.Marshal(v)
	return string(b)
}
func strOr(v any, fallback string) string {
	if !truth(v) {
		return fallback
	}
	return pyString(v)
}
func array(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case []int:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = v
		}
		return out
	case []string:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = v
		}
		return out
	case []Object:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = v
		}
		return out
	}
	return nil
}
func objects(v any) []Object {
	out := []Object{}
	for _, x := range array(v) {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}
func contains(a []string, s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}
func uniqueStrings(a []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, x := range a {
		if !seen[x] {
			out = append(out, x)
			seen[x] = true
		}
	}
	return out
}
func ptr[T any](v T) *T { return &v }
func deref[T any](v *T) any {
	if v == nil {
		return nil
	}
	return *v
}
func clone[T any](v T) T {
	b, _ := json.Marshal(v)
	var out T
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	_ = d.Decode(&out)
	return out
}
func asInt(v any, defaultValue int) int {
	if s, ok := v.(string); ok {
		n, valid := byteInteger([]byte(s))
		if !valid || !n.IsInt64() {
			return defaultValue
		}
		n64 := n.Int64()
		if int64(int(n64)) != n64 {
			return defaultValue
		}
		return int(n64)
	}
	f, ok := floatValue(v)
	if !ok || math.IsInf(f, 0) || math.IsNaN(f) || f >= float64(math.MaxInt) || f < float64(math.MinInt) {
		return defaultValue
	}
	return int(f)
}
func intPtr(v any) *int {
	if v == nil {
		return nil
	}
	f, ok := floatValue(v)
	if !ok {
		return nil
	}
	return ptr(int(f))
}
func floatPtr(v any) *float64 {
	if v == nil {
		return nil
	}
	f, ok := floatValue(v)
	if !ok {
		return nil
	}
	return &f
}

func forgivingUnquote(s string, plus bool) string {
	out := []byte{}
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			b, e := strconv.ParseUint(s[i+1:i+3], 16, 8)
			if e == nil {
				out = append(out, byte(b))
				i += 2
				continue
			}
		}
		if plus && s[i] == '+' {
			out = append(out, ' ')
		} else {
			out = append(out, s[i])
		}
	}
	return strings.ToValidUTF8(string(out), "\ufffd")
}
