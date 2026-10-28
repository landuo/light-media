package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"light-media/internal/plugins/iyuuautoseed/compat/pythonre"
	"light-media/internal/transferextensions"
)

var originalTransferSubtitleMatchers struct {
	sync.Once
	values []*pythonre.Pattern
	err    error
}

func originalTransferSubtitleName(source, target string, defaultSub any) (string, error) {
	return originalTransferSubtitleItemName(filepath.Base(source), *transferextensions.PathFile(source).Extension, target, defaultSub)
}

func originalTransferSubtitleItemName(source, extension, target string, defaultSub any) (string, error) {
	originalTransferSubtitleMatchers.Do(func() {
		for _, pattern := range originalTransferSubtitlePatterns {
			compiled, err := pythonre.Compile("(?i)" + pattern.Pattern)
			if err != nil {
				originalTransferSubtitleMatchers.err = err
				return
			}
			originalTransferSubtitleMatchers.values = append(originalTransferSubtitleMatchers.values, compiled)
		}
	})
	if err := originalTransferSubtitleMatchers.err; err != nil {
		return "", err
	}
	tag := ""
	for index, matcher := range originalTransferSubtitleMatchers.values {
		matched, err := matcher.Search(filepath.Base(source))
		if err != nil {
			return "", err
		}
		if matched {
			pattern := originalTransferSubtitlePatterns[index]
			tag = pattern.Tag
			if value, ok := defaultSub.(string); ok && value == pattern.Language {
				tag = ".default" + tag
			}
			break
		}
	}
	name := filepath.Base(target)
	if name == "/" || name == "." || name == ".." {
		return "", originalTransferRenameInvalid()
	}
	// PathFile shares pinned pathlib suffix semantics, including hidden names
	// and trailing dots; filepath.Ext alone differs on those source cases.
	if ext := *transferextensions.PathFile(target).Extension; ext != "" {
		name = strings.TrimSuffix(name, "."+ext)
	}
	return filepath.Join(filepath.Dir(target), name+tag+"."+extension), nil
}

func (a *App) originalDefaultSubtitle() (any, error) {
	if value, present := os.LookupEnv("DEFAULT_SUB"); present {
		return value, nil
	}
	raw := a.Settings().DefaultSub
	if len(raw) == 0 {
		return "zh-cn", nil
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil, bad("INVALID_DEFAULT_SUB", "默认字幕语言不是有效配置")
	}
	if value != nil {
		if _, ok := value.(string); !ok {
			return nil, bad("INVALID_DEFAULT_SUB", "默认字幕语言必须为字符串或空值")
		}
	}
	return value, nil
}
