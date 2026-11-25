package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

// Source outcomes are explicitly negotiated resources. The ordinary downloader
// resources keep returning host errors for native failures. These four clients
// expose only the pinned wrappers' ordinary error/false outcomes, never guard,
// identity, configuration or cancellation failures.
const (
	DownloaderSourceTasksCapability     = "downloader.source.tasks"
	DownloaderSourceCompletedCapability = "downloader.source.completed"
	DownloaderSourceAddCapability       = "downloader.source.add"
	DownloaderSourceControlCapability   = "downloader.source.control"
)

// DownloaderSourceAddRequestV1 carries the fixed QB vendor's skip_checking
// multipart text and repeated labels. Normal Add remains unchanged. The
// business adapter owns Python repr normalization; the host preserves text.
type DownloaderSourceAddRequestV1 struct {
	DownloaderAddRequestV1
	SkipCheckingWire *string  `json:"skip_checking_wire,omitempty"`
	QBLabels         []string `json:"qb_labels,omitempty"`
}

func (r DownloaderSourceAddRequestV1) Validate() error {
	if err := r.DownloaderAddRequestV1.Validate(); err != nil {
		return err
	}
	if r.SkipCheckingWire != nil && (r.SkipChecking != nil || !utf8.ValidString(*r.SkipCheckingWire) || len(*r.SkipCheckingWire) > MaxPayloadBytes) {
		return errors.New("invalid or conflicting source skip-checking text")
	}
	for _, label := range r.QBLabels {
		if !utf8.ValidString(label) || len(label) > MaxPayloadBytes {
			return errors.New("invalid source QB label text")
		}
	}
	if len(r.Torrent) > MaxPayloadBytes {
		return errors.New("source add request exceeds shared transport limit")
	}
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > MaxPayloadBytes {
		return errors.New("source add request exceeds shared transport limit")
	}
	return nil
}

type DownloaderSourceTasksResultV1 struct {
	DownloaderTasksV1
	NativeError bool `json:"native_error"`
}

type DownloaderSourceMutationResultV1 struct {
	DownloaderMutationV1
	NativeError bool `json:"native_error"`
}

func (d Downloaders) SourceTasks(ctx context.Context, request DownloaderTasksRequestV1) (DownloaderSourceTasksResultV1, error) {
	selected, err := request.CanonicalSelection()
	if err != nil {
		return DownloaderSourceTasksResultV1{}, err
	}
	return d.sourceTasks(ctx, DownloaderSourceTasksCapability, request, request.DownloaderID, selected)
}

func (d Downloaders) SourceCompleted(ctx context.Context, request DownloaderCompletedRequestV1) (DownloaderSourceTasksResultV1, error) {
	if err := request.Validate(); err != nil {
		return DownloaderSourceTasksResultV1{}, err
	}
	return d.sourceTasks(ctx, DownloaderSourceCompletedCapability, request, request.DownloaderID, request.Hashes)
}

type DownloaderSourceCompletedRequestV1 struct {
	DownloaderCompletedRequestV1
	IncludeQBPackFields bool `json:"include_qb_pack_fields,omitempty"`
}

// SourceCompletedWithQBPackFields retains qBittorrent's native completed and
// availability values. The ordinary projection stays stable for older workers.
func (d Downloaders) SourceCompletedWithQBPackFields(ctx context.Context, request DownloaderCompletedRequestV1) (DownloaderSourceTasksResultV1, error) {
	if err := request.Validate(); err != nil {
		return DownloaderSourceTasksResultV1{}, err
	}
	return d.sourceTasks(ctx, DownloaderSourceCompletedCapability, DownloaderSourceCompletedRequestV1{
		DownloaderCompletedRequestV1: request,
		IncludeQBPackFields:          true,
	}, request.DownloaderID, request.Hashes)
}

func (d Downloaders) sourceTasks(ctx context.Context, capability string, request any, id string, selected []string) (DownloaderSourceTasksResultV1, error) {
	var wire struct {
		DownloaderTasksV1
		NativeError *bool `json:"native_error"`
	}
	if err := d.hostRequest(ctx, capability, request, &wire); err != nil {
		return DownloaderSourceTasksResultV1{}, err
	}
	out := DownloaderSourceTasksResultV1{DownloaderTasksV1: wire.DownloaderTasksV1}
	if wire.NativeError == nil || out.SchemaVersion != 1 || out.DownloaderID != id || out.Tasks == nil || out.Kind != "qbittorrent" && out.Kind != "transmission" {
		return DownloaderSourceTasksResultV1{}, errors.New("invalid source downloader task response")
	}
	out.NativeError = *wire.NativeError
	if out.NativeError && len(out.Tasks) != 0 {
		return DownloaderSourceTasksResultV1{}, errors.New("native query failure must have empty tasks")
	}
	allowed := map[string]bool{}
	for _, hash := range selected {
		allowed[strings.ToLower(hash)] = true
	}
	seen := map[string]bool{}
	for _, task := range out.Tasks {
		if !DownloaderTaskHashValid(task.Hash) || seen[task.Hash] || selected != nil && !allowed[task.Hash] || out.Kind == "qbittorrent" && (task.QBittorrent == nil || task.Transmission != nil) || out.Kind == "transmission" && (task.Transmission == nil || task.QBittorrent != nil) {
			return DownloaderSourceTasksResultV1{}, errors.New("invalid source downloader task projection")
		}
		seen[task.Hash] = true
	}
	return out, nil
}

func (d Downloaders) SourceAdd(ctx context.Context, request DownloaderSourceAddRequestV1) (DownloaderSourceMutationResultV1, error) {
	if err := request.Validate(); err != nil {
		return DownloaderSourceMutationResultV1{}, err
	}
	return d.sourceMutation(ctx, DownloaderSourceAddCapability, request, request.DownloaderID)
}

func (d Downloaders) SourceControl(ctx context.Context, request DownloaderControlRequestV1) (DownloaderSourceMutationResultV1, error) {
	if err := request.Validate(); err != nil {
		return DownloaderSourceMutationResultV1{}, err
	}
	return d.sourceMutation(ctx, DownloaderSourceControlCapability, request, request.DownloaderID)
}

func (d Downloaders) sourceMutation(ctx context.Context, capability string, request any, id string) (DownloaderSourceMutationResultV1, error) {
	var wire struct {
		DownloaderMutationV1
		NativeError *bool `json:"native_error"`
	}
	if err := d.hostRequest(ctx, capability, request, &wire); err != nil {
		return DownloaderSourceMutationResultV1{}, err
	}
	if wire.NativeError == nil {
		return DownloaderSourceMutationResultV1{}, errors.New("source downloader outcome requires an explicit native error flag")
	}
	out, err := validateDownloaderMutation(id, wire.DownloaderMutationV1, nil)
	if err != nil {
		return DownloaderSourceMutationResultV1{}, err
	}
	return DownloaderSourceMutationResultV1{DownloaderMutationV1: out, NativeError: *wire.NativeError}, nil
}
