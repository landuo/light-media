package app

import (
	"errors"
	"math/big"

	"light-media/internal/notice"
)

// PluginLogRotation is a trusted startup override, never plugin supplied. The
// source Pydantic int accepts arbitrary precision; zero/negative values retain
// RotatingFileHandler's disable-rotation/append-only semantics.
type PluginLogRotation struct {
	MaxMiB, Backups *big.Int
}

func PluginLogRotationEnvironment(lookup func(string) (string, bool)) (*PluginLogRotation, error) {
	out := &PluginLogRotation{}
	for _, field := range []struct {
		name string
		to   **big.Int
	}{{"LOG_MAX_FILE_SIZE", &out.MaxMiB}, {"LOG_BACKUP_COUNT", &out.Backups}} {
		if raw, exists := lookup(field.name); exists {
			value, ok := notice.CoerceSourceInteger(raw)
			if !ok {
				return nil, errors.New("invalid_plugin_log_rotation")
			}
			*field.to, ok = new(big.Int).SetString(value.String(), 10)
			if !ok {
				return nil, errors.New("invalid_plugin_log_rotation")
			}
		}
	}
	return out, nil
}

func clonePluginLogRotation(in *PluginLogRotation) *PluginLogRotation {
	if in == nil {
		return nil
	}
	out := &PluginLogRotation{}
	if in.MaxMiB != nil {
		out.MaxMiB = new(big.Int).Set(in.MaxMiB)
	}
	if in.Backups != nil {
		out.Backups = new(big.Int).Set(in.Backups)
	}
	return out
}

func (a *App) pluginProjectionBudget() (mib, backups *big.Int) {
	mib, backups = big.NewInt(5), big.NewInt(pluginProjectionBackups)
	c := a.Settings()
	if c.LogMaxMiB != 0 {
		mib.SetInt64(int64(c.LogMaxMiB))
	}
	if c.LogRetainFiles != 0 {
		backups.SetInt64(int64(c.LogRetainFiles))
	}
	if override := a.pluginLogRotation; override != nil {
		if override.MaxMiB != nil {
			mib.Set(override.MaxMiB)
		}
		if override.Backups != nil {
			backups.Set(override.Backups)
		}
	}
	return
}
