package scrapefileclean

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	py "light-media/internal/releasemeta/legacyanime"
	"math"
	"math/big"
	"path/filepath"
	"strings"
	"time"
)

type Identity struct {
	Dev, Inode uint64
	Added      time.Time
}
type Tracked struct {
	Path     string
	Identity Identity
}
type Task struct {
	Path       string
	Identity   Identity
	At         time.Time
	KnownPaths []string
	Processed  bool
}
type Entry struct {
	Path string
	Dir  bool
}
type Ops interface {
	Exists(string) bool
	IsDir(string) bool
	IsSymlink(string) bool
	Stat(string) (Identity, error)
	Children(string) ([]Entry, error)
	Scan(string) ([]Tracked, error)
	Unlink(string) error
	RemoveTree(string) error
	RemoveDir(string) error
	History(string, string) (bool, error)
	Event(string) error
	Notice(string, string) error
}
type Config struct {
	raw                                                map[string]any
	Enabled, Notify, Scrap, Torrents, History, Delayed bool
	Monitor, Exclude, Keywords, Custom                 string
	Extensions                                         []string
	Delay                                              int
}

func NewConfig() Config { return Config{Delayed: true, Delay: 30} }
func Truth(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		f, _ := x.Float64()
		return f != 0
	case float64:
		return x != 0
	case int:
		return x != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}
func decode(raw json.RawMessage) (any, error) {
	var value any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&value); err != nil {
		return nil, err
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, errors.New("invalid config JSON")
	}
	return value, nil
}
func typename(v any) string {
	switch x := v.(type) {
	case nil:
		return "NoneType"
	case string:
		return "str"
	case bool:
		return "bool"
	case []any:
		return "list"
	case map[string]any:
		return "dict"
	case float64:
		return "float"
	case json.Number:
		if strings.ContainsAny(string(x), ".eE") {
			return "float"
		}
		return "int"
	}
	return "object"
}
func attr(v any, name string) error {
	return errors.New("AttributeError:'" + typename(v) + "' object has no attribute '" + name + "'")
}
func (c Config) text(key, method string) string {
	v := c.raw[key]
	if !Truth(v) {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		panic(effectFailure{attr(v, method)})
	}
	return s
}
func (c *Config) apply(raw json.RawMessage) error {
	v, e := decode(raw)
	if e != nil {
		return e
	}
	if !Truth(v) {
		v = map[string]any{}
	}
	fields, ok := v.(map[string]any)
	if !ok {
		return attr(v, "get")
	}
	c.raw = fields
	c.Enabled = Truth(fields["enabled"])
	c.Notify = Truth(fields["notify"])
	c.Scrap = Truth(fields["delete_scrap_infos"])
	c.Torrents = Truth(fields["delete_torrents"])
	c.History = Truth(fields["delete_history"])
	c.Delayed = true
	if value, ok := fields["delayed_deletion"]; ok {
		c.Delayed = Truth(value)
	}
	// Other truthy non-strings only fail when the source first calls their split.
	c.Monitor, _ = fields["monitor_dirs"].(string)
	c.Exclude, _ = fields["exclude_dirs"].(string)
	c.Keywords, _ = fields["exclude_keywords"].(string)
	c.Custom = c.text("custom_scrap_extensions", "replace")
	c.Extensions = ParseExtensions(c.Custom)
	nextDelay := 30
	if value, ok := fields["delay_seconds"]; ok {
		n, e := delay(value)
		if e == nil {
			nextDelay = n
		} else if strings.HasPrefix(e.Error(), "OverflowError") {
			return e
		}
	}
	c.Delay = nextDelay
	return nil
}
func delay(v any) (int, error) {
	if s, ok := v.(string); ok {
		n, e := py.Integer(s)
		if e != nil {
			return 0, e
		}
		if n.Sign() < 0 || n.Cmp(bigTen) < 0 {
			return 10, nil
		}
		if n.Cmp(bigMax) > 0 {
			return 86400, nil
		}
		return int(n.Int64()), nil
	}
	if b, ok := v.(bool); ok {
		if b {
			return 10, nil
		}
		return 10, nil
	}
	var f float64
	switch x := v.(type) {
	case json.Number:
		if !strings.ContainsAny(string(x), ".eE") {
			return delay(string(x))
		}
		var e error
		f, e = x.Float64()
		if e != nil {
			if math.IsInf(f, 0) {
				return 0, errors.New("OverflowError:cannot convert float infinity to integer")
			}
			return 0, e
		}
	case float64:
		f = x
	default:
		return 0, errors.New("TypeError")
	}
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return 0, errors.New("OverflowError:cannot convert float infinity to integer")
	}
	if f < 10 {
		return 10, nil
	}
	if f > 86400 {
		return 86400, nil
	}
	return int(f), nil
}

var bigTen = big.NewInt(10)
var bigMax = big.NewInt(86400)
var ScrapExtensions = []string{".nfo", ".xml", ".jpg", ".jpeg", ".png", ".webp", ".tbn", ".fanart", ".gif", ".bmp", ".srt", ".ass", ".ssa", ".sub", ".idx", ".vtt", ".sup", ".pgs", ".smi", ".rt", ".sbv", ".csf-bk", ".csf-tmp"}

func ParseExtensions(raw string) []string {
	out := []string{}
	for _, value := range strings.Split(strings.NewReplacer("，", ",", "\n", ",").Replace(raw), ",") {
		value = py.LowerCase(py.Strip(value))
		if value == "" {
			continue
		}
		if !strings.HasPrefix(value, ".") && !strings.HasPrefix(value, "-") {
			value = "." + value
		}
		if !contains(out, value) {
			out = append(out, value)
		}
	}
	return out
}
func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func (c Config) IsScrap(path string) bool {
	for _, suffix := range append(append([]string{}, ScrapExtensions...), c.Extensions...) {
		if strings.HasSuffix(py.LowerCase(base(path)), suffix) {
			return true
		}
	}
	return false
}
func temporary(p string) bool {
	return contains([]string{".!qb", ".part", ".mp", ".tmp", ".temp"}, py.LowerCase(suffix(p)))
}
func keywords(raw, path string) bool {
	for _, v := range strings.Split(raw, "\n") {
		if v != "" && strings.Contains(path, v) {
			return true
		}
	}
	return false
}
func cleanPath(p string) string {
	anchor := ""
	if strings.HasPrefix(p, "//") && !strings.HasPrefix(p, "///") {
		anchor = "//"
	} else if strings.HasPrefix(p, "/") {
		anchor = "/"
	}
	parts := []string{}
	for _, v := range strings.Split(p, "/") {
		if v != "" && v != "." {
			parts = append(parts, v)
		}
	}
	v := anchor + strings.Join(parts, "/")
	if v == "" {
		return "."
	}
	return v
}
func base(p string) string {
	p = cleanPath(p)
	if p == "." || p == "/" || p == "//" {
		return ""
	}
	return p[strings.LastIndex(p, "/")+1:]
}
func parent(p string) string {
	p = cleanPath(p)
	if p == "/" || p == "//" {
		return p
	}
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return "."
	}
	if i == 0 {
		return "/"
	}
	if i == 1 && strings.HasPrefix(p, "//") {
		return "//"
	}
	return p[:i]
}
func suffix(p string) string {
	p = base(p)
	at := strings.LastIndex(p, ".")
	if at < 1 || at == len(p)-1 {
		return ""
	}
	return p[at:]
}
func stem(p string) string { return strings.TrimSuffix(base(p), suffix(p)) }
func normalized(p, home string, resolver ...func(string) (string, error)) string {
	if p == "~" {
		p = home
	} else if strings.HasPrefix(p, "~/") {
		p = home + "/" + p[2:]
	} else if strings.HasPrefix(p, "~") {
		at := strings.IndexByte(p, '/')
		if at < 0 {
			at = len(p)
		}
		if len(resolver) == 0 || resolver[0] == nil {
			panic(effectFailure{errors.New("expanduser_resolver_required")})
		}
		expanded, e := resolver[0](p[1:at])
		checked(e)
		p = expanded + p[at:]
	}
	clean := filepath.Clean(cleanPath(p))
	if strings.HasPrefix(p, "//") && !strings.HasPrefix(p, "///") {
		clean = "/" + clean
	}
	return clean
}
func sameOrChild(path, root, home string, resolver ...func(string) (string, error)) bool {
	if root == "" {
		return false
	}
	a, b := normalized(path, home, resolver...), normalized(root, home, resolver...)
	if strings.HasPrefix(a, "/") != strings.HasPrefix(b, "/") {
		return false
	}
	if b == "." || strings.HasPrefix(b, "//") {
		return false
	}
	rel, err := filepath.Rel(b, a)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}
func (c Config) Excluded(path, home string, resolver ...func(string) (string, error)) bool {
	for _, root := range strings.Split(c.text("exclude_dirs", "split"), "\n") {
		root = py.Strip(root)
		if root != "" && sameOrChild(path, root, home, resolver...) {
			return true
		}
	}
	return false
}
func (c Config) KeywordExcluded(path string) bool {
	return keywords(c.text("exclude_keywords", "split"), path)
}

type effectFailure struct{ err error }

func checked(err error) {
	if err != nil {
		panic(effectFailure{err})
	}
}
func capture(err *error) {
	if v := recover(); v != nil {
		if f, ok := v.(effectFailure); ok {
			*err = f.err
		} else {
			panic(v)
		}
	}
}
func missing(err error) bool { return errors.Is(err, fs.ErrNotExist) }
