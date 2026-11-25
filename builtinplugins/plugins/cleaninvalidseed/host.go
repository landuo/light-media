package cleaninvalidseed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strings"

	sdk "light-media/builtinplugins/internal/sdk"
)

const (
	tasksCapability    = "clean_seed.tasks"
	trackersCapability = "clean_seed.trackers"
	controlCapability  = "clean_seed.control"
	filesCapability    = "clean_seed.files"
	removeCapability   = "clean_seed.remove"
)

type downloaderRequest struct {
	DownloaderID string `json:"downloader_id"`
	Hash         string `json:"hash,omitempty"`
}

type controlRequest struct {
	downloaderRequest
	Action       string   `json:"action"`
	Tags         []string `json:"tags"`
	OriginalTags []string `json:"original_tags,omitempty"`
}

type scanFile struct {
	Path  string  `json:"path"`
	Kind  string  `json:"kind"`
	Size  int64   `json:"size"`
	MTime float64 `json:"mtime"`
}

type fileScan struct {
	SchemaVersion int        `json:"schema_version"`
	Exists        bool       `json:"exists"`
	Files         []scanFile `json:"files"`
}

type scanKey struct {
	path      string
	recursive bool
}

type liveHost struct {
	ctx       context.Context
	caller    sdk.HostCaller
	config    map[string]any
	services  map[string]string
	torrents  map[*Object]string
	labels    map[downloaderRequest][]string
	files     map[string]scanFile
	fileScans map[scanKey]fileScan
	failure   error
}

func newLiveHost(ctx context.Context, caller sdk.HostCaller) *liveHost {
	return &liveHost{ctx: ctx, caller: caller,
		services: map[string]string{}, torrents: map[*Object]string{}, labels: map[downloaderRequest][]string{},
		files: map[string]scanFile{}, fileScans: map[scanKey]fileScan{},
	}
}

func (h *liveHost) call(ctx context.Context, capability string, request, out any) error {
	if h.caller == nil {
		return errors.New("CleanInvalidSeed requires an active host caller")
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return err
	}
	raw, err = h.caller(ctx, capability, raw)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("invalid CleanInvalidSeed host response")
	}
	return nil
}

func (h *liveHost) rememberFailure(err error) {
	if err != nil && h.failure == nil {
		h.failure = err
	}
}

func (h *liveHost) Services(ctx context.Context, selected any) ([]Service, error) {
	names, err := listValue(selected)
	if err != nil {
		return nil, err
	}
	selectedNames := make(map[string]bool, len(names))
	for _, raw := range names {
		name, err := stringValue(raw, "downloader name")
		if err != nil {
			return nil, err
		}
		selectedNames[name] = true
	}
	client := sdk.Downloaders{Host: h.caller}
	configs, err := client.Configurations(ctx)
	if err != nil {
		return nil, err
	}
	services := []Service{}
	for _, config := range configs.Downloaders {
		if !selectedNames[config.Name] {
			continue
		}
		delete(selectedNames, config.Name)
		connection, err := client.SourceConnection(ctx, sdk.DownloaderConnectionRequestV1{DownloaderID: config.ID})
		if err != nil {
			return nil, err
		}
		h.services[config.Name] = config.ID
		services = append(services, Service{Name: config.Name, Kind: connection.Kind, Inactive: !config.Enabled || !connection.Exists || connection.Inactive})
	}
	return services, nil
}

func (h *liveHost) Torrents(ctx context.Context, service Service) ([]*Object, bool, error) {
	id := h.services[service.Name]
	var response struct {
		SchemaVersion int              `json:"schema_version"`
		Kind          string           `json:"kind"`
		NativeError   bool             `json:"native_error"`
		Tasks         []map[string]any `json:"tasks"`
	}
	if err := h.call(ctx, tasksCapability, downloaderRequest{DownloaderID: id}, &response); err != nil {
		h.rememberFailure(err)
		return nil, true, err
	}
	if response.SchemaVersion != 1 || response.Kind != service.Kind || response.Tasks == nil || response.NativeError {
		err := errors.New("CleanInvalidSeed could not observe the selected downloader task list")
		h.rememberFailure(err)
		return nil, true, err
	}
	torrents := make([]*Object, 0, len(response.Tasks))
	for _, fields := range response.Tasks {
		var torrent *Object
		var err error
		switch service.Kind {
		case "qbittorrent":
			torrent = nativeQB(fields)
		case "transmission":
			torrent, err = nativeTR(fields)
		}
		if torrent == nil || err != nil {
			err := errors.New("invalid CleanInvalidSeed task projection")
			h.rememberFailure(err)
			return nil, true, err
		}
		hash, err := Hash(torrent, service.Kind)
		exact, _ := hash.(string)
		if err != nil || !sdk.DownloaderTaskHashValid(exact) {
			err := errors.New("invalid CleanInvalidSeed task identity")
			h.rememberFailure(err)
			return nil, true, err
		}
		h.torrents[torrent] = id
		if service.Kind == "transmission" {
			labels, err := stringsFromValues(fields["labels"])
			if err != nil {
				h.rememberFailure(err)
				return nil, true, err
			}
			h.labels[downloaderRequest{id, exact}] = labels
		}
		torrents = append(torrents, torrent)
	}
	return torrents, false, nil
}

func (h *liveHost) TrackerObservations(torrent *Object, kind string) ([]any, error) {
	hash, err := Hash(torrent, kind)
	if err != nil {
		h.rememberFailure(err)
		return nil, err
	}
	id := h.torrents[torrent]
	exact, _ := hash.(string)
	var response struct {
		SchemaVersion int                  `json:"schema_version"`
		Trackers      []trackerObservation `json:"trackers"`
	}
	if err := h.call(h.ctx, trackersCapability, downloaderRequest{id, exact}, &response); err != nil {
		h.rememberFailure(err)
		return nil, err
	}
	if response.SchemaVersion != 1 || response.Trackers == nil {
		err := errors.New("invalid CleanInvalidSeed tracker projection")
		h.rememberFailure(err)
		return nil, err
	}
	rows := make([]any, 0, len(response.Trackers))
	for _, tracker := range response.Trackers {
		if tracker.Valid && tracker.Key == "" || tracker.ExplicitError && !eq(tracker.Status, 4) {
			err := errors.New("invalid CleanInvalidSeed tracker observation")
			h.rememberFailure(err)
			return nil, err
		}
		rows = append(rows, tracker)
	}
	if kind == "transmission" && Truth(h.config["more_logs"]) {
		value, _ := attr(torrent, "name", "Unknown")
		name := diagnosticText(value)
		h.Log(h.ctx, "debug", fmt.Sprintf("种子 [%s] Transmission tracker信息: %d 个", name, len(response.Trackers)))
		for i, tracker := range response.Trackers {
			h.Log(h.ctx, "debug", fmt.Sprintf("种子 [%s] tracker[%d] 错误检查: status=%s, tier=%s, last_announce_result=%q, is_error_msg=%s",
				name, i, str(tracker.Status), str(tracker.Tier), diagnosticText(tracker.Message), str(tracker.ExplicitError)))
		}
	}
	return rows, nil
}

func stringsFromValues(value any) ([]string, error) {
	if value == nil {
		return []string{}, nil
	}
	values, err := listValue(value)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		label, err := stringValue(value, "downloader label")
		if err != nil {
			return nil, err
		}
		out = append(out, label)
	}
	return out, nil
}

func (h *liveHost) SetTags(ctx context.Context, service Service, hash any, _ []any) (any, error) {
	exact, _ := hash.(string)
	label, ok := h.config["label"].(string)
	if !ok && h.config["label"] != nil {
		return nil, source("TypeError", "downloader label")
	}
	if label == "" {
		label = "无效做种"
	}
	request := downloaderRequest{h.services[service.Name], exact}
	return h.control(ctx, controlRequest{downloaderRequest: request, Action: "label", Tags: []string{label}, OriginalTags: h.labels[request]})
}

func (h *liveHost) DeleteTorrent(ctx context.Context, service Service, hash any, deleteFiles bool) (any, error) {
	if deleteFiles {
		return nil, errors.New("CleanInvalidSeed may remove torrents only while keeping data")
	}
	exact, _ := hash.(string)
	request := downloaderRequest{h.services[service.Name], exact}
	return h.control(ctx, controlRequest{downloaderRequest: request, Action: "remove_task"})
}

func (h *liveHost) control(ctx context.Context, request controlRequest) (bool, error) {
	var applied *bool
	if err := h.call(ctx, controlCapability, request, &applied); err != nil {
		return false, err
	}
	if applied == nil {
		return false, errors.New("invalid CleanInvalidSeed downloader mutation outcome")
	}
	if !*applied {
		return false, errors.New("CleanInvalidSeed could not confirm the downloader mutation")
	}
	return true, nil
}

func (h *liveHost) BeginFileScan(ctx context.Context) error {
	var reset bool
	request := struct {
		Reset bool `json:"reset"`
	}{true}
	if err := h.call(ctx, filesCapability, request, &reset); err != nil {
		return err
	}
	if !reset {
		return errors.New("CleanInvalidSeed could not begin a fresh local file scan")
	}
	clear(h.files)
	clear(h.fileScans)
	return nil
}

func (h *liveHost) scan(path string, recursive bool) (fileScan, error) {
	key := scanKey{path, recursive}
	if existing, ok := h.fileScans[key]; ok {
		return existing, nil
	}
	var view fileScan
	request := struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
	}{path, recursive}
	if err := h.call(h.ctx, filesCapability, request, &view); err != nil {
		h.rememberFailure(err)
		return fileScan{}, err
	}
	invalid := errors.New("invalid CleanInvalidSeed local file scan")
	if view.SchemaVersion != 1 || view.Files == nil || view.Exists != (len(view.Files) > 0) ||
		view.Exists && view.Files[0].Path != path {
		h.rememberFailure(invalid)
		return fileScan{}, invalid
	}
	seen := make(map[string]bool, len(view.Files))
	for i, file := range view.Files {
		relative, err := filepath.Rel(path, file.Path)
		if err != nil || !filepath.IsLocal(relative) || i > 0 && (!recursive && filepath.Dir(relative) != "." || view.Files[0].Kind != "dir") ||
			file.Kind != "file" && file.Kind != "dir" || file.Size < 0 || math.IsNaN(file.MTime) || math.IsInf(file.MTime, 0) || seen[file.Path] {
			h.rememberFailure(invalid)
			return fileScan{}, invalid
		}
		seen[file.Path] = true
	}
	for _, file := range view.Files {
		h.files[file.Path] = file
	}
	h.fileScans[key] = view
	return view, nil
}

func (h *liveHost) file(path string) (scanFile, bool, error) {
	if file, ok := h.files[path]; ok {
		return file, true, nil
	}
	view, err := h.scan(path, false)
	if err != nil || !view.Exists {
		return scanFile{}, false, err
	}
	return view.Files[0], true, nil
}

func (h *liveHost) Exists(path string) bool {
	_, found, _ := h.file(path)
	return found
}

func (h *liveHost) IsFile(path string) bool {
	file, found, _ := h.file(path)
	return found && file.Kind == "file"
}

func (h *liveHost) IsDir(path string) bool {
	file, found, _ := h.file(path)
	return found && file.Kind == "dir"
}

func (h *liveHost) Stat(path string) (FileStat, error) {
	file, found, err := h.file(path)
	if err != nil {
		return FileStat{}, err
	}
	if !found {
		return FileStat{}, source("FileNotFoundError", "local file stat")
	}
	return FileStat{Size: file.Size, MTime: file.MTime}, nil
}

func (h *liveHost) children(path string, recursive bool) ([]string, error) {
	view, err := h.scan(path, recursive)
	if err != nil {
		return nil, err
	}
	if !view.Exists {
		return nil, source("FileNotFoundError", "local file scan")
	}
	if view.Files[0].Kind != "dir" {
		return nil, source("NotADirectoryError", "local file scan")
	}
	children := make([]string, 0, len(view.Files)-1)
	for _, file := range view.Files[1:] {
		children = append(children, file.Path)
	}
	return children, nil
}

func (h *liveHost) Children(path string) ([]string, error) {
	return h.children(path, false)
}

func (h *liveHost) Descendants(path string) ([]string, error) {
	return h.children(path, true)
}

func (h *liveHost) Unlink(path string) error     { return h.remove(path, "file") }
func (h *liveHost) RemoveTree(path string) error { return h.remove(path, "tree") }

func (h *liveHost) remove(path, kind string) error {
	if h.failure != nil {
		return h.failure
	}
	request := struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
	}{path, kind}
	var removed bool
	if err := h.call(h.ctx, removeCapability, request, &removed); err != nil {
		return err
	}
	if !removed {
		return errors.New("CleanInvalidSeed could not confirm local deletion")
	}
	delete(h.files, path)
	return nil
}

func (h *liveHost) Notice(ctx context.Context, fields map[string]any) error {
	if fields["mtype"] == "SiteMessage" {
		fields["mtype"] = "站点"
	}
	_, err := (sdk.Base{Host: h.caller}).PostMessage(ctx, fields)
	return err
}

func (h *liveHost) SaveConfig(ctx context.Context, config map[string]any) error {
	_, err := (sdk.Base{Host: h.caller}).UpdateConfig(ctx, config)
	return err
}

func (h *liveHost) Log(ctx context.Context, level, message string) {
	if len(message) > sdk.LogMessageLimit {
		message = strings.ToValidUTF8(message[:sdk.LogMessageLimit], "")
	}
	_ = (sdk.Logger{Host: h.caller}).Log(ctx, sdk.LogLevel(strings.ToUpper(level)), message)
}
