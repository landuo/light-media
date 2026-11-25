package iyuuautoseed

import (
	"context"
	"errors"

	sdk "light-media/builtinplugins/internal/sdk"
)

func canonicalDownloaderKind(kind string) string {
	switch kind {
	case "qb":
		return "qbittorrent"
	case "tr":
		return "transmission"
	}
	return kind
}

// MoviePilot's get_service returns the first wrapper while get_services keeps
// the last wrapper for each name in its first insertion slot. Config metadata
// in either case comes from the last enabled config, regardless of module.
func (d sourceDownloaderReads) Inventory(ctx context.Context, mode string, selection any) ([]Service, error) {
	if !truth(selection) {
		return nil, nil
	}
	selected, err := sourceDownloaderNames(mode, selection)
	if err != nil || len(selected) == 0 {
		return nil, err
	}
	configs, err := d.client.Configurations(ctx)
	if err != nil {
		return nil, err
	}
	metadata := map[string]string{}
	for _, ref := range configs.Downloaders {
		if ref.Enabled && ref.Kind != "" && ref.Name != "" && selected[ref.Name] {
			metadata[ref.Name] = canonicalDownloaderKind(ref.Kind)
		}
	}
	out := []Service{}
	positions := map[string]int{}
	for _, kind := range []string{"qbittorrent", "transmission"} {
		var names []string
		refs := map[string]string{}
		for _, ref := range configs.Downloaders {
			if !ref.Enabled || metadata[ref.Name] == "" || canonicalDownloaderKind(ref.Kind) != kind {
				continue
			}
			if _, exists := refs[ref.Name]; !exists {
				names = append(names, ref.Name)
			}
			refs[ref.Name] = ref.ID
		}
		for _, name := range names {
			id := refs[name]
			conn, err := d.client.SourceConnection(ctx, sdk.DownloaderConnectionRequestV1{DownloaderID: id})
			if err != nil {
				return nil, err
			}
			if conn.Kind != kind {
				return nil, errors.New("IYUU downloader runtime kind changed")
			}
			if !conn.Exists {
				continue
			}
			service := Service{Ref: id, Name: name, Kind: metadata[name], RuntimeKind: kind, Inactive: conn.Inactive}
			if mode == "auto" {
				return []Service{service}, nil
			}
			if position, exists := positions[name]; exists {
				out[position] = service
			} else {
				positions[name] = len(out)
				out = append(out, service)
			}
		}
	}
	return out, nil
}

func sourceDownloaderNames(mode string, selection any) (map[string]bool, error) {
	names := map[string]bool{}
	switch mode {
	case "auto":
		if name, ok := selection.(string); ok {
			names[name] = true
		}
	case "services":
		// Python set() consumes characters for a string and keys for a dict.
		switch value := selection.(type) {
		case string:
			for _, char := range value {
				names[string(char)] = true
			}
		case map[string]any:
			for name := range value {
				names[name] = true
			}
		case []string:
			for _, name := range value {
				names[name] = true
			}
		case []any:
			for _, item := range value {
				switch name := item.(type) {
				case string:
					names[name] = true
				case []any, map[string]any:
					return nil, sourceFault("TypeError", "downloader name filters")
				}
			}
		default:
			return nil, sourceFault("TypeError", "downloader name filters")
		}
	default:
		return nil, errors.New("invalid IYUU downloader selection mode")
	}
	return names, nil
}
