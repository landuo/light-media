// OwnedLocalFiles is an acceptance plugin. It proves that ordinary Go file IO
// can use an explicitly configured backup mount and the platform data path.
// It contains no downloader migration or scheduled business operation.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"light-media/internal/plugins"
)

var fixtureVersion = "1.0"

type configuration struct {
	BackupPath string `json:"backup_path"`
	Hash       string `json:"hash"`
	RunOnce    bool   `json:"run_once"`
}
type observation struct {
	Instance   string `json:"instance"`
	PID        int    `json:"pid"`
	Version    string `json:"version"`
	DataPath   string `json:"data_path"`
	TorrentSHA string `json:"torrent_sha"`
	ResumeSHA  string `json:"resume_sha"`
	TempPath   string `json:"temp_path"`
	Error      string `json:"error,omitempty"`
}

func manifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "OwnedLocalFiles", Name: "本地文件验收插件", Version: fixtureVersion, Runtime: "go-executable", Entry: "owned-local-files-fixture", HostAPIMin: 1, HostAPIMax: 1,
		Methods: []string{"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "get_form", "get_page", "http", "stop"}, Capabilities: plugins.BaseMethods(), HasPage: true,
		APIs: []plugins.HTTPAPI{{Key: "snapshot", Path: "/snapshot", Methods: []string{"GET"}, Auth: "bear"}, {Key: "run", Path: "/run", Methods: []string{"POST"}, Auth: "bear"}, {Key: "cleanup", Path: "/cleanup", Methods: []string{"DELETE"}, Auth: "bear"}}}
}
func observationKey() *string { s := "observation"; return &s }
func digest(b []byte) string  { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func readBackup(root *os.Root, name string) ([]byte, error) {
	st, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > 8<<20 {
		return nil, errors.New("backup is not a bounded regular file")
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (8<<20)+1))
	if len(b) > 8<<20 {
		return nil, errors.New("backup exceeds fixture limit")
	}
	return b, err
}
func execute(ctx context.Context, base plugins.Base, config configuration) (observation, error) {
	meta, err := base.GetInstanceMetadata(ctx)
	if err != nil {
		return observation{}, err
	}
	path, err := base.GetDataPath(ctx)
	if err != nil {
		return observation{}, err
	}
	out := observation{Instance: meta.InstanceID, PID: os.Getpid(), Version: fixtureVersion, DataPath: path}
	if !plugins.DownloaderTaskHashValid(config.Hash) || !filepath.IsAbs(config.BackupPath) {
		return out, errors.New("explicit mounted backup path and exact hash required")
	}
	root, err := os.OpenRoot(config.BackupPath)
	if err != nil {
		return out, errors.New("configured backup directory unavailable")
	}
	defer root.Close()
	torrent, err := readBackup(root, config.Hash+".torrent")
	if err != nil {
		return out, errors.New("torrent backup unavailable")
	}
	resume, err := readBackup(root, config.Hash+".fastresume")
	if err != nil {
		return out, errors.New("fastresume backup unavailable")
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	// Use this instance's platform path, so source and clone do not implement
	// their own identity/clone scheme. Only this fixture's own temp is touched.
	temp := filepath.Join(path, config.Hash+".torrent")
	if err = os.WriteFile(temp, torrent, 0600); err != nil {
		return out, err
	}
	out.TorrentSHA, out.ResumeSHA, out.TempPath = digest(torrent), digest(resume), temp
	return out, nil
}
func main() {
	m := manifest()
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		_ = json.NewEncoder(os.Stdout).Encode(m)
		return
	}
	var mu sync.Mutex
	var cfg configuration
	handle := func(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
		mu.Lock()
		defer mu.Unlock()
		base := plugins.Base{Host: host}
		run := func() (observation, error) {
			o, e := execute(ctx, base, cfg)
			if e != nil {
				o.Error = e.Error()
			}
			if se := base.SaveData(ctx, "observation", o); se != nil {
				return o, se
			}
			return o, e
		}
		switch method {
		case "init":
			cfg = configuration{}
			if len(raw) > 0 && string(raw) != "null" {
				if err := json.Unmarshal(raw, &cfg); err != nil {
					return nil, err
				}
			}
			if cfg.RunOnce {
				cfg.RunOnce = false
				if _, err := base.UpdateConfig(ctx, cfg); err != nil {
					return nil, err
				}
				_, _ = run()
			}
			return json.RawMessage(`null`), nil
		case "stop":
			return json.RawMessage(`null`), nil
		case "get_state":
			return json.RawMessage(`false`), nil
		case "get_service", "get_command":
			return json.RawMessage(`[]`), nil
		case "get_api":
			return json.Marshal(m.APIs)
		case "get_render_mode":
			return json.RawMessage(`["vuetify",""]`), nil
		case "get_form":
			return json.Marshal([]any{[]any{map[string]any{"component": "VTextField", "props": map[string]any{"model": "backup_path", "label": "备份挂载路径", "hint": "验收专用目录"}}, map[string]any{"component": "VTextField", "props": map[string]any{"model": "hash", "label": "种子 Hash"}}, map[string]any{"component": "VSwitch", "props": map[string]any{"model": "run_once", "label": "保存后执行一次"}}}, cfg})
		case "get_page":
			raw, err := base.GetData(ctx, observationKey())
			if err != nil {
				return nil, err
			}
			return json.Marshal([]any{map[string]any{"component": "VAlert", "props": map[string]any{"type": "info", "text": string(raw)}}})
		case "http":
			var req plugins.HTTPRequest
			if err := json.Unmarshal(raw, &req); err != nil {
				return nil, err
			}
			var result any
			status := 200
			switch req.Key {
			case "snapshot":
				saved, err := base.GetData(ctx, observationKey())
				if err != nil {
					return nil, err
				}
				path, err := base.GetDataPath(ctx)
				if err != nil {
					return nil, err
				}
				result = map[string]any{"pid": os.Getpid(), "version": fixtureVersion, "data_path": path, "observation": saved, "config": cfg}
			case "run":
				o, err := run()
				result = o
				if err != nil {
					status = 422
				}
			case "cleanup":
				path, err := base.GetDataPath(ctx)
				if err != nil {
					return nil, err
				}
				if !plugins.DownloaderTaskHashValid(cfg.Hash) {
					return nil, errors.New("exact hash required")
				}
				err = os.Remove(filepath.Join(path, cfg.Hash+".torrent"))
				if err != nil && !os.IsNotExist(err) {
					return nil, err
				}
				result = map[string]bool{"removed": err == nil}
			default:
				return nil, errors.New("unknown fixture API")
			}
			body, err := json.Marshal(result)
			if err != nil {
				return nil, err
			}
			return json.Marshal(plugins.HTTPResponse{Status: status, Body: body, Headers: map[string]string{"Content-Type": "application/json"}})
		}
		return nil, errors.New("unknown fixture method")
	}
	if err := plugins.Serve(context.Background(), os.Stdin, os.Stdout, m, handle); err != nil {
		fmt.Fprintln(os.Stderr, "owned local files fixture stopped")
		os.Exit(1)
	}
}
