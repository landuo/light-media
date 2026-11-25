package autobackup

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"

	sdk "light-media/builtinplugins/internal/sdk"
)

//go:embed form.json
var sourceForm json.RawMessage

func backupForm(ctx context.Context, caller sdk.HostCaller) (json.RawMessage, error) {
	path, err := (sdk.Base{Host: caller}).GetDataPath(ctx)
	if err != nil {
		return nil, err
	}
	var form []json.RawMessage
	if err := json.Unmarshal(sourceForm, &form); err != nil {
		return nil, err
	}
	if len(form) != 2 {
		return nil, errors.New("invalid AutoBackup form tuple")
	}
	var schema []any
	if err := json.Unmarshal(form[0], &schema); err != nil {
		return nil, err
	}

	// The model deliberately keeps back_path empty. An explicitly configured
	// path is applied by the host's ordinary form merge and survives cloning.
	fields := 0
	var project func([]any)
	project = func(nodes []any) {
		for _, child := range nodes {
			node, ok := child.(map[string]any)
			if !ok {
				continue
			}
			if props, ok := node["props"].(map[string]any); ok && node["component"] == "VTextField" && props["model"] == "back_path" {
				props["placeholder"] = path
				props["hint"] = "留空使用当前实例的默认备份目录：" + path
				props["persistent-hint"] = true
				fields++
			}
			if children, ok := node["content"].([]any); ok {
				project(children)
			}
		}
	}
	project(schema)
	if fields != 1 {
		return nil, errors.New("invalid AutoBackup backup path field")
	}
	form[0], err = json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	return json.Marshal(form)
}
