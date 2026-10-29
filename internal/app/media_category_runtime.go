package app

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
	"golang.org/x/sys/unix"
)

// CategoryHelper.init replaces its movie/tv maps only for a truthy parsed root.
// Empty input and ordinary parser failures retain the old process-local maps;
// a cold process starts with empty maps. This cache is never persisted as rules.
type mediaCategoryRuntime struct {
	mu          sync.Mutex
	initialized bool
	policy      mediaCategoryPolicy
	// A truthy non-mapping _categorys raises at .get outside the source IO
	// handler. Remember it separately: getters still use the previous maps,
	// but a later init whose read fails retries that same .get operation.
	rootError error
}

type categoryReloadKind uint8

const (
	categoryKeepEmpty categoryReloadKind = iota
	categoryKeepParseFailure
	categoryReplaceMaps
	categoryKeepRootFailure
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
	if err := categoryPluginResolveYAML11(document.Content[0], raw); err != nil {
		return nil, categoryKeepEmpty, err
	}
	// Nodes never construct or execute tags. Unsupported native shapes, custom
	// tags, cycles and resource expansion fail before accepting replacement maps.
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
	if root.Kind != yaml.MappingNode {
		return nil, categoryKeepRootFailure, categoryPolicyError("顶层必须是movie和tv分类映射")
	}
	policy, err := parseMediaCategoryPolicyConditions(raw, true)
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
			id := categoryPluginKeyIdentity(key)
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
	return a.loadMediaCategoryRuntime(false)
}

// CategoryHelper getters only consult process-local maps. Only explicit init
// (including CategoryEditor after writing) reloads an already initialized helper.
func (a *App) reloadMediaCategoryPolicy() (mediaCategoryPolicy, error) {
	return a.loadMediaCategoryRuntime(true)
}

func (a *App) loadMediaCategoryRuntime(reload bool) (mediaCategoryPolicy, error) {
	runtime := &a.mediaCategories
	runtime.mu.Lock()
	warning := ""
	defer func() {
		runtime.mu.Unlock()
		switch warning {
		case "parse_failed_cached":
			a.log("WARN", "category", warning, "二级分类策略解析失败；保留当前进程分类，冷启动时为空", "", "")
		case "load_failed_cached":
			a.log("WARN", "category", warning, "二级分类策略配置文件加载出错；保留当前进程分类，冷启动时为空", "", "")
		}
	}()
	if runtime.initialized && !reload {
		return runtime.policy, nil
	}
	raw, err := readCategoryHelperPolicy(a.Dir)
	if err != nil {
		var loadError *categoryHelperLoadError
		if !errors.As(err, &loadError) {
			return nil, err // Host safety boundaries are not ordinary source IO failures.
		}
		warning = "load_failed_cached"
		if runtime.rootError != nil {
			return nil, runtime.rootError
		}
		if runtime.policy == nil {
			runtime.policy = mediaCategoryPolicy{}
		}
		runtime.initialized = true
		return runtime.policy, nil
	}
	policy, kind, err := categoryPluginReload(raw)
	if kind == categoryKeepRootFailure {
		runtime.rootError = err
	}
	if err != nil {
		return nil, err
	}
	runtime.rootError = nil
	if kind == categoryReplaceMaps {
		runtime.policy = policy
	}
	if kind == categoryKeepParseFailure {
		warning = "parse_failed_cached"
	}
	if runtime.policy == nil {
		runtime.policy = mediaCategoryPolicy{}
	}
	runtime.initialized = true
	return runtime.policy, nil
}

// Only real filesystem IO errors take the source's warn-and-retain path.
// Resource bounds and regular-file/no-symlink checks remain fatal and distinct.
type categoryHelperLoadError struct{ error }

func (e *categoryHelperLoadError) Unwrap() error { return e.error }

func readCategoryHelperPolicy(dataDir string) ([]byte, error) {
	path := filepath.Join(dataDir, "category.yaml")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		// Source init copies its packaged default into the owned config path.
		// Exclusive creation must never truncate a path created concurrently.
		fd, createErr := unix.Open(path, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
		if createErr == nil {
			file := os.NewFile(uintptr(fd), path)
			_, writeErr := file.WriteString(defaultMediaCategoryPolicy)
			closeErr := file.Close()
			if writeErr != nil {
				return nil, &categoryHelperLoadError{writeErr}
			}
			if closeErr != nil {
				return nil, &categoryHelperLoadError{closeErr}
			}
		} else if !errors.Is(createErr, os.ErrExist) {
			return nil, &categoryHelperLoadError{createErr}
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return nil, &categoryHelperLoadError{err}
	}
	if !info.Mode().IsRegular() || info.Size() > maxCategoryPolicyBytes {
		return nil, categoryPolicyError("请使用不超过64 KiB的普通配置文件")
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, unix.ELOOP) {
		return nil, categoryPolicyError("请使用不超过64 KiB的普通配置文件")
	}
	if err != nil {
		return nil, &categoryHelperLoadError{err}
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return nil, &categoryHelperLoadError{err}
	}
	if !info.Mode().IsRegular() || info.Size() > maxCategoryPolicyBytes {
		return nil, categoryPolicyError("请使用不超过64 KiB的普通配置文件")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxCategoryPolicyBytes+1))
	if err != nil {
		return nil, &categoryHelperLoadError{err}
	}
	if len(raw) > maxCategoryPolicyBytes {
		return nil, categoryPolicyError("配置文件超过64 KiB")
	}
	return raw, nil
}
