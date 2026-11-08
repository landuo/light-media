package app

import (
	"context"

	"light-media/internal/sites"
)

func legacyChatDownloadTorrent(ctx context.Context, client *sites.Client, resource string) ([]byte, error) {
	material, err := client.ChatDownloadMaterial(ctx, resource)
	if err != nil {
		return nil, err
	}
	// A legacy intent admitted as a torrent cannot change to a magnet during
	// confirmation. An identical info hash does not prove equivalent bytes.
	if material.Magnet != "" {
		return nil, bad("REVIEW_MATERIAL_CHANGED", "确认的种子材料已改为磁力链接，请重新预览")
	}
	return material.Torrent, nil
}
