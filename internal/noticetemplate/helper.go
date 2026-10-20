package noticetemplate

import (
	"container/list"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"reflect"
	"strings"
	"sync"
	"time"

	"light-media/internal/jinja/exec"
)

func asDict(v any) (*exec.Dict, bool) {
	if d, ok := v.(*exec.Dict); ok {
		return d, true
	}
	if d, ok := v.(exec.Dict); ok {
		return &d, true
	}
	return nil, false
}
func ParseTemplateContent(input any, kind string) (*string, error) {
	var value string
	var err error
	dump := func(v any) string { s, e := exec.PythonDumps(v, exec.JSONOptions{}); err = e; return s }
	switch kind {
	case "string":
		value = exec.AsValue(input).String()
	case "dict":
		value = dump(input)
	case "literal":
		v := input
		if s, ok := input.(string); ok {
			v, err = parseLiteral(s)
		}
		if err == nil {
			if _, ok := asDict(v); !ok {
				return nil, nil
			}
			value = dump(v)
		}
	case "":
		if _, ok := asDict(input); ok {
			value = dump(input)
		} else if s, ok := input.(string); ok {
			if _, jsonErr := DecodeValueJSON([]byte(s)); jsonErr == nil {
				value = s
			} else {
				v, e := parseLiteral(s)
				if errors.Is(e, exec.ErrUnsupported) {
					return nil, e
				}
				var se SourceError
				if errors.As(e, &se) && se.Class == "TypeError" {
					return nil, nil
				}
				if _, ok := asDict(v); e == nil && ok {
					value = dump(v)
				} else {
					value = s
				}
			}
		} else {
			return nil, nil
		}
	default:
		return nil, nil
	}
	if err != nil {
		if errors.Is(err, exec.ErrUnsupported) {
			return nil, err
		}
		return nil, nil
	}
	return &value, nil
}

type cachedContext struct {
	key     string
	value   Object
	expires time.Time
}

// ContextCache is the source notification region's memory TLRU behavior:
// 100 entries,600 seconds; accesses affect LRU but do not refresh expiry.
type ContextCache struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[string]*list.Element
	order   *list.List
}

func NewContextCache(now func() time.Time) *ContextCache {
	if now == nil {
		now = time.Now
	}
	return &ContextCache{now: now, entries: map[string]*list.Element{}, order: list.New()}
}
func (c *ContextCache) expire() {
	now := c.now()
	for k, el := range c.entries {
		if !now.Before(el.Value.(cachedContext).expires) {
			delete(c.entries, k)
			c.order.Remove(el)
		}
	}
}
func (c *ContextCache) Get(key string) Object {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expire()
	if el := c.entries[key]; el != nil {
		c.order.MoveToBack(el)
		return el.Value.(cachedContext).value
	}
	return nil
}
func (c *ContextCache) Set(key string, value Object) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expire()
	if el := c.entries[key]; el != nil {
		el.Value = cachedContext{key, value, c.now().Add(600 * time.Second)}
		c.order.MoveToBack(el)
		return
	}
	for len(c.entries) >= 100 {
		el := c.order.Front()
		delete(c.entries, el.Value.(cachedContext).key)
		c.order.Remove(el)
	}
	c.entries[key] = c.order.PushBack(cachedContext{key, value, c.now().Add(600 * time.Second)})
}

// Close is a no-op for the source memory backend; it does not clear values.
func (c *ContextCache) Close() {}

type Helper struct{ Cache *ContextCache }

func NewHelper(cache *ContextCache) *Helper {
	if cache == nil {
		cache = NewContextCache(nil)
	}
	return &Helper{Cache: cache}
}
func CacheKey(value any) (string, error) {
	var text string
	if d, ok := asDict(value); ok {
		get := func(key string) any {
			for _, p := range d.Pairs {
				if p.Key.IsString() && p.Key.String() == key {
					return p.Value.Interface()
				}
			}
			return ""
		}
		v, err := sourceAdd(get("title"), get("text"))
		if err != nil {
			return "", err
		}
		text, err = exec.PythonDumps(v, exec.JSONOptions{SortKeys: true})
		if err != nil {
			return "", err
		}
	} else {
		if !templateTruth(value) {
			return "", nil
		}
		text = exec.AsValue(value).String()
	}
	sum := md5.Sum([]byte(text))
	return hex.EncodeToString(sum[:]), nil
}
func (h *Helper) GetCacheContext(content any) (Object, error) {
	key, err := CacheKey(content)
	if err != nil {
		return nil, err
	}
	return h.Cache.Get(key), nil
}
func (h *Helper) SetCacheContext(content any, context Object) error {
	key, err := CacheKey(content)
	if err != nil {
		return err
	}
	h.Cache.Set(key, context)
	return nil
}
func (h *Helper) Close() { h.Cache.Close() }
func (h *Helper) Render(ctx context.Context, input any, kind string, business ContextInput) (any, error) {
	value, _, err := h.RenderWithTrace(ctx, input, kind, business)
	return value, err
}

// SourceCache records this call's actual successful cache write. It is evidence
// of that write, not a command to repopulate the original memory cache on replay.
type SourceCache struct {
	Key     string
	Context Object
}

func (h *Helper) RenderWithTrace(ctx context.Context, input any, kind string, business ContextInput) (any, *SourceCache, error) {
	parsed, err := ParseTemplateContent(input, kind)
	if err != nil {
		return nil, nil, err
	}
	if parsed == nil || *parsed == "" {
		return nil, nil, SourceError{"ValueError"}
	}
	variables, err := BuildContext(business)
	if err != nil {
		return nil, nil, SourceError{"ValueError"}
	}
	if len(variables) == 0 {
		return nil, nil, SourceError{"ValueError"}
	}
	rendered, err := RenderWithContext(ctx, *parsed, variables)
	if err != nil {
		if errors.Is(err, exec.ErrUnsupported) || ctx.Err() != nil {
			return nil, nil, err
		}
		return nil, nil, SourceError{"ValueError"}
	}
	if rendered == "" {
		return nil, nil, SourceError{"ValueError"}
	}
	var value any = rendered
	if kind != "string" {
		value, err = processFormatted(rendered)
		if err != nil {
			return nil, nil, err
		}
	}
	if !templateTruth(value) {
		return nil, nil, nil
	}
	key, err := CacheKey(value)
	if err != nil {
		if errors.Is(err, exec.ErrUnsupported) {
			return nil, nil, err
		}
		return nil, nil, SourceError{"ValueError"}
	}
	h.Cache.Set(key, variables)
	return value, &SourceCache{Key: key, Context: variables}, nil
}

func templateTruth(value any) bool {
	if _, ok := value.(json.Number); ok {
		return truth(value)
	}
	if n, ok := value.(*big.Int); ok {
		return n.Sign() != 0
	}
	return exec.AsValue(value).IsTrue()
}
func processFormatted(rendered string) (any, error) {
	processed := rendered
	for _, p := range [][2]string{{"\n", `\n`}, {"\r", `\r`}, {"\t", `\t`}, {"\b", `\b`}, {"\f", `\f`}} {
		processed = strings.ReplaceAll(processed, p[0], p[1])
	}
	value, err := DecodeValueJSON([]byte(processed))
	if err != nil {
		return rendered, nil
	}
	var restore func(any) any
	restore = func(v any) any {
		switch x := v.(type) {
		case string:
			for _, p := range [][2]string{{`\n`, "\n"}, {`\r`, "\r"}, {`\t`, "\t"}, {`\b`, "\b"}, {`\f`, "\f"}} {
				x = strings.ReplaceAll(x, p[0], p[1])
			}
			return x
		case *exec.Dict:
			for _, p := range x.Pairs {
				p.Value = exec.AsValue(restore(p.Value.Interface()))
			}
			return x
		case []any:
			for i, v := range x {
				x[i] = restore(v)
			}
			return x
		}
		return v
	}
	return restore(value), nil
}
func sourceAdd(a, b any) (any, error) {
	if x, ok := a.(string); ok {
		if y, ok := b.(string); ok {
			return x + y, nil
		}
		return nil, SourceError{"TypeError"}
	}
	if x, ok := a.([]any); ok {
		if y, ok := b.([]any); ok {
			return append(append([]any{}, x...), y...), nil
		}
		return nil, SourceError{"TypeError"}
	}
	if x, ok := a.(exec.Tuple); ok {
		if y, ok := b.(exec.Tuple); ok {
			return append(append(exec.Tuple{}, x...), y...), nil
		}
		return nil, SourceError{"TypeError"}
	}
	integer := func(v any) (*big.Int, bool) {
		if n, ok := v.(*big.Int); ok {
			return new(big.Int).Set(n), true
		}
		if n, ok := v.(bool); ok {
			if n {
				return big.NewInt(1), true
			}
			return new(big.Int), true
		}
		if v == nil {
			return nil, false
		}
		r := reflect.ValueOf(v)
		switch r.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return big.NewInt(r.Int()), true
		}
		return nil, false
	}
	x, xok := integer(a)
	y, yok := integer(b)
	if xok && yok {
		return new(big.Int).Add(x, y), nil
	}
	toFloat := func(v any) (float64, bool) {
		if n, ok := v.(float64); ok {
			return n, true
		}
		if n, ok := integer(v); ok {
			f, _ := new(big.Float).SetInt(n).Float64()
			return f, true
		}
		return 0, false
	}
	fa, aok := toFloat(a)
	fb, bok := toFloat(b)
	if aok && bok {
		return fa + fb, nil
	}
	return nil, SourceError{"TypeError"}
}
