package app

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
	"light-media/internal/catalog"
)

// MoviePilot's default category.yaml order is significant: the first matching
// secondary category is also the directory name. A user file replaces this
// policy, so locally customized categories retain their original ordering.
//
//go:embed defaults/category.yaml
var defaultMediaCategoryPolicy string

const maxCategoryPolicyBytes = 64 << 10

type mediaCategoryCondition struct {
	field         string
	include       map[string]bool
	exclude       map[string]bool
	includeRanges []categoryNumberRange
	excludeRanges []categoryNumberRange
	// CategoryHelper.init loads YAML without evaluating value.split. The
	// runtime retains truthy non-string conditions and fails only if matching
	// reaches that condition with a truthy TMDB field.
	deferredSplitError bool
	deferredRangeError bool
	runtimeSemantics   bool
}

type mediaCategoryRule struct {
	name       string
	conditions []mediaCategoryCondition
	// Runtime init stores these truthy shapes without calling keys/items.
	// Keep them distinct: a malformed rule still has a valid listing name.
	deferredSectionError bool
	deferredItemsError   bool
}

type mediaCategoryPolicy map[string][]mediaCategoryRule

func categoryPolicyError(reason string) error {
	return bad("INVALID_CATEGORY_POLICY", "category.yaml 二级分类策略无效："+reason)
}

func loadMediaCategoryPolicy(dataDir string) (mediaCategoryPolicy, error) {
	raw, err := readMediaCategoryPolicy(dataDir)
	if err != nil {
		return nil, err
	}
	return parseMediaCategoryPolicy(raw)
}

func readMediaCategoryPolicy(dataDir string) ([]byte, error) {
	path := filepath.Join(dataDir, "category.yaml")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return []byte(defaultMediaCategoryPolicy), nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxCategoryPolicyBytes {
		return nil, categoryPolicyError("请使用不超过64 KiB的普通配置文件")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, categoryPolicyError("无法读取配置文件")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxCategoryPolicyBytes+1))
	if err != nil || len(raw) > maxCategoryPolicyBytes {
		return nil, categoryPolicyError("配置文件读取失败或超过64 KiB")
	}
	return raw, nil
}

func parseMediaCategoryPolicy(raw []byte) (mediaCategoryPolicy, error) {
	return parseMediaCategoryPolicyConditions(raw, false)
}

// runtimeSemantics is used only by the CategoryHelper runtime after its bounded
// full-node validation. Strict callers retain their preexisting validation
// contract; this mode changes neither safe names nor YAML/resource limits.
func parseMediaCategoryPolicyConditions(raw []byte, runtimeSemantics bool) (mediaCategoryPolicy, error) {
	if len(raw) > maxCategoryPolicyBytes || !utf8.Valid(raw) {
		return nil, categoryPolicyError("配置文件超过64 KiB或不是UTF-8")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	var doc yaml.Node
	if err := decoder.Decode(&doc); err == io.EOF {
		return mediaCategoryPolicy{}, nil
	} else if err != nil || len(doc.Content) != 1 {
		return nil, categoryPolicyError("顶层必须是movie和tv分类映射")
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, categoryPolicyError("仅支持一个YAML文档")
	}
	if runtimeSemantics {
		if err := categoryPluginResolveYAML11(doc.Content[0], raw); err != nil {
			return nil, err
		}
	}
	policy := mediaCategoryPolicy{}
	root, err := categoryYAMLNode(doc.Content[0])
	if err != nil {
		return nil, err
	}
	if categoryYAMLEmpty(root) {
		return policy, nil
	}
	mergeBudget := 8192
	sections, err := categoryYAMLMapping(root, nil, &mergeBudget)
	if err != nil {
		return nil, err
	}
	for _, pair := range sections {
		key, section := pair.key, pair.value
		if key.Value != "movie" && key.Value != "tv" {
			continue // Original ignores other top-level keys, e.g. YAML anchors.
		}
		if categoryYAMLEmpty(section) {
			policy[key.Value] = nil
			continue
		}
		if runtimeSemantics && section.Kind != yaml.MappingNode {
			policy[key.Value] = []mediaCategoryRule{{deferredSectionError: true}}
			continue
		}
		categories, err := categoryYAMLMapping(section, nil, &mergeBudget)
		if err != nil {
			return nil, err
		}
		if len(categories) > 64 {
			return nil, categoryPolicyError("每类最多64个分类")
		}
		rules, names := []mediaCategoryRule{}, map[string]bool{}
		for _, category := range categories {
			name, fields := category.key, category.value
			if name.Kind != yaml.ScalarNode || name.Tag != "!!str" || !safeMediaCategoryName(name.Value) || names[name.Value] {
				return nil, categoryPolicyError("分类目录名无效或重复，不能包含路径分隔符")
			}
			names[name.Value] = true
			rule := mediaCategoryRule{name: name.Value}
			if categoryYAMLEmpty(fields) {
				rules = append(rules, rule)
				continue
			}
			if runtimeSemantics && fields.Kind != yaml.MappingNode {
				rule.deferredItemsError = true
				rules = append(rules, rule)
				continue
			}
			conditions, err := categoryYAMLMapping(fields, nil, &mergeBudget)
			if err != nil {
				return nil, err
			}
			seen := map[string]bool{}
			for _, pair := range conditions {
				field, value := pair.key, pair.value
				if field.Kind != yaml.ScalarNode || field.Tag != "!!str" || seen[field.Value] || !categoryFieldSupported(field.Value) {
					return nil, categoryPolicyError(fmt.Sprintf("第%d行包含无效或重复条件名", field.Line))
				}
				seen[field.Value] = true
				if categoryYAMLEmpty(value) {
					continue
				}
				if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
					if runtimeSemantics {
						rule.conditions = append(rule.conditions, mediaCategoryCondition{field: field.Value, deferredSplitError: true})
						continue
					}
					return nil, categoryPolicyError("非空条件请使用逗号分隔的字符串")
				}
				condition, err := parseMediaCategoryConditionMode(field.Value, value.Value, runtimeSemantics)
				if err != nil {
					return nil, err
				}
				rule.conditions = append(rule.conditions, condition)
			}
			rules = append(rules, rule)
		}
		policy[key.Value] = rules
		if len(policy.extraFields(key.Value)) > 128 {
			return nil, categoryPolicyError("单类TMDB条件字段超过128项")
		}
	}
	return policy, nil
}

// yaml.v3 resolves plain on/off as strings even with a 1.1 directive. The
// archived ruamel loader honors that directive. Normalize only implicit plain
// scalars, never quoted or explicitly tagged strings, and never execute tags.
// The normal full-tree validator still checks cycles, depth and all tags.
func categoryPluginResolveYAML11(root *yaml.Node, raw []byte) error {
	legacy := false
	for _, line := range strings.Split(strings.TrimPrefix(string(raw), "\ufeff"), "\n") {
		if strings.HasPrefix(line, "%YAML") {
			fields := strings.Fields(line)
			legacy = len(fields) >= 2 && fields[0] == "%YAML" && fields[1] == "1.1"
			break
		}
		if trimmed := strings.TrimSpace(line); trimmed != "" && !strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(line, "%") {
			break
		}
	}
	if !legacy {
		return nil
	}
	seen := map[*yaml.Node]bool{}
	pending := []*yaml.Node{root}
	for len(pending) != 0 {
		node := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if node == nil || seen[node] {
			continue
		}
		seen[node] = true
		if len(seen) > 8192 {
			return categoryPolicyError("YAML循环或展开超过限制")
		}
		if node.Kind == yaml.ScalarNode && node.Tag == "!!str" && node.Style == 0 {
			switch node.Value {
			case "y", "Y", "yes", "Yes", "YES", "on", "On", "ON":
				node.Tag, node.Value = "!!bool", "true"
			case "n", "N", "no", "No", "NO", "off", "Off", "OFF":
				node.Tag, node.Value = "!!bool", "false"
			}
		}
		pending = append(pending, node.Content...)
		if node.Kind == yaml.AliasNode {
			pending = append(pending, node.Alias)
		}
	}
	return nil
}

// Python maps compare bool and integer keys by value (True == 1, False == 0).
// Recognize that collision before the runtime's intentionally stricter string
// key validation, so ruamel DuplicateKeyError follows the parser-failure path.
func categoryPluginKeyIdentity(key *yaml.Node) string {
	switch key.Tag {
	case "!!bool":
		if strings.EqualFold(key.Value, "true") {
			return "!!number\x001"
		}
		if strings.EqualFold(key.Value, "false") {
			return "!!number\x000"
		}
	case "!!int":
		value := strings.ReplaceAll(key.Value, "_", "")
		// A decimal spelling is interpreted in base 10, including leading
		// zeroes in ruamel's default YAML 1.2 mode. Explicit bases keep theirs.
		base := 10
		unsigned := strings.TrimLeft(value, "+-")
		if strings.HasPrefix(unsigned, "0x") || strings.HasPrefix(unsigned, "0o") || strings.HasPrefix(unsigned, "0b") {
			base = 0
		}
		if number, ok := new(big.Int).SetString(value, base); ok && (number.Sign() == 0 || number.Cmp(big.NewInt(1)) == 0) {
			return "!!number\x00" + number.String()
		}
	case "!!null":
		return "!!null"
	}
	return key.Tag + "\x00" + key.Value
}

func (p mediaCategoryPolicy) categoryNames(kind string) ([]string, error) {
	names := []string{}
	for _, rule := range p[kind] {
		if rule.deferredSectionError {
			return nil, categoryPolicyError("分类分区不是映射，无法执行keys")
		}
		names = append(names, rule.name)
	}
	return names, nil
}

func safeMediaCategoryName(name string) bool {
	return name != "" && len(name) <= 240 && name == strings.TrimSpace(name) && !strings.HasPrefix(name, ".") && !strings.HasSuffix(name, ".") && !strings.ContainsAny(name, `/\<>:"|?*`) && !strings.ContainsFunc(name, unicode.IsControl)
}

func categoryFieldSupported(field string) bool {
	return field != "" && len(field) <= 128 && !strings.ContainsFunc(field, unicode.IsControl)
}

func (p mediaCategoryPolicy) category(identity catalog.Media) string {
	// Kept for callers with a strictly validated policy (the source-condition
	// unit tests). Production paths use categoryWithError and propagate errors.
	category, _ := p.categoryWithError(identity)
	return category
}

func (p mediaCategoryPolicy) categoryWithError(identity catalog.Media) (string, error) {
	values := map[string][]string{
		"original_language": {strings.ToUpper(identity.OriginalLanguage)},
		"origin_country":    identity.OriginCountry,
	}
	for _, genre := range identity.Genres {
		values["genre_ids"] = append(values["genre_ids"], strconv.FormatInt(genre.ID, 10))
	}
	for _, genre := range identity.GenreIDs {
		values["genre_ids"] = append(values["genre_ids"], strconv.FormatInt(genre, 10))
	}
	for _, country := range identity.ProductionCountries {
		values["production_countries"] = append(values["production_countries"], country.ISO3166_1)
	}
	if identity.Year > 0 {
		values["release_year"] = []string{strconv.Itoa(identity.Year)}
	}
	// Test the original info_value before normalizing its elements. A list
	// containing an empty string (or a country with an empty code) is truthy
	// in Python and must still reach a deferred value.split error.
	present := map[string]bool{
		"original_language":    identity.OriginalLanguage != "",
		"origin_country":       len(identity.OriginCountry) != 0,
		"genre_ids":            len(identity.Genres) != 0 || len(identity.GenreIDs) != 0,
		"production_countries": len(identity.ProductionCountries) != 0,
		"release_year":         identity.Year > 0,
	}
	for _, rule := range p[identity.MediaType] {
		if rule.deferredSectionError || rule.deferredItemsError {
			return "", categoryPolicyError("分类分区或规则不是映射，无法执行items")
		}
		matches := true
		for _, condition := range rule.conditions {
			matched, err := condition.matchesWithError(values[condition.field], identity.CategoryFields[condition.field], present[condition.field])
			if err != nil {
				return "", err
			}
			if !matched {
				matches = false
				// Source continues the remaining conditions even after a
				// mismatch; a later non-string condition can still raise.
			}
		}
		if matches {
			return rule.name, nil
		}
	}
	return "", nil
}

func (a *App) importMediaCategory(ctx context.Context, identity catalog.Media) (catalog.Media, string, error) {
	policy, err := a.currentMediaCategoryPolicy()
	if err != nil || len(policy[identity.MediaType]) == 0 {
		return identity, "", err
	}
	fields := policy.extraFields(identity.MediaType)
	needsRefresh := !identity.CategoryMetadataKnown
	for _, field := range fields {
		if _, known := identity.CategoryFields[field]; !known {
			needsRefresh = true
		}
	}
	if needsRefresh && !providerNativeMedia(identity) {
		fresh, err := a.catalog().DetailForCategory(ctx, identity.MediaType, identity.ID, fields)
		if err != nil {
			return identity, "", err
		}
		if fresh.ID != identity.ID || fresh.MediaType != identity.MediaType || !fresh.CategoryMetadataKnown {
			return identity, "", bad("INVALID_METADATA", "TMDB返回的影视身份与整理对象不符，未创建整理计划")
		}
		identity = fresh
		key := fmt.Sprintf("media:%s:%d", identity.MediaType, identity.ID)
		if err := a.Store.Put(ctx, "cache", key, map[string]any{"at": time.Now().UTC(), "data": identity}); err != nil {
			return identity, "", err
		}
	}
	category, err := policy.categoryWithError(identity)
	return identity, category, err
}
