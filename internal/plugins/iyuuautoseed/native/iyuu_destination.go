package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"path"
	"strings"
)

// IYUUDestination is an observation of one authorized daemon, never a local
// filesystem capability. The source's add arguments are left unchanged.
type IYUUDestination struct {
	Version                     int
	Kind, BindingSHA, ConfigSHA string
	EffectivePath, Category     string
	AutoTMM                     bool
}

func destinationDigest(value any) string {
	raw, _ := json.Marshal(value)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func (c *IYUUClient) addCategoryMode(spec IYUUAddSpec) (*string, bool) {
	category, auto := spec.Category, c.options.AutoCategory
	if !spec.IgnoreCategoryCheck && (category == nil || *category == "" || !auto) {
		category, auto = nil, false
	}
	return category, auto
}

func (c *IYUUClient) DestinationRequired(spec IYUUAddSpec) bool {
	if spec.SavePath == nil || *spec.SavePath == "" {
		return true
	}
	_, auto := c.addCategoryMode(spec)
	return c.client.config.Kind == "qbittorrent" && auto
}

func (c *IYUUClient) EffectiveAddDestination(ctx context.Context, spec IYUUAddSpec) (IYUUDestination, error) {
	if err := c.client.nativeLock(ctx); err != nil {
		return IYUUDestination{}, err
	}
	defer c.client.mu.Unlock()
	return c.effectiveAddDestinationLocked(ctx, spec)
}

func daemonDirectory(value string) (string, error) {
	if value == "" || !strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\x00\r\n\\") || len(value) > 4096 {
		return "", failure("iyuu_destination", "invalid_daemon_directory")
	}
	return path.Clean(value), nil
}

func (c *IYUUClient) effectiveAddDestinationLocked(ctx context.Context, spec IYUUAddSpec) (IYUUDestination, error) {
	ctx = mutationReadOnly(ctx)
	out := IYUUDestination{Version: 1, Kind: c.client.config.Kind, BindingSHA: destinationDigest(struct {
		Config Config
		APIKey string
	}{c.client.config, c.options.APIKey})}
	if out.Kind == "transmission" {
		// The original TR wrapper retains an explicit empty string. Unlike qB's
		// wrapper, it does not omit it; the daemon rejects that relative path.
		// Do not mint a successful default-directory grant for those arguments.
		if spec.SavePath != nil && *spec.SavePath == "" {
			return out, failure("iyuu_destination", "empty_explicit_transmission_directory")
		}
		if err := c.client.trConnect(ctx); err != nil {
			return out, err
		}
		settings, err := c.client.trRPC(ctx, "session_get", map[string]any{}, c.client.trDialect)
		if err != nil {
			return out, err
		}
		var directory string
		if json.Unmarshal(settings["download_dir"], &directory) != nil {
			if json.Unmarshal(settings["download-dir"], &directory) != nil {
				return out, failure("iyuu_destination", "missing_daemon_directory")
			}
		}
		directory, err = daemonDirectory(directory)
		if err != nil {
			return out, err
		}
		out.ConfigSHA = destinationDigest(struct{ Dialect, Directory string }{c.client.trDialect, directory})
		out.EffectivePath = directory
		if spec.SavePath != nil && *spec.SavePath != "" {
			out.EffectivePath, err = daemonDirectory(*spec.SavePath)
		}
		return out, err
	}
	if out.Kind != "qbittorrent" {
		return out, failure("iyuu_destination", "unsupported_kind")
	}
	raw, err := c.client.qb(ctx, http.MethodGet, "app/preferences", "", "")
	if err != nil {
		return out, err
	}
	var preferences struct {
		SavePath         string `json:"save_path"`
		Subcategories    bool   `json:"use_subcategories"`
		ManualCategories bool   `json:"use_category_paths_in_manual_mode"`
	}
	if err = decode(raw, &preferences); err != nil {
		return out, err
	}
	preferences.SavePath, err = daemonDirectory(preferences.SavePath)
	if err != nil {
		return out, err
	}
	category, auto := c.addCategoryMode(spec)
	out.AutoTMM = auto
	if category != nil {
		out.Category = *category
	}
	out.EffectivePath = preferences.SavePath
	used := map[string]string{}
	if (auto || preferences.ManualCategories) && out.Category != "" {
		raw, err = c.client.qb(ctx, http.MethodGet, "torrents/categories", "", "")
		if err != nil {
			return out, err
		}
		var categories map[string]struct {
			SavePath string `json:"savePath"`
		}
		if err = decode(raw, &categories); err != nil {
			return out, err
		}
		var resolve func(string, int) (string, error)
		resolve = func(name string, depth int) (string, error) {
			if name == "" {
				return preferences.SavePath, nil
			}
			if depth > 64 || len(name) > 4096 || strings.ContainsAny(name, "\x00\r\n\\") {
				return "", failure("iyuu_destination", "invalid_category")
			}
			value := categories[name].SavePath
			used[name] = value
			base := preferences.SavePath
			if value == "" {
				value = name
				if preferences.Subcategories {
					if index := strings.LastIndex(name, "/"); index >= 0 {
						base, err = resolve(name[:index], depth+1)
						if err != nil {
							return "", err
						}
						value = name[index+1:]
					}
				}
			}
			if !strings.HasPrefix(value, "/") {
				value = path.Join(base, value)
			}
			return daemonDirectory(value)
		}
		out.EffectivePath, err = resolve(out.Category, 0)
		if err != nil {
			return out, err
		}
	}
	// qB ignores explicit savepath under ATM; in manual mode an explicitly
	// supplied absolute path wins over the suggested category/default path.
	if !auto && spec.SavePath != nil && *spec.SavePath != "" {
		out.EffectivePath, err = daemonDirectory(*spec.SavePath)
		if err != nil {
			return out, err
		}
	}
	out.ConfigSHA = destinationDigest(struct {
		Preferences any
		Categories  map[string]string
		Category    string
		Auto        bool
	}{preferences, used, out.Category, auto})
	return out, nil
}

// Revalidation is inside the same client lock and authenticated dispatch path.
// A remote daemon cannot offer an atomic preference-read-and-add transaction;
// the Host therefore also verifies the actual task before granting controls.
func (c *IYUUClient) AddPausedAt(ctx context.Context, spec IYUUAddSpec, expected IYUUDestination) (IYUUAddResult, error) {
	return c.addPaused(ctx, spec, &expected)
}
