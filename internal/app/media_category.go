package app

import (
	"bytes"
	"context"
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
const defaultMediaCategoryPolicy = `movie:
  动画电影:
    genre_ids: '16'
  华语电影:
    original_language: 'zh,cn,bo,za'
  外语电影:
tv:
  国漫:
    genre_ids: '16'
    origin_country: 'CN,TW,HK'
  日番:
    genre_ids: '16'
    origin_country: 'JP'
  纪录片:
    genre_ids: '99'
  儿童:
    genre_ids: '10762'
  综艺:
    genre_ids: '10764,10767'
  国产剧:
    origin_country: 'CN,TW,HK'
  欧美剧:
    origin_country: 'US,FR,GB,DE,ES,IT,NL,PT,RU,UK'
  日韩剧:
    origin_country: 'JP,KP,KR,TH,IN,SG'
  未分类:
`

const maxCategoryPolicyBytes = 64 << 10

type mediaCategoryCondition struct {
	field   string
	include map[string]bool
	exclude map[string]bool
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
	path := filepath.Join(dataDir, "category.yaml")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return parseMediaCategoryPolicy([]byte(defaultMediaCategoryPolicy))
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
	return parseMediaCategoryPolicy(raw)
}

func parseMediaCategoryPolicy(raw []byte) (mediaCategoryPolicy, error) {
	if len(raw) > maxCategoryPolicyBytes || !utf8.Valid(raw) {
		return nil, categoryPolicyError("配置文件超过64 KiB或不是UTF-8")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	var doc yaml.Node
	if err := decoder.Decode(&doc); err != nil || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, categoryPolicyError("顶层必须是movie和tv分类映射")
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, categoryPolicyError("仅支持一个YAML文档")
	}
	policy := mediaCategoryPolicy{}
	root := doc.Content[0]
	if root.Anchor != "" {
		return nil, categoryPolicyError("不支持锚点或别名")
	}
	for i := 0; i < len(root.Content); i += 2 {
		key, section := root.Content[i], root.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || (key.Value != "movie" && key.Value != "tv") || section.Kind != yaml.MappingNode || key.Anchor != "" || section.Anchor != "" {
			return nil, categoryPolicyError("仅支持movie和tv分类映射，不支持锚点或别名")
		}
		if _, exists := policy[key.Value]; exists {
			return nil, categoryPolicyError("movie或tv分区重复")
		}
		if len(section.Content) > 128 {
			return nil, categoryPolicyError("每类最多64个分类")
		}
		rules, names := []mediaCategoryRule{}, map[string]bool{}
		for n := 0; n < len(section.Content); n += 2 {
			name, fields := section.Content[n], section.Content[n+1]
			if name.Kind != yaml.ScalarNode || name.Tag != "!!str" || name.Anchor != "" || !safeMediaCategoryName(name.Value) || names[name.Value] {
				return nil, categoryPolicyError("分类目录名无效或重复，不能包含路径分隔符")
			}
			names[name.Value] = true
			rule := mediaCategoryRule{name: name.Value}
			if fields.Kind == yaml.ScalarNode && fields.Tag == "!!null" && fields.Anchor == "" {
				rules = append(rules, rule)
				continue
			}
			if fields.Kind != yaml.MappingNode || fields.Anchor != "" {
				return nil, categoryPolicyError("分类条件必须是映射或空兜底项")
			}
			seen := map[string]bool{}
			for j := 0; j < len(fields.Content); j += 2 {
				field, value := fields.Content[j], fields.Content[j+1]
				if field.Kind != yaml.ScalarNode || field.Tag != "!!str" || field.Anchor != "" || seen[field.Value] || !categoryFieldSupported(field.Value) {
					return nil, categoryPolicyError(fmt.Sprintf("第%d行包含未知或重复条件，仅支持genre_ids、original_language、origin_country、production_countries、release_year", field.Line))
				}
				seen[field.Value] = true
				if value.Kind != yaml.ScalarNode || value.Anchor != "" || (value.Tag != "!!str" && value.Tag != "!!int" && value.Tag != "!!null") {
					return nil, categoryPolicyError("条件请使用逗号分隔的字符串，不支持锚点或别名")
				}
				if value.Tag == "!!null" || strings.TrimSpace(value.Value) == "" {
					continue
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
	}
	return policy, nil
}

func safeMediaCategoryName(name string) bool {
	return name != "" && len(name) <= 240 && name == strings.TrimSpace(name) && !strings.HasPrefix(name, ".") && !strings.HasSuffix(name, ".") && !strings.ContainsAny(name, `/\<>:"|?*`) && !strings.ContainsFunc(name, unicode.IsControl)
}

func categoryFieldSupported(field string) bool {
	switch field {
	case "genre_ids", "original_language", "origin_country", "production_countries", "release_year":
		return true
	}
	return false
}

func parseMediaCategoryCondition(field, expression string) (mediaCategoryCondition, error) {
	condition := mediaCategoryCondition{field: field, include: map[string]bool{}, exclude: map[string]bool{}}
	for _, raw := range strings.Split(expression, ",") {
		token := strings.TrimSpace(raw)
		if token == "" {
			continue
		}
		destination := condition.include
		if strings.HasPrefix(token, "!") {
			token = strings.TrimSpace(strings.TrimPrefix(token, "!"))
			destination = condition.exclude
		}
		if field == "genre_ids" || field == "release_year" {
			parts := strings.Split(token, "-")
			first, err := strconv.Atoi(parts[0])
			last := first
			if len(parts) == 2 {
				last, err = strconv.Atoi(parts[1])
			}
			if err != nil || len(parts) > 2 || first < 1 || last < first || last > 1<<31-1 || last-first >= 1000 {
				return condition, categoryPolicyError("类型与年份条件须为正整数或不超过1000项的数字范围")
			}
			for number := first; number <= last; number++ {
				destination[strconv.Itoa(number)] = true
			}
		} else {
			if len(token) != 2 || !asciiCategoryCode(token) {
				return condition, categoryPolicyError("语种和国家须为两字母代码")
			}
			destination[strings.ToUpper(token)] = true
		}
		if len(condition.include)+len(condition.exclude) > 2000 {
			return condition, categoryPolicyError("单个分类条件超过2000项")
		}
	}
	if len(condition.include)+len(condition.exclude) == 0 {
		return condition, categoryPolicyError("分类条件不能只有空值或分隔符")
	}
	return condition, nil
}

func asciiCategoryCode(value string) bool {
	for _, char := range value {
		if char < 'a' || char > 'z' {
			if char < 'A' || char > 'Z' {
				return false
			}
		}
	}
	return true
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
			present, included, excluded := false, len(condition.include) == 0, false
			for _, value := range values[condition.field] {
				value = strings.ToUpper(strings.TrimSpace(value))
				if value == "" {
					continue
				}
				present = true
				included = included || condition.include[value]
				excluded = excluded || condition.exclude[value]
			}
			if !present || !included || excluded {
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
	policy, err := loadMediaCategoryPolicy(a.Dir)
	if err != nil || len(policy[identity.MediaType]) == 0 {
		return identity, "", err
	}
	if !identity.CategoryMetadataKnown {
		fresh, err := a.catalog().Detail(ctx, identity.MediaType, identity.ID)
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
