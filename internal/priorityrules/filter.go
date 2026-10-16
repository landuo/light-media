// Package priorityrules ports the installed MoviePilot FilterModule. Both its
// Rust 0.2.6 behavior and Python fallback are implemented in Go; neither runtime
// nor the original service is used when filtering resources.
package priorityrules

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/dlclark/regexp2/v2"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"light-media/internal/pyregex"
)

type Mode string

const (
	Rust   Mode = "rust-0.2.6"
	Python Mode = "python-fallback"
)

type sourceError string

func (e sourceError) Error() string { return string(e) }

type Definition map[string]any
type Group struct {
	Name       string  `json:"name"`
	RuleString *string `json:"rule_string"`
	MediaType  *string `json:"media_type"`
	Category   *string `json:"category"`
}
type Configuration struct {
	CustomRules     []Definition `json:"custom_rules"`
	Groups          []Group      `json:"groups"`
	SearchGroups    []string     `json:"search_groups"`
	SubscribeGroups []string     `json:"subscribe_groups"`
	Mode            Mode         `json:"source_mode,omitempty"`
}
type Media struct {
	Type       string         `json:"type"`
	Category   *string        `json:"category"`
	Attributes map[string]any `json:"attributes"`
}
type Torrent struct {
	Title           string              `json:"title"`
	Description     *string             `json:"description"`
	Labels          []string            `json:"labels"`
	Size            int64               `json:"size"`
	Seeders         int                 `json:"seeders"`
	DownloadFactor  float64             `json:"downloadvolumefactor"`
	AgeMinutes      float64             `json:"age"`
	EpisodeCount    int                 `json:"episode_count"`
	Priority        int                 `json:"pri_order"`
	SiteName        string              `json:"site_name"`
	Fields          map[string]any      `json:"fields,omitempty"`
	EpisodeResolver func() (int, error) `json:"-"`
}
type Match struct {
	Index    int `json:"index"`
	Priority int `json:"priority"`
}
type compiledExpression struct {
	node *expression
	err  error
}
type compiledRegex struct {
	re  *regexp2.Regexp
	err error
}
type Engine struct {
	groups      []Group
	rules       map[string]Definition
	mode        Mode
	mu          sync.Mutex
	expressions map[string]compiledExpression
	patterns    map[string]compiledRegex
}

//go:embed builtin.json
var builtinJSON []byte

func Builtins() map[string]Definition {
	var definitions map[string]Definition
	decoder := json.NewDecoder(strings.NewReader(string(builtinJSON)))
	decoder.UseNumber()
	if decoder.Decode(&definitions) != nil {
		panic("invalid embedded priority definitions")
	}
	return definitions
}
func New(config Configuration) (*Engine, error) {
	rules := map[string]Definition{}
	for _, rule := range config.CustomRules {
		name, ok := rule["id"].(string)
		if !ok || name == "" {
			return nil, sourceError("InvalidRuleID")
		}
		rules[name] = rule
	}
	return NewWithDefinitions(config.Groups, rules, config.Mode)
}
func NewWithDefinitions(groups []Group, overrides map[string]Definition, mode Mode) (*Engine, error) {
	if mode == "" {
		mode = Rust
	}
	if mode != Rust && mode != Python {
		return nil, sourceError("InvalidRuleMode")
	}
	if len(groups) > 1000 || len(overrides) > 1000 {
		return nil, sourceError("RuleLimit")
	}
	raw, err := json.Marshal(struct {
		Groups []Group
		Rules  map[string]Definition
	}{groups, overrides})
	if err != nil || len(raw) > 1<<20 {
		return nil, sourceError("RuleLimit")
	}
	// Caller mutations cannot change an in-flight rule snapshot.
	var frozen struct {
		Groups []Group
		Rules  map[string]Definition
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&frozen) != nil {
		return nil, sourceError("InvalidRuleValue")
	}
	set := Builtins()
	for id, rule := range frozen.Rules {
		set[id] = rule
	}
	for _, g := range frozen.Groups {
		if g.RuleString != nil && len(*g.RuleString) > 64<<10 {
			return nil, sourceError("RuleLimit")
		}
	}
	return &Engine{groups: frozen.Groups, rules: set, mode: mode, expressions: map[string]compiledExpression{}, patterns: map[string]compiledRegex{}}, nil
}
func (e *Engine) Filter(ctx context.Context, names []string, torrents []Torrent, media *Media) ([]Match, error) {
	if len(torrents) > 100000 {
		return nil, sourceError("RuleLimit")
	}
	result, err := e.filter(ctx, names, torrents, media, e.mode)
	if err != nil && e.mode == Rust && ctx.Err() == nil {
		return e.filter(ctx, names, torrents, media, Python)
	}
	return result, err
}
func (e *Engine) filter(ctx context.Context, names []string, torrents []Torrent, media *Media, mode Mode) ([]Match, error) {
	result := make([]Match, len(torrents))
	for i, torrent := range torrents {
		result[i] = Match{i, torrent.Priority}
	}
	if len(names) == 0 {
		return result, nil
	}
	for _, group := range e.groups {
		if !slices.Contains(names, group.Name) || !applicable(group, media) || group.RuleString == nil || *group.RuleString == "" {
			continue
		}
		levels := strings.Split(*group.RuleString, ">")
		if mode == Rust {
			trimmed := []string{}
			for _, s := range levels {
				if strings.TrimSpace(s) != "" {
					trimmed = append(trimmed, s)
				}
			}
			levels = trimmed
		}
		filtered := []Match{}
		for _, previous := range result {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			for tier, level := range levels {
				node, err := e.expression(strings.TrimSpace(level))
				if err != nil {
					return nil, err
				}
				ok, err := e.evaluate(ctx, node, torrents[previous.Index], media, mode)
				if err != nil {
					return nil, err
				}
				if ok {
					filtered = append(filtered, Match{previous.Index, 100 - tier})
					break
				}
			}
		}
		result = filtered
		if len(result) == 0 {
			break
		}
	}
	return result, nil
}
func applicable(group Group, media *Media) bool {
	if group.MediaType == nil || *group.MediaType == "" {
		return true
	}
	if media == nil {
		return false
	}
	if (group.Category == nil || *group.Category == "") && *group.MediaType == media.Type {
		return true
	}
	if group.Category == nil || media.Category == nil {
		return group.Category == nil && media.Category == nil
	}
	return *group.Category == *media.Category
}
func (e *Engine) expression(text string) (*expression, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if cached, ok := e.expressions[text]; ok {
		return cached.node, cached.err
	}
	node, err := parseExpression(text)
	if len(e.expressions) < 4096 {
		e.expressions[text] = compiledExpression{node, err}
	}
	return node, err
}
func (e *Engine) evaluate(ctx context.Context, node *expression, t Torrent, media *Media, mode Mode) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if node.kind == "atom" {
		return e.matchRule(ctx, node.atom, t, media, mode)
	}
	left, err := e.evaluate(ctx, node.left, t, media, mode)
	if err != nil {
		return false, err
	}
	if node.kind == "not" {
		return !left, nil
	}
	if node.kind == "and" && !left || node.kind == "or" && left {
		return left, nil
	}
	return e.evaluate(ctx, node.right, t, media, mode)
}
func (e *Engine) search(ctx context.Context, pattern, content string) (bool, error) {
	if len(pattern) > 64<<10 || len(content) > 64<<10 {
		return false, sourceError("RuleLimit")
	}
	e.mu.Lock()
	compiled, ok := e.patterns[pattern]
	if !ok {
		compiled.re, compiled.err = pyregex.Compile(pattern, true)
		if len(e.patterns) < 1024 {
			e.patterns[pattern] = compiled
		}
	}
	e.mu.Unlock()
	if compiled.err != nil {
		return false, sourceError("error")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	matched, err := compiled.re.MatchString(content)
	if err != nil {
		return false, sourceError("RegexTimeout")
	}
	return matched, nil
}
func (e *Engine) matchRule(ctx context.Context, name string, t Torrent, media *Media, mode Mode) (bool, error) {
	rule, exists := e.rules[name]
	if !exists || rule == nil || len(rule) == 0 && mode == Python {
		return false, nil
	}
	if conditions, ok := rule["tmdb"].(map[string]any); ok && len(conditions) > 0 {
		matched, err := matchMedia(conditions, media, mode)
		if err != nil {
			return false, err
		}
		if matched {
			return true, nil
		}
	}
	description := "None"
	if t.Description != nil {
		description = *t.Description
	}
	content := t.Title + " " + description + " " + strings.Join(t.Labels, " ")
	if fields, ok := rule["match"].([]any); ok && len(fields) > 0 {
		parts := []string{}
		for _, value := range fields {
			field, ok := value.(string)
			if !ok {
				return false, sourceError("TypeError")
			}
			var v any
			switch field {
			case "title":
				v = t.Title
			case "description":
				if t.Description != nil {
					v = *t.Description
				}
			case "labels":
				v = t.Labels
			case "site_name":
				v = t.SiteName
			case "size":
				v = t.Size
			case "seeders":
				v = t.Seeders
			default:
				v = t.Fields[field]
			}
			if !truthy(v) {
				continue
			}
			switch x := v.(type) {
			case string:
				parts = append(parts, x)
			case []string:
				parts = append(parts, x...)
			case []any:
				for _, item := range x {
					s, ok := item.(string)
					if !ok {
						return false, sourceError("TypeError")
					}
					parts = append(parts, s)
				}
			default:
				return false, sourceError("TypeError")
			}
		}
		if len(parts) > 0 {
			content = strings.Join(parts, " ")
		}
	}
	includes := rulePatterns(rule["include"], mode)
	if len(includes) > 0 {
		found := false
		for _, p := range includes {
			ok, err := e.search(ctx, pythonString(p), content)
			if err != nil {
				return false, err
			}
			if ok {
				found = true
				break
			}
		}
		if !found {
			return false, nil
		}
	}
	for _, p := range rulePatterns(rule["exclude"], mode) {
		found, err := e.search(ctx, pythonString(p), content)
		if err != nil {
			return false, err
		}
		if found {
			return false, nil
		}
	}
	if value := rule["size_range"]; truthy(value) {
		s, ok := value.(string)
		if !ok {
			return false, sourceError("AttributeError")
		}
		matched, err := matchSize(t, s)
		if err != nil || !matched {
			return false, err
		}
	}
	if value := rule["seeders"]; truthy(value) || mode == Rust && isNumber(value) {
		n, err := strconv.Atoi(strings.TrimSpace(pythonString(value)))
		if err != nil {
			if mode == Python {
				return false, sourceError("ValueError")
			}
		} else if t.Seeders < n {
			return false, nil
		}
	}
	if value, ok := rule["downloadvolumefactor"]; ok && value != nil {
		factor, ok := number(value)
		if !ok || t.DownloadFactor != factor {
			return false, nil
		}
	}
	if value := rule["publish_time"]; truthy(value) {
		text, ok := value.(string)
		if !ok {
			return false, sourceError("AttributeError")
		}
		times := []float64{}
		for _, part := range strings.Split(text, "-") {
			n, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
			if err != nil {
				return false, sourceError("ValueError")
			}
			times = append(times, n)
		}
		if len(times) == 1 {
			if t.AgeMinutes < times[0] {
				return false, nil
			}
		} else if !(times[0] <= t.AgeMinutes && t.AgeMinutes <= times[1]) {
			return false, nil
		}
	}
	return true, nil
}
func matchSize(t Torrent, rule string) (bool, error) {
	rule = strings.TrimSpace(rule)
	kind := ""
	min, max := 0.0, 0.0
	parse := func(s string) (float64, error) {
		n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return 0, sourceError("ValueError")
		}
		return n * 1024 * 1024, nil
	}
	var err error
	if strings.Contains(rule, "-") {
		parts := strings.Split(rule, "-")
		if len(parts) != 2 {
			return false, sourceError("ValueError")
		}
		kind = "between"
		min, err = parse(parts[0])
		if err == nil {
			max, err = parse(parts[1])
		}
	} else if strings.HasPrefix(rule, ">") {
		kind = "gte"
		min, err = parse(rule[1:])
	} else if strings.HasPrefix(rule, "<") {
		kind = "lte"
		max, err = parse(rule[1:])
	}
	if err != nil {
		return false, err
	}
	count := t.EpisodeCount
	if t.EpisodeResolver != nil {
		var err error
		count, err = t.EpisodeResolver()
		if err != nil {
			return false, err
		}
	}
	if count == 0 {
		count = 1
	}
	size := float64(t.Size) / float64(count)
	switch kind {
	case "between":
		return min <= size && size <= max, nil
	case "gte":
		return size >= min, nil
	case "lte":
		return size <= max, nil
	}
	return false, nil
}
func matchMedia(conditions map[string]any, media *Media, mode Mode) (bool, error) {
	if media == nil {
		return false, nil
	}
	tested := false
	for key, raw := range conditions {
		if !truthy(raw) {
			continue
		}
		tested = true
		value, ok := raw.(string)
		if !ok {
			return false, sourceError("AttributeError")
		}
		info, exists := media.Attributes[key]
		if !exists {
			if mode == Python {
				return false, sourceError("AttributeError")
			}
			return false, nil
		}
		if !truthy(info) {
			return false, nil
		}
		values := []any{info}
		if list, ok := info.([]any); ok {
			values = list
		}
		upper := cases.Upper(language.Und)
		actual := map[string]bool{}
		for _, item := range values {
			if key == "production_countries" {
				country, ok := item.(map[string]any)
				if !ok {
					return false, sourceError("AttributeError")
				}
				item = country["iso_3166_1"]
			}
			actual[upper.String(pythonString(item))] = true
		}
		matched := false
		for _, target := range strings.Split(value, ",") {
			if target != "" && actual[upper.String(target)] {
				matched = true
				break
			}
		}
		if !matched {
			return false, nil
		}
	}
	return tested || mode == Python, nil
}
func truthy(v any) bool {
	if v == nil {
		return false
	}
	if n, ok := v.(json.Number); ok {
		f, _ := n.Float64()
		return f != 0
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.String, reflect.Array, reflect.Slice, reflect.Map:
		return r.Len() != 0
	case reflect.Bool:
		return r.Bool()
	case reflect.Float32, reflect.Float64:
		return r.Float() != 0
	case reflect.Int, reflect.Int64, reflect.Int32:
		return r.Int() != 0
	}
	return true
}
func isNumber(v any) bool { _, ok := v.(json.Number); return ok }
func rulePatterns(v any, mode Mode) []any {
	if mode == Rust && isNumber(v) {
		return []any{v}
	}
	return patterns(v)
}
func patterns(v any) []any {
	if !truthy(v) {
		return nil
	}
	if list, ok := v.([]any); ok {
		return list
	}
	return []any{v}
}
func pythonString(v any) string {
	if v == nil {
		return "None"
	}
	if b, ok := v.(bool); ok {
		if b {
			return "True"
		}
		return "False"
	}
	return fmt.Sprint(v)
}
func number(v any) (float64, bool) {
	switch x := v.(type) {
	case json.Number:
		n, err := x.Float64()
		return n, err == nil
	case float64:
		return x, true
	case int:
		return float64(x), true
	}
	return math.NaN(), false
}

// MoviePilot sorts the padded text, including its ordering for negative tiers.
func PriorityKey(n int) string {
	value := strconv.Itoa(n)
	if len(value) < 3 {
		value = strings.Repeat("0", 3-len(value)) + value
	}
	return value
}
