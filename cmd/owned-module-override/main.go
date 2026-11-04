// Owned protocol fixture; every observation is in its process or app test DB.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"light-media/internal/plugins"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var declareModules = "true"
var version = "1.0"
var fixtureProcessAPI = ""
var fixtureCatalogEvents = ""
var fixtureTransferEvents = ""

func manifest() plugins.Manifest {
	m := plugins.Manifest{SchemaVersion: 1, PluginID: "OwnedModuleOverride", Name: "Owned module override", Version: version, Runtime: "go-executable", Entry: "owned-module-override", HostAPIMin: 1, HostAPIMax: 1, Capabilities: plugins.BaseMethods(), Methods: []string{"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "get_form", "get_page", "get_dashboard", "get_sidebar_nav", "module_execute", "module_page_size", "module_fail", "snapshot", "close", "stop_service", "stop"}}
	if declareModules == "true" {
		m.Methods = append(m.Methods, "get_module")
	}
	m.Methods = append(m.Methods, "module_filter", "module_images", "module_search_media", "module_episodes", "module_metadata_nfo", "module_storage", "module_storage_mkdir", "module_storage_rename", "module_storage_delete", "module_transfer", "module_storage_parent", "module_storage_siblings", "module_storage_tree")
	if fixtureTransferEvents != "" {
		m.Methods = append(m.Methods, "transfer_event", "transfer_chain_event")
		for _, name := range []string{"transfer.complete", "transfer.failed", "transfer.subtitle.complete", "transfer.subtitle.failed", "transfer.audio.complete", "transfer.audio.failed", "metadata.scrape"} {
			m.BroadcastHandlers = append(m.BroadcastHandlers, plugins.BroadcastHandler{Event: name, Method: "transfer_event"})
		}
		for _, name := range []string{"storage.operation", "transfer.rename.build", "transfer.rename"} {
			m.ChainHandlers = append(m.ChainHandlers, plugins.ChainHandler{Event: name, Method: "transfer_chain_event"})
		}
	}
	if fixtureProcessAPI != "" {
		m.Methods = append(m.Methods, "http")
		m.APIs = []plugins.HTTPAPI{{Key: "snapshot", Path: "/snapshot", Methods: []string{"GET"}, Auth: "bear"}}
	}
	if fixtureCatalogEvents != "" {
		m.Methods = append(m.Methods, "catalog_event")
		m.ChainHandlers = append(m.ChainHandlers, plugins.ChainHandler{Event: "recommend.source", Method: "catalog_event"}, plugins.ChainHandler{Event: "discover.source", Method: "catalog_event"}, plugins.ChainHandler{Event: "media.recognize.convert", Method: "catalog_event"}, plugins.ChainHandler{Event: "name.recognize", Method: "catalog_event"})
		m.APIs = append(m.APIs, plugins.HTTPAPI{Key: "feed", Path: "/feed", Methods: []string{"GET"}, Auth: "bear"})
	}
	return m
}

type config struct {
	StorageTree           map[string]json.RawMessage `json:"storage_tree"`
	TransferHold          string                     `json:"transfer_hold"`
	StorageParent         json.RawMessage            `json:"storage_parent"`
	StorageSiblings       json.RawMessage            `json:"storage_siblings"`
	RenameSourceExtension bool                       `json:"rename_source_extension"`
	TransferResults       map[string]json.RawMessage `json:"transfer_results"`
	TransferTargets       map[string]string          `json:"transfer_targets"`
	BuildTransferInfo     bool                       `json:"build_transfer_info"`
	TransferTarget        string                     `json:"transfer_target"`
	TransferRoot          string                     `json:"transfer_root"`
	TransferMode          string                     `json:"transfer_mode"`
	StorageRoot           string                     `json:"storage_root"`
	StorageAction         string                     `json:"storage_action"`
	StorageMode           string                     `json:"storage_mode"`
	Enabled               bool                       `json:"enabled"`
	FailState             bool                       `json:"fail_state"`
	FailModule            bool                       `json:"fail_module"`
	Modules               json.RawMessage            `json:"modules"`
	Result                json.RawMessage            `json:"result"`
	SearchResults         map[string]json.RawMessage `json:"search_results"`
	RecognizeResults      map[string]json.RawMessage `json:"recognize_results"`
	RecognizeMissing      json.RawMessage            `json:"recognize_missing"`
	PageSize              json.RawMessage            `json:"page_size"`
	ImageMode             string                     `json:"image_mode"`
	MediaResult           json.RawMessage            `json:"media_result"`
	EpisodeResult         json.RawMessage            `json:"episode_result"`
	NFOResult             json.RawMessage            `json:"nfo_result"`
	FilterMode            string                     `json:"filter_mode"`
	RateLimited           bool                       `json:"rate_limited"`
	EventMutations        map[string]json.RawMessage `json:"event_mutations"`
	EventFailure          bool                       `json:"event_failure"`
	EventAppendSources    bool                       `json:"event_append_sources"`
	Feed                  json.RawMessage            `json:"feed"`
	FeedPages             map[string]json.RawMessage `json:"feed_pages"`
	FeedFail              bool                       `json:"feed_fail"`
}
type fixture struct {
	StorageCalls   []json.RawMessage `json:"storage_calls"`
	TransferCalls  []json.RawMessage `json:"transfer_calls"`
	TransferEvents []json.RawMessage `json:"transfer_events"`
	config         config
	Calls          map[string]int             `json:"calls"`
	Inputs         map[string]json.RawMessage `json:"inputs"`
	PID            int                        `json:"pid"`
	LastFeed       json.RawMessage            `json:"last_feed"`
	SearchKeywords []string                   `json:"search_keywords"`
	ModuleInputs   []json.RawMessage          `json:"module_inputs"`
	TransferTrace  []string                   `json:"transfer_trace"`
}

func (f *fixture) handle(ctx context.Context, method string, input json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	f.Calls[method]++
	f.Inputs[method] = append(json.RawMessage(nil), input...)
	if method == "module_transfer" || method == "module_execute" || method == "module_fail" {
		f.TransferTrace = append(f.TransferTrace, method)
		var call struct{ Kwargs map[string]json.RawMessage }
		if json.Unmarshal(input, &call) == nil && len(call.Kwargs["fileitem"]) > 0 {
			f.TransferCalls = append(f.TransferCalls, append(json.RawMessage(nil), input...))
		}
	}
	switch method {
	case "init":
		stored, err := (plugins.Base{Host: host}).GetConfig(ctx)
		if err != nil {
			return nil, err
		}
		f.config = config{}
		if len(stored) > 0 && string(stored) != "null" {
			if err = json.Unmarshal(stored, &f.config); err != nil {
				return nil, err
			}
		}
		return json.RawMessage(`null`), nil
	case "get_state":
		if f.config.FailState {
			return nil, errors.New("owned state failure")
		}
		return json.Marshal(f.config.Enabled)
	case "get_module":
		if f.config.FailModule {
			return nil, errors.New("owned declaration failure")
		}
		if len(f.config.Modules) == 0 {
			return json.RawMessage(`null`), nil
		}
		return f.config.Modules, nil
	case "module_execute":
		f.ModuleInputs = append(f.ModuleInputs, append(json.RawMessage(nil), input...))
		if f.config.TransferHold != "" {
			path := f.config.TransferHold
			rel, err := filepath.Rel(f.config.StorageRoot, path)
			if err != nil || !filepath.IsAbs(f.config.StorageRoot) || !filepath.IsAbs(path) || filepath.Clean(path) != path || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				return nil, errors.New("owned hold path rejected")
			}
			entered, err := os.OpenFile(path+".entered", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0640)
			if err == nil {
				entered.Close()
			} else if !os.IsExist(err) {
				return nil, err
			}
			ready := false
			for until := time.Now().Add(10 * time.Second); time.Now().Before(until); {
				if stat, err := os.Lstat(path); err == nil && stat.Mode().IsRegular() {
					ready = true
					break
				} else if err != nil && !os.IsNotExist(err) {
					return nil, err
				}
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(10 * time.Millisecond):
				}
			}
			if !ready {
				return nil, errors.New("owned hold expired")
			}
		}
		if f.config.RateLimited {
			return nil, fmt.Errorf("owned private throttle text: %w", plugins.ErrModuleRateLimited)
		}
		if f.config.RecognizeResults != nil {
			var call struct {
				Kwargs struct {
					Source  string
					MediaID string `json:"mediaid"`
					Meta    struct{ Name string }
				}
			}
			if json.Unmarshal(input, &call) != nil {
				return nil, errors.New("invalid owned recognition query input")
			}
			key := call.Kwargs.Source + ":" + call.Kwargs.MediaID
			if call.Kwargs.Meta.Name != "" {
				key = "name:" + call.Kwargs.Meta.Name
			}
			if result, present := f.config.RecognizeResults[key]; present {
				return result, nil
			}
			if len(f.config.RecognizeMissing) > 0 {
				return f.config.RecognizeMissing, nil
			}
			return json.RawMessage(`false`), nil
		}
		if f.config.SearchResults != nil {
			var call struct {
				Kwargs struct {
					Keyword string `json:"keyword"`
				}
			}
			if json.Unmarshal(input, &call) != nil {
				return nil, errors.New("invalid owned media query input")
			}
			f.SearchKeywords = append(f.SearchKeywords, call.Kwargs.Keyword)
			if value, present := f.config.SearchResults[call.Kwargs.Keyword]; present {
				return value, nil
			}
			return json.RawMessage(`{}`), nil
		}
		return f.configuredTransferResult(input)
	case "module_fail":
		return nil, errors.New("owned module failure")
	case "transfer_event":
		f.TransferEvents = append(f.TransferEvents, append(json.RawMessage(nil), input...))
		return json.RawMessage(`null`), nil
	case "module_transfer":
		var call struct {
			Kwargs struct {
				FileItem struct{ Path, Type string }
				Preview  bool
			}
		}
		if err := json.Unmarshal(input, &call); err != nil {
			return nil, err
		}
		if call.Kwargs.Preview {
			return f.configuredTransferResult(input)
		}
		path := filepath.Clean(call.Kwargs.FileItem.Path)
		target := f.config.TransferTarget
		if selected, ok := f.config.TransferTargets[path]; ok {
			target = selected
		}
		for _, pair := range [][2]string{{f.config.StorageRoot, path}, {f.config.TransferRoot, target}} {
			rel, err := filepath.Rel(pair[0], pair[1])
			if err != nil || !filepath.IsAbs(pair[0]) || !filepath.IsAbs(pair[1]) || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				return nil, errors.New("owned transfer path rejected")
			}
		}
		if f.config.StorageMode != "no_effect" {
			if call.Kwargs.FileItem.Type == "dir" {
				if err := f.transferOwnedDirectory(path, target); err != nil {
					return nil, err
				}
				return f.configuredTransferResult(input)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0750); err != nil {
				return nil, err
			}
			if f.config.TransferMode == "copy" || f.config.TransferMode == "corrupt" {
				src, err := os.Open(path)
				if err != nil {
					return nil, err
				}
				defer src.Close()
				dst, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0640)
				if err != nil {
					return nil, err
				}
				defer dst.Close()
				if f.config.TransferMode == "corrupt" {
					info, err := src.Stat()
					if err != nil {
						return nil, err
					}
					_, err = dst.Write([]byte(strings.Repeat("x", int(info.Size()))))
					if err != nil {
						return nil, err
					}
				} else if _, err = io.Copy(dst, src); err != nil {
					return nil, err
				}
			} else if err := os.Link(path, target); err != nil {
				return nil, err
			}
			if f.config.StorageMode == "effect_then_throw" {
				return nil, errors.New("owned transfer changed then failed")
			}
		}
		return f.configuredTransferResult(input)
	case "module_storage_tree":
		f.StorageCalls = append(f.StorageCalls, append(json.RawMessage(nil), input...))
		var call struct {
			Kwargs struct {
				Path     string
				FileItem struct{ Path string }
			}
		}
		if err := json.Unmarshal(input, &call); err != nil {
			return nil, err
		}
		key := "get_file_item:" + filepath.Clean(call.Kwargs.Path)
		if call.Kwargs.FileItem.Path != "" {
			key = "list_files:" + filepath.Clean(call.Kwargs.FileItem.Path)
		}
		if value, ok := f.config.StorageTree[key]; ok {
			return value, nil
		}
		return json.RawMessage(`null`), nil
	case "module_storage_parent", "module_storage_siblings":
		value := f.config.StorageParent
		if method == "module_storage_siblings" {
			value = f.config.StorageSiblings
		}
		if len(value) == 0 {
			return json.RawMessage(`null`), nil
		}
		return value, nil
	case "module_storage", "module_storage_mkdir", "module_storage_rename", "module_storage_delete":
		action := f.config.StorageAction
		if strings.HasPrefix(method, "module_storage_") {
			action = strings.TrimPrefix(method, "module_storage_")
		}
		var call struct {
			Kwargs struct {
				FileItem struct{ Path string }
				Name     string
			}
		}
		if err := json.Unmarshal(input, &call); err != nil {
			return nil, err
		}
		path := filepath.Clean(call.Kwargs.FileItem.Path)
		rel, err := filepath.Rel(f.config.StorageRoot, path)
		if err != nil || !filepath.IsAbs(f.config.StorageRoot) || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return nil, errors.New("owned storage path rejected")
		}
		if f.config.StorageMode != "no_effect" {
			switch action {
			case "mkdir", "rename":
				name := call.Kwargs.Name
				if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
					return nil, errors.New("owned storage name rejected")
				}
				if action == "mkdir" {
					err = os.Mkdir(filepath.Join(path, name), 0750)
				} else {
					err = os.Rename(path, filepath.Join(filepath.Dir(path), name))
				}
			case "delete":
				err = os.RemoveAll(path)
			default:
				return nil, errors.New("owned storage action rejected")
			}
			if err != nil {
				return nil, err
			}
		}
		if f.config.StorageMode == "effect_then_throw" {
			return nil, errors.New("owned storage changed then failed")
		}
		if len(f.config.Result) == 0 {
			return json.RawMessage(`null`), nil
		}
		return f.config.Result, nil
	case "module_page_size":
		if len(f.config.PageSize) == 0 {
			return json.RawMessage(`null`), nil
		}
		return f.config.PageSize, nil
	case "module_images":
		return plugins.HandleMediaImages(input, func(model *plugins.ModuleMediaInfo) (json.RawMessage, error) {
			if f.config.ImageMode == "second" {
				model.Fields["backdrop_path"] = json.RawMessage(`"plugin-backdrop"`)
				return nil, nil
			}
			model.Fields["poster_path"] = json.RawMessage(`"plugin-poster"`)
			switch f.config.ImageMode {
			case "false":
				return json.RawMessage(`false`), nil
			case "dict":
				return json.RawMessage(`{}`), nil
			case "list":
				return json.RawMessage(`["ignored-list-item"]`), nil
			case "other":
				return json.RawMessage(`{"poster_path":"different-return-object"}`), nil
			case "raise":
				return nil, errors.New("owned image handler failure")
			case "rate":
				return nil, plugins.ErrModuleRateLimited
			default:
				return nil, nil
			}
		})
	case "module_search_media":
		if len(f.config.MediaResult) == 0 {
			return json.RawMessage(`null`), nil
		}
		return f.config.MediaResult, nil
	case "module_episodes":
		if len(f.config.EpisodeResult) == 0 {
			return json.RawMessage(`null`), nil
		}
		return f.config.EpisodeResult, nil
	case "module_metadata_nfo":
		if len(f.config.NFOResult) == 0 {
			return json.RawMessage(`null`), nil
		}
		return f.config.NFOResult, nil
	case "module_filter":
		return plugins.HandleTitleTorrentFilter(input, func(call *plugins.TitleTorrentFilterInput) (plugins.TitleTorrentFilterReturn, error) {
			switch f.config.FilterMode {
			case "media_mutate_then_fail":
				if call.MediaInfo != nil {
					call.MediaInfo.Fields["title"] = json.RawMessage(`"Changed before error"`)
					call.MediaInfo.Fields["category"] = json.RawMessage(`"Changed category"`)
				}
				if len(call.TorrentList) > 0 {
					call.TorrentList[0].Fields["pri_order"] = json.RawMessage(`77`)
				}
				return plugins.TitleTorrentFilterReturn{}, errors.New("owned media mutation before failure")
			case "same_input_alias", "alias_core_mutation":
				return plugins.TitleTorrentFilterReturn{InputList: true}, nil
			case "copy_equal":
				rows := []*plugins.TitleFilterTorrent{}
				for _, model := range call.TorrentList {
					rows = append(rows, model.Clone())
				}
				return plugins.TitleTorrentFilterReturn{Rows: rows}, nil
			case "copy_container":
				return plugins.TitleTorrentFilterReturn{Rows: append([]*plugins.TitleFilterTorrent{}, call.TorrentList...)}, nil
			case "mutate_then_none", "mutate_then_fail":
				if len(call.TorrentList) > 0 {
					call.TorrentList[0].Fields["pri_order"] = json.RawMessage(`77`)
				}
				if f.config.FilterMode == "mutate_then_fail" {
					return plugins.TitleTorrentFilterReturn{}, errors.New("owned filter mutation then failure")
				}
				return plugins.TitleTorrentFilterReturn{}, nil
			default:
				return plugins.TitleTorrentFilterReturn{Value: f.config.Result}, nil
			}
		})
	case "catalog_event", "transfer_chain_event":
		return plugins.HandleMutableEvent(input, func(event *plugins.MutableEvent) (json.RawMessage, error) {
			var original struct {
				SourceItem struct{ Extension string } `json:"source_item"`
			}
			_ = json.Unmarshal(event.Data, &original)
			if method == "transfer_chain_event" {
				f.TransferTrace = append(f.TransferTrace, event.Type)
			}
			if mutation, present := f.config.EventMutations[event.Type]; present {
				if f.config.EventAppendSources && (event.Type == "recommend.source" || event.Type == "discover.source") {
					var before, after map[string]json.RawMessage
					var existing, extra []json.RawMessage
					if json.Unmarshal(event.Data, &before) != nil || json.Unmarshal(mutation, &after) != nil || before == nil || after == nil || json.Unmarshal(before["extra_sources"], &existing) != nil || json.Unmarshal(after["extra_sources"], &extra) != nil {
						return nil, errors.New("invalid owned source list append")
					}
					before["extra_sources"], _ = json.Marshal(append(existing, extra...))
					event.Data, _ = json.Marshal(before)
				} else {
					event.Data = append(json.RawMessage(nil), mutation...)
				}
			}
			if event.Type == "transfer.rename" && f.config.RenameSourceExtension && original.SourceItem.Extension != "" {
				var fields map[string]json.RawMessage
				var path string
				if json.Unmarshal(event.Data, &fields) != nil || json.Unmarshal(fields["updated_str"], &path) != nil {
					return nil, errors.New("invalid owned rename extension input")
				}
				fields["updated_str"], _ = json.Marshal(strings.TrimSuffix(path, filepath.Ext(path)) + "." + original.SourceItem.Extension)
				event.Data, _ = json.Marshal(fields)
			}
			if f.config.EventFailure {
				return nil, errors.New("owned catalog event failure after mutation")
			}
			return nil, nil
		})
	case "snapshot":
		return json.Marshal(f)
	case "http":
		var request plugins.HTTPRequest
		if json.Unmarshal(input, &request) == nil && request.Key == "feed" && request.Method == "GET" && fixtureCatalogEvents != "" {
			f.Calls["feed"]++
			f.LastFeed, _ = json.Marshal(map[string]any{"method": request.Method, "path": request.Path, "query": request.Query, "pid": f.PID})
			if f.config.FeedFail {
				return json.Marshal(plugins.HTTPResponse{Status: 503, Body: json.RawMessage(`{"success":false,"message":"Owned catalog feed failure"}`)})
			}
			data := f.config.Feed
			if f.config.FeedPages != nil {
				page := plugins.LastQuery(request.Query, "page")
				if page == "" {
					page = "1"
				}
				data = f.config.FeedPages[page]
			}
			if len(data) == 0 {
				data = json.RawMessage(`[]`)
			}
			return json.Marshal(plugins.HTTPResponse{Status: 200, Body: data})
		}
		if json.Unmarshal(input, &request) != nil || request.Key != "snapshot" || request.Method != "GET" {
			return nil, errors.New("invalid owned process snapshot request")
		}
		inputs := map[string]json.RawMessage{}
		for key, value := range f.Inputs {
			if key != "http" {
				inputs[key] = value
			}
		}
		// Owned CLI observation only: use the real existing SDK current-caller
		// metadata/name methods; no namespace override or new capability.
		base := plugins.Base{Host: host}
		metadata, err := base.GetInstanceMetadata(ctx)
		if err != nil {
			return nil, err
		}
		name, err := base.GetName(ctx)
		if err != nil {
			return nil, err
		}
		body, err := json.Marshal(map[string]any{"metadata": metadata, "name": name, "pid": f.PID, "calls": f.Calls, "inputs": inputs, "last_feed": f.LastFeed, "search_keywords": f.SearchKeywords, "transfer_calls": f.TransferCalls, "transfer_events": f.TransferEvents, "transfer_trace": f.TransferTrace, "storage_calls": f.StorageCalls})
		if err != nil {
			return nil, err
		}
		return json.Marshal(plugins.HTTPResponse{Status: 200, Body: body})
	case "get_render_mode":
		return json.RawMessage(`["vuetify", ""]`), nil
	case "get_form":
		return json.RawMessage(`[[], {}]`), nil
	case "get_dashboard":
		return json.RawMessage(`[{},{},[]]`), nil
	case "get_api":
		if fixtureProcessAPI == "" {
			return json.RawMessage(`[]`), nil
		}
		return json.Marshal(manifest().APIs)
	case "get_service", "get_command", "get_page", "get_sidebar_nav":
		return json.RawMessage(`[]`), nil
	case "close", "stop_service", "stop":
		return json.RawMessage(`null`), nil
	default:
		return nil, errors.New("unsupported method")
	}
}

func (f *fixture) configuredTransferResult(input json.RawMessage) (json.RawMessage, error) {
	raw := f.config.Result
	var call struct {
		Kwargs struct{ FileItem struct{ Path string } }
	}
	if json.Unmarshal(input, &call) == nil {
		if selected, ok := f.config.TransferResults[call.Kwargs.FileItem.Path]; ok {
			raw = selected
		}
	}
	if f.config.BuildTransferInfo {
		if len(raw) == 0 {
			raw = json.RawMessage(`{}`)
		}
		return plugins.ModuleTransferInfoResult(raw)
	}
	if len(raw) == 0 {
		return json.RawMessage(`null`), nil
	}
	return raw, nil
}

func main() {
	m := manifest()
	if err := m.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		_ = json.NewEncoder(os.Stdout).Encode(m)
		return
	}
	f := &fixture{Calls: make(map[string]int), Inputs: make(map[string]json.RawMessage), PID: os.Getpid()}
	if err := plugins.Serve(context.Background(), os.Stdin, os.Stdout, m, f.handle); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
