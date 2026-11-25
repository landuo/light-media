package removelink

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"os/user"

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
	Path      string
	Identity  Identity
	At        time.Time
	Processed bool
}
type Entry struct {
	Path string
	Dir  bool
}
type Item struct {
	Storage   string         `json:"storage"`
	Path      string         `json:"path"`
	Type      string         `json:"type"`
	Name      string         `json:"name"`
	Extension string         `json:"extension"`
	FileID    string         `json:"fileid"`
	Extra     map[string]any `json:"extra,omitempty"`
}
type Ops interface {
	Exists(string) bool
	Stat(string) (Identity, error)
	Children(string) ([]Entry, error)
	Scan(string) ([]Tracked, error)
	Unlink(string) error
	RemoveTree(string) error
	RemoveDir(string) error
	History(string, string) (bool, error)
	Event(string) error
	Notice(string, string) error
	StorageExists(Item) (bool, error)
	StorageList(Item) ([]Item, error)
	StorageDelete(Item) (bool, error)
	MediaExtensions() []string
}

type Config struct {
	raw                                                      map[string]any
	Enabled, Notify, Scrap, Torrents, History, Delayed, STRM any
	Monitor, Exclude, Keywords, Mappings, Custom             string
	Extensions                                               []string
	Delay                                                    int
}

func NewConfig() Config {
	return Config{Enabled: false, Notify: false, Scrap: false, Torrents: false, History: false, Delayed: true, STRM: false, Delay: 30}
}
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
func Decode(raw json.RawMessage) (any, error) {
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if e := d.Decode(&v); e != nil {
		return nil, e
	}
	return v, nil
}

var errDelayOverflow = errors.New("OverflowError:cannot convert float infinity to integer")

func pyInt(v any) (int, error) {
	clip := func(f float64) int {
		if f > 86400 {
			return 86400
		}
		if f < 10 {
			return 10
		}
		return int(f)
	}
	switch x := v.(type) {
	case bool:
		if x {
			return 1, nil
		}
		return 0, nil
	case json.Number:
		if strings.ContainsAny(string(x), ".eE") {
			f, e := x.Float64()
			if math.IsInf(f, 0) {
				return 0, errDelayOverflow
			}
			return clip(f), e
		}
		return pyInt(string(x))
	case float64:
		if math.IsInf(x, 0) {
			return 0, errDelayOverflow
		}
		return clip(x), nil
	case string:
		n, e := sourceInteger(x)
		if e != nil {
			return 0, e
		}
		if !n.IsInt64() {
			if n.Sign() > 0 {
				return 86400, nil
			}
			return 10, nil
		}
		v := n.Int64()
		if v > 86400 {
			return 86400, nil
		}
		if v < 10 {
			return 10, nil
		}
		return int(v), nil
	}
	return 0, errors.New("not an integer")
}
func configString(v any) (string, error) {
	if !Truth(v) {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", errors.New("truthy non-string configuration")
	}
	return s, nil
}
func (c *Config) Apply(raw json.RawMessage) error {
	v, e := Decode(raw)
	if e != nil {
		return e
	}
	if !Truth(v) {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return errors.New("configuration has no get")
	}
	c.raw = m
	c.Enabled = m["enabled"]
	c.Notify = m["notify"]
	c.Scrap = m["delete_scrap_infos"]
	c.Torrents = m["delete_torrents"]
	c.History = m["delete_history"]
	c.Delayed = true
	if x, ok := m["delayed_deletion"]; ok {
		c.Delayed = x
	}
	c.STRM = false
	if x, ok := m["monitor_strm_deletion"]; ok {
		c.STRM = x
	}
	for key, dst := range map[string]*string{"monitor_dirs": &c.Monitor, "exclude_dirs": &c.Exclude, "exclude_keywords": &c.Keywords, "strm_path_mappings": &c.Mappings, "custom_scrap_extensions": &c.Custom} {
		*dst, _ = configString(m[key])
	}
	if _, e := configString(m["custom_scrap_extensions"]); e != nil {
		return errors.New("AttributeError:'" + pyType(m["custom_scrap_extensions"]) + "' object has no attribute 'replace'")
	}
	c.Extensions = ParseExtensions(c.Custom)
	delay := any(json.Number("30"))
	if x, ok := m["delay_seconds"]; ok {
		delay = x
	}
	nextDelay, e := pyInt(delay)
	if errors.Is(e, errDelayOverflow) {
		return e
	}
	if e != nil {
		nextDelay = 30
	}
	c.Delay = max(10, min(86400, nextDelay))
	return nil
}

var ScrapExtensions = []string{".nfo", ".xml", ".jpg", ".jpeg", ".png", ".webp", ".tbn", ".fanart", ".gif", ".bmp", ".srt", ".ass", ".ssa", ".sub", ".idx", ".vtt", ".sup", ".pgs", ".smi", ".rt", ".sbv", ".csf-bk", ".csf-tmp"}

func ParseExtensions(s string) []string {
	out := []string{}
	for _, x := range strings.Split(strings.NewReplacer("，", ",", "\n", ",").Replace(s), ",") {
		x = sourceLower(sourceStrip(x))
		if x == "" {
			continue
		}
		if !strings.HasPrefix(x, ".") && !strings.HasPrefix(x, "-") {
			x = "." + x
		}
		found := false
		for _, v := range out {
			if v == x {
				found = true
			}
		}
		if !found {
			out = append(out, x)
		}
	}
	return out
}
func Trickplay(p string) bool {
	for _, x := range strings.Split(filepath.ToSlash(p), "/") {
		if strings.HasSuffix(sourceLower(x), ".trickplay") {
			return true
		}
	}
	return false
}
func (c Config) IsScrap(p string) bool {
	name := sourceLower(base(p))
	for _, ext := range append(append([]string{}, ScrapExtensions...), c.Extensions...) {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}
func normalized(p, home string) string {
	// Path parses away '.' and duplicate separators before expanduser, while
	// preserving '..' until the final os.path.normpath step.
	p = cleanPath(p)
	if strings.HasPrefix(p, "~") {
		name, rest, _ := strings.Cut(p[1:], "/")
		expanded := home
		if name != "" {
			account, err := user.Lookup(name)
			if err != nil {
				checked(sourceHomeError{})
			}
			expanded = account.HomeDir
		}
		if strings.HasPrefix(expanded, "~") {
			checked(sourceHomeError{})
		}
		expanded = strings.TrimRight(expanded, "/")
		if expanded == "" {
			expanded = "/"
		}
		p = expanded
		if rest != "" {
			if !strings.HasSuffix(p, "/") {
				p += "/"
			}
			p += rest
		}
	}
	return normPath(p)
}
func (c Config) Excluded(p, home string) bool {
	c.ensureString("exclude_dirs")
	for _, base := range strings.Split(c.Exclude, "\n") {
		base = sourceStrip(base)
		if base == "" {
			continue
		}
		candidate := normalized(p, home)
		base = normalized(base, home)
		if strings.HasPrefix(base, "//") {
			continue
		}
		rel, e := filepath.Rel(base, candidate)
		if e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
func keywords(s, p string) bool {
	for _, k := range strings.Split(s, "\n") {
		if k != "" && strings.Contains(p, k) {
			return true
		}
	}
	return false
}
func suffix(p string) string {
	name := base(p)
	i := strings.LastIndex(name, ".")
	if i < 1 || i == len(name)-1 {
		return ""
	}
	return name[i:]
}
func stem(p string) string   { return strings.TrimSuffix(base(p), suffix(p)) }
func missing(err error) bool { return errors.Is(err, fs.ErrNotExist) }

// Expected adapter failures follow source exception boundaries, never success.
// Public event entrypoints recover them; programming panics remain visible.
type effectFailure struct{ err error }

// Path.expanduser raises this RuntimeError before commonpath/unlink catches.
// Keep the class for an uncaught entry, and str(e) for source caught logging.
type sourceHomeError struct{}

func (sourceHomeError) Error() string { return "RuntimeError:Could not determine home directory." }
func sourceCaughtErrorText(err error) string {
	var home sourceHomeError
	if errors.As(err, &home) {
		return "Could not determine home directory."
	}
	return err.Error()
}

func (e effectFailure) Error() string { return e.err.Error() }
func checked(err error) {
	if err != nil {
		panic(effectFailure{err})
	}
}
func capture(err *error) {
	if x := recover(); x != nil {
		if f, ok := x.(effectFailure); ok {
			*err = f.err
		} else {
			panic(x)
		}
	}
}

// A source configuration attribute error is a deterministic local handler
// exception. It must remain distinguishable from an opaque host/guard error
// when applying watchdog's per-observer failure boundary.
type sourceAttributeError struct{ class, attribute string }

func (e sourceAttributeError) Error() string {
	return "AttributeError:'" + e.class + "' object has no attribute '" + e.attribute + "'"
}

func pyType(v any) string {
	switch v.(type) {
	case bool:
		return "bool"
	case []any:
		return "list"
	case map[string]any:
		return "dict"
	case json.Number:
		if strings.ContainsAny(string(v.(json.Number)), ".eE") {
			return "float"
		}
		return "int"
	}
	return "object"
}
func (c Config) ensureString(key string) string {
	v, ok := c.raw[key]
	if !ok {
		return ""
	}
	s, e := configString(v)
	if e != nil {
		panic(effectFailure{sourceAttributeError{class: pyType(v), attribute: "split"}})
	}
	return s
}
func normPath(p string) string {
	out := filepath.Clean(p)
	if strings.HasPrefix(p, "//") && !strings.HasPrefix(p, "///") {
		out = "/" + out
	}
	return out
}

// pathlib.Path collapses redundant separators and '.' but preserves '..'.
func cleanPath(p string) string {
	anchor := ""
	if strings.HasPrefix(p, "//") && !strings.HasPrefix(p, "///") {
		anchor = "//"
	} else if strings.HasPrefix(p, "/") {
		anchor = "/"
	}
	parts := []string{}
	for _, x := range strings.Split(p, "/") {
		if x != "" && x != "." {
			parts = append(parts, x)
		}
	}
	out := anchor + strings.Join(parts, "/")
	if out == "" {
		return "."
	}
	return out
}
func base(p string) string {
	p = cleanPath(p)
	if p == "." || p == "/" || p == "//" {
		return ""
	}
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
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

// SourceNoticeError is a proven, terminal exception from the synchronous source
// notification call. Adapters must not construct it for transport, persistence,
// cancellation, or unclassified Router failures.
type SourceNoticeError struct {
	Class string
}

func (e *SourceNoticeError) Valid() bool {
	return e != nil && (e.Class == "TypeError" || e.Class == "AttributeError" || e.Class == "ValueError" || e.Class == "RuntimeError")
}
func (e *SourceNoticeError) Error() string {
	return e.Class
}
func knownSourceNotice(err error) bool {
	var source *SourceNoticeError
	return errors.As(err, &source) && source.Valid()
}
