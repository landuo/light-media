package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
	"light-media/internal/plugins"
)

func categoryEditorCapability(method string) bool {
	return method == plugins.CategoryDefaultCapability || method == plugins.CategoryApplyCapability || method == plugins.SystemMessagePutCapability
}

// The worker owns the source order and its members. These operations only
// bridge the existing policy and MessageHelper resources; no native plugin is
// loaded and no legacy invoke/config_form protocol is accepted.
func (a *App) categoryEditorResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	if method == plugins.CategoryDefaultCapability {
		var input struct{}
		if err := originalResourceDecode(raw, &input); err != nil {
			return nil, err
		}
		return json.Marshal(defaultMediaCategoryPolicy)
	}
	if method == plugins.SystemMessagePutCapability {
		var input struct {
			Message string `json:"message"`
			Title   string `json:"title"`
		}
		if err := originalResourceDecode(raw, &input); err != nil {
			return nil, err
		}
		if err := scope.validate(ctx); err != nil {
			return nil, err
		}
		// MessageHelper.put defaults to role=plugin. A duplicate is a normal
		// no-op, not an exception, and it never routes to an external channel.
		title := a.clean(input.Title)
		a.systemMessages.put("plugin", &title, a.clean(input.Message), nil, time.Now())
		return json.RawMessage("null"), nil
	}
	var input categoryEditorApplyInput
	if err := categoryEditorApplyDecode(raw, &input); err != nil {
		return nil, err
	}
	var content string
	if len(input.Content) == 0 || string(input.Content) == "null" || json.Unmarshal(input.Content, &content) != nil {
		return nil, bad("INVALID_PLUGIN_VALUE", "分类配置文件内容必须是字符串")
	}
	// Retain the existing application's bounded policy-file contract. This is
	// an explicit host adaptation, not a claim that Python had the same limit.
	if len(content) > maxCategoryPolicyBytes {
		return nil, categoryPolicyError("配置文件超过64 KiB")
	}
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	path := filepath.Join(a.Dir, "category.yaml")
	// No plugin-selected path and no symlink/device target. Open before
	// truncating so a non-regular file cannot be accidentally modified.
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("category policy is not a regular file")
	}
	if err = file.Truncate(0); err != nil {
		return nil, err
	}
	// Python's UTF-8 text writer rejects an unpaired JSON surrogate after
	// opening/truncating the file. encoding/json would silently substitute U+FFFD.
	// Inspect the original JSON spelling so that source failure is not success.
	if categoryUnpairedSurrogate(input.Content) {
		return nil, bad("INVALID_PLUGIN_VALUE", "分类配置包含无法编码为UTF-8的字符")
	}
	if _, err = file.WriteString(content); err != nil {
		return nil, err
	}
	if err = file.Close(); err != nil {
		return nil, err
	}
	// Like write_text -> CategoryHelper.init, writing is not rolled back when
	// reload fails. Existing warm-cache semantics preserve maps for empty or
	// syntactically invalid YAML; a cold application starts with empty maps.
	if _, err = a.reloadMediaCategoryPolicy(); err != nil {
		return nil, err
	}
	return json.RawMessage("null"), nil
}

func categoryUnpairedSurrogate(raw []byte) bool {
	for i := 1; i+1 < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if raw[i] != 'u' || i+4 >= len(raw) {
			continue
		}
		n, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			continue
		} // JSON validity is checked before this function.
		i += 4
		if n >= 0xDC00 && n <= 0xDFFF {
			return true
		}
		if n < 0xD800 || n > 0xDBFF {
			continue
		}
		if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
			return true
		}
		low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
		if err != nil || low < 0xDC00 || low > 0xDFFF {
			return true
		}
		i += 6
	}
	return false
}
