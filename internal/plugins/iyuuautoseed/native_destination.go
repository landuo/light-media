package iyuuautoseed

import (
	"context"
	"errors"
	"path"

	download "light-media/internal/plugins/iyuuautoseed/native"
)

func (h *NativeHost) verifyDaemonDestination(ctx context.Context, entry *nativeHostService, hash string, expected download.IYUUDestination) error {
	var observed struct {
		Path, Category string
		AutoTMM        bool
	}
	err := h.run.step(ctx, "native-add-destination-observed", struct {
		Ref, Hash string
		Expected  download.IYUUDestination
	}{entry.binding.Service.Ref, hash, expected}, false, &observed, func() (any, string, error) {
		raw, err := entry.client.Torrents(ctx, []string{hash}, false)
		if err != nil {
			return nil, "read", err
		}
		data, err := decodeJSON(raw)
		if err != nil {
			return nil, "read", err
		}
		rows, ok := data.([]any)
		if !ok || len(rows) != 1 {
			return nil, "read", errors.New("IYUU added task destination cannot be observed")
		}
		row, ok := rows[0].(map[string]any)
		if !ok {
			return nil, "read", errors.New("IYUU added task destination is invalid")
		}
		idKey, pathKey := "hash", "save_path"
		if expected.Kind == "transmission" {
			idKey, pathKey = "hashString", "download_dir"
		}
		id, _ := row[idKey].(string)
		if id != hash {
			return nil, "read", errors.New("IYUU added task identity differs")
		}
		observed.Path, _ = row[pathKey].(string)
		observed.Category, _ = row["category"].(string)
		observed.AutoTMM, _ = row["auto_tmm"].(bool)
		return observed, "read", nil
	})
	if err != nil {
		return err
	}
	// The read itself is durable evidence even when it proves a mismatch. The
	// confirmed add remains recorded and is never replayed to repair a path.
	if observed.Path == "" || path.Clean(observed.Path) != expected.EffectivePath || (expected.Kind == "qbittorrent" && (observed.AutoTMM != expected.AutoTMM || observed.Category != expected.Category)) {
		return errors.New("IYUU daemon task destination changed; recorded addition requires reconciliation")
	}
	return nil
}
