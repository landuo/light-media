package app

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
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
}

type mediaCategoryRule struct {
	name       string
	conditions []mediaCategoryCondition
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
					return nil, categoryPolicyError("非空条件请使用逗号分隔的字符串")
				}
				condition, err := parseMediaCategoryCondition(field.Value, value.Value)
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

func safeMediaCategoryName(name string) bool {
	return name != "" && len(name) <= 240 && name == strings.TrimSpace(name) && !strings.HasPrefix(name, ".") && !strings.HasSuffix(name, ".") && !strings.ContainsAny(name, `/\<>:"|?*`) && !strings.ContainsFunc(name, unicode.IsControl)
}

func categoryFieldSupported(field string) bool {
	return field != "" && len(field) <= 128 && !strings.ContainsFunc(field, unicode.IsControl)
}

func (p mediaCategoryPolicy) category(identity catalog.Media) string {
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
	for _, rule := range p[identity.MediaType] {
		matches := true
		for _, condition := range rule.conditions {
			if !condition.matches(values[condition.field], identity.CategoryFields[condition.field]) {
				matches = false
				break
			}
		}
		if matches {
			return rule.name
		}
	}
	return ""
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
	if needsRefresh {
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
	return identity, policy.category(identity), nil
}
