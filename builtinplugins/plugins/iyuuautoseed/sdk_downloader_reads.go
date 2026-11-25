package iyuuautoseed

import (
	"context"
	"encoding/json"
	"errors"

	sdk "light-media/builtinplugins/internal/sdk"
)

// sourceDownloaderReads keeps the qBittorrent and Transmission wrapper field
// names consumed by the original selection and recheck logic.
type sourceDownloaderReads struct{ client sdk.Downloaders }

func (s Service) actualKind() string {
	if s.RuntimeKind != "" {
		return s.RuntimeKind
	}
	return s.Kind
}

func (d sourceDownloaderReads) Completed(ctx context.Context, service Service) ([]NativeTorrent, error) {
	request := sdk.DownloaderCompletedRequestV1{DownloaderID: service.Ref}
	if service.actualKind() == "qbittorrent" {
		reply, err := d.client.SourceCompletedWithQBPackFields(ctx, request)
		return projectSourceTasks(service, reply, err)
	}
	reply, err := d.client.SourceCompleted(ctx, request)
	return projectSourceTasks(service, reply, err)
}

func (d sourceDownloaderReads) Torrents(ctx context.Context, service Service, hashes []string) ([]NativeTorrent, error) {
	reply, err := d.client.SourceTasks(ctx, sdk.DownloaderTasksRequestV1{DownloaderID: service.Ref, Hashes: hashes})
	return projectSourceTasks(service, reply, err)
}

func projectSourceTasks(service Service, reply sdk.DownloaderSourceTasksResultV1, err error) ([]NativeTorrent, error) {
	if err != nil {
		return nil, err
	}
	if reply.Kind != service.actualKind() {
		return nil, errors.New("IYUU downloader kind changed")
	}
	// Original native query failures become an empty task list. In particular,
	// IYUU ignores the wrapper's error flag when it reconciles the recheck queue.
	out := make([]NativeTorrent, 0, len(reply.Tasks))
	for _, task := range reply.Tasks {
		var fields map[string]any
		if qb := task.QBittorrent; qb != nil {
			raw, err := json.Marshal(qb)
			if err != nil {
				return nil, err
			}
			value, err := decodeJSON(raw)
			if err != nil {
				return nil, err
			}
			fields = value.(map[string]any)
			fields["hash"] = task.Hash
		} else {
			tr := task.Transmission
			fields = map[string]any{"hashString": task.Hash}
			if tr.DownloadDir != nil {
				fields["download_dir"] = *tr.DownloadDir
			}
			if tr.TotalSize != nil {
				fields["total_size"] = *tr.TotalSize
			}
			if tr.PercentDone != nil {
				fields["percent_done"] = *tr.PercentDone
			}
			if tr.Status != nil {
				fields["status_stopped"] = zero(*tr.Status)
			}
			if tr.Labels != nil {
				labels := make([]any, 0, len(*tr.Labels))
				for _, label := range *tr.Labels {
					labels = append(labels, label)
				}
				fields["labels"] = labels
			}
		}
		out = append(out, NativeTorrent{Fields: fields})
	}
	return out, nil
}
