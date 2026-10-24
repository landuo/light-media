package app

// Pure serialization, metadata and authenticated-storage tools shared by ordinary core code
// and the standard plugin manager. No plugin lifecycle, queue, receipt or recovery admission.
import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"light-media/internal/plugins"
	"light-media/internal/releasemeta"
	"math/big"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

func mustCleanSeedJSON(v any) json.RawMessage { b, _ := cleanupJournalJSON(v); return b }

func identifiersTimezone() string {
	if zone := os.Getenv("TZ"); zone != "" && zone != "Local" {
		if _, err := time.LoadLocation(zone); err == nil {
			return zone
		}
	}
	return "Asia/Shanghai"
}

func larkMetaNumbers(meta *releasemeta.MetaObject) ([]int, []int, error) {
	seasons, e := meta.SeasonList()
	if e != nil {
		return nil, nil, e
	}
	s, e := larkNativeNumbers(seasons)
	if e != nil {
		return nil, nil, e
	}
	episodes, e := meta.EpisodeList()
	if e != nil {
		return nil, nil, e
	}
	eps, e := larkNativeNumbers(episodes)
	return s, eps, e
}

func larkSameRaw(a, b []byte) bool { return bytes.Equal(a, b) }

func decodeSourceJSON(raw []byte) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if e := decoder.Decode(&value); e != nil {
		return nil, e
	}
	return value, nil
}

func sourceSubscriptionLink(domain, kind string) any {
	if domain == "" {
		return nil
	}
	if !strings.HasSuffix(domain, "/") {
		domain += "/"
	}
	if !strings.HasPrefix(domain, "http://") && !strings.HasPrefix(domain, "https://") {
		domain = "http://" + domain
	}
	base, e := url.Parse(domain)
	if e != nil {
		return nil
	}
	path := "movie"
	if kind == "tv" {
		path = "tv"
	}
	return base.ResolveReference(&url.URL{Fragment: "/subscribe/" + path + "?tab=mysub"}).String()
}

func sourceSubscriptionNoteClone(note []int) []int {
	if note == nil {
		return nil
	}
	return append([]int{}, note...)
}

func sourceSubscriptionTruth(value any) bool {
	switch value := value.(type) {
	case nil:
		return false
	case bool:
		return value
	case string:
		return value != ""
	case json.Number:
		n, ok := new(big.Rat).SetString(string(value))
		return !ok || n.Sign() != 0
	case int:
		return value != 0
	case int64:
		return value != 0
	case float64:
		return value != 0
	case []any:
		return len(value) != 0
	case []string:
		return len(value) != 0
	case map[string]any:
		return len(value) != 0
	default:
		return true
	}
}

func (p *PluginStore) noticeID(kind string, v ...any) string {
	b := mustCleanSeedJSON(v)
	m := hmac.New(sha256.New, p.store.key)
	m.Write([]byte("plugin-notice/v1/" + kind + "\x00"))
	m.Write(b)
	return hex.EncodeToString(m.Sum(nil))
}

func sourceMap(value any) map[string]any { out, _ := value.(map[string]any); return out }

type PluginProofReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func cleanupJournalJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err = d.Decode(&v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

func cleanupJournalStrict(raw []byte, out any) error {
	if !utf8.Valid(raw) {
		return cleanupJournalCorrupt()
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && reflect.ValueOf(out).Elem().Kind() == reflect.Struct {
		return cleanupJournalCorrupt()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	d.DisallowUnknownFields()
	if d.Decode(out) != nil || d.Decode(new(any)) != io.EOF {
		return cleanupJournalCorrupt()
	}
	return nil
}

type pluginRemoveLinkJournalReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func pluginErrorCodeInternal(err error) string {
	var api *APIError
	if errors.As(err, &api) {
		return api.Code
	}
	return ""
}

const pluginDataValueLimit = plugins.MaxPayloadBytes

func pluginJSONLimit(value any, object bool, limit int) (json.RawMessage, error) {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > limit {
		return nil, bad("INVALID_PLUGIN_VALUE", "插件数据不是有效JSON或超过大小上限")
	}
	if object {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			return nil, bad("INVALID_PLUGIN_CONFIG", "插件配置或清单必须是JSON对象")
		}
	}
	return raw, nil
}

var pluginStorageDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

var pluginStorageID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,95}$`)

const pluginValueLimit = 1 << 20

func (p *PluginStore) seal(kind, instanceID, key string, raw []byte) ([]byte, error) {
	block, err := aes.NewCipher(p.store.key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	aad, _ := json.Marshal([]string{"light-media/plugins/v1", kind, instanceID, key})
	return aead.Seal(nonce, nonce, raw, aad), nil
}

func cleanupJournalCorrupt() error {
	return bad("CLEANUP_JOURNAL_CORRUPT", "清理状态无法通过格式或完整性校验")
}

func larkNativeNumbers(values []*big.Int) ([]int, error) {
	out := []int{}
	for _, value := range values {
		if value == nil || !value.IsInt64() || value.Sign() < 0 || value.Int64() > int64(^uint(0)>>1) {
			return nil, bad("INVALID_SOURCE_METADATA", "原资源季集超出本机整数范围")
		}
		out = append(out, int(value.Int64()))
	}
	return out, nil
}

func subscriptionSiteControls(defaults, selected []string) []string {
	if len(selected) == 0 {
		return slices.Clone(defaults)
	}
	if len(defaults) == 0 {
		return slices.Clone(selected)
	}
	intersection := []string{}
	for _, id := range selected {
		if slices.Contains(defaults, id) {
			intersection = append(intersection, id)
		}
	}
	if len(intersection) > 0 {
		return intersection
	}
	return slices.Clone(defaults)
}

// Preserved limit for existing encrypted ordinary notice history.
const pluginCleanInvalidSeedJournalLimit = 32 << 20
