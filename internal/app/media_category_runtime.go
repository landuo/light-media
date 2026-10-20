package app

import (
	"bytes"
	"crypto/sha256"
	"io"
	"sync"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// CategoryHelper.init replaces its movie/tv maps only for a truthy parsed root.
// Empty input and ordinary parser failures retain the old process-local maps;
// a cold process starts with empty maps. This cache is never persisted as rules.
type mediaCategoryRuntime struct {
	mu          sync.Mutex
	initialized bool
	digest      [32]byte
	policy      mediaCategoryPolicy
}

type categoryReloadKind uint8

const (
	categoryKeepEmpty categoryReloadKind = iota
	categoryKeepParseFailure
	categoryReplaceMaps
)

func categoryPluginReload(raw []byte) (mediaCategoryPolicy, categoryReloadKind, error) {
	if len(raw) > maxCategoryPolicyBytes || !utf8.Valid(raw) {
		return nil, categoryKeepEmpty, categoryPolicyError("配置文件超过64 KiB或不是UTF-8")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	var document yaml.Node
	if err := decoder.Decode(&document); err == io.EOF {
		return nil, categoryKeepEmpty, nil
	} else if err != nil || len(document.Content) != 1 {
		return nil, categoryKeepParseFailure, nil // ruamel parse failure: keep the previous maps.
	}
	var extra yaml.Node
	if decoder.Decode(&extra) != io.EOF {
		return nil, categoryKeepParseFailure, nil // load(), unlike load_all(), rejects extra documents.
	}
	// Nodes never construct or execute tags. Unsupported native shapes, custom
	// tags, cycles and resource expansion still fail explicitly before a write.
	budget := 8192
	duplicate, err := categoryPluginNodes(document.Content[0], nil, &budget)
	if err != nil {
		return nil, categoryKeepEmpty, err
	}
	if duplicate {
		return nil, categoryKeepParseFailure, nil // ruamel DuplicateKeyError is a parser failure too.
	}
	root, err := categoryYAMLNode(document.Content[0])
	if err != nil {
		return nil, categoryKeepEmpty, err
	}
	if categoryYAMLEmpty(root) {
		return nil, categoryKeepEmpty, nil
	}
	policy, err := parseMediaCategoryPolicy(raw)
	return policy, categoryReplaceMaps, err
}

func categoryPluginNodes(node *yaml.Node, parents map[*yaml.Node]bool, budget *int) (bool, error) {
	*budget--
	if *budget < 0 || len(parents) >= 32 || parents[node] {
		return false, categoryPolicyError("YAML循环或展开超过限制")
	}
	path := make(map[*yaml.Node]bool, len(parents)+1)
	for n := range parents {
		path[n] = true
	}
	path[node] = true
	if node.Kind == yaml.AliasNode {
		if node.Alias == nil {
			return false, categoryPolicyError("YAML别名没有有效目标")
		}
		return categoryPluginNodes(node.Alias, path, budget)
	}
	switch node.Tag {
	case "", "!!map", "!!seq", "!!str", "!!null", "!!bool", "!!int", "!!float", "!!merge":
	default:
		return false, categoryPolicyError("此YAML标签的原生语义尚未实现")
	}
	duplicate := false
	if node.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			if key.Kind != yaml.ScalarNode {
				return false, categoryPolicyError("复杂YAML键的原生语义尚未实现")
			}
			id := key.Tag + "\x00" + key.Value
			if seen[id] {
				duplicate = true
			}
			seen[id] = true
		}
	}
	for _, child := range node.Content {
		found, err := categoryPluginNodes(child, path, budget)
		if err != nil {
			return false, err
		}
		duplicate = duplicate || found
	}
	return duplicate, nil
}

func (a *App) currentMediaCategoryPolicy() (mediaCategoryPolicy, error) {
	raw, err := readMediaCategoryPolicy(a.Dir)
	if err != nil {
		return nil, err
	}
	runtime := &a.mediaCategories
	runtime.mu.Lock()
	parserWarning := false
	defer func() {
		runtime.mu.Unlock()
		if parserWarning {
			a.log("WARN", "category", "parse_failed_cached", "二级分类策略解析失败；保留当前进程分类，冷启动时为空", "", "")
		}
	}()
	digest := sha256.Sum256(raw)
	if runtime.initialized && runtime.digest == digest {
		return runtime.policy, nil
	}
	policy, kind, err := categoryPluginReload(raw)
	if err != nil {
		return nil, err
	}
	if kind == categoryReplaceMaps {
		runtime.policy = policy
	}
	parserWarning = kind == categoryKeepParseFailure
	if runtime.policy == nil {
		runtime.policy = mediaCategoryPolicy{}
	}
	runtime.initialized, runtime.digest = true, digest
	return runtime.policy, nil
}
