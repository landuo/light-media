package app

import (
	"context"

	"light-media/internal/plugins/downloadercommands"
	native "light-media/internal/plugins/iyuuautoseed/native"
)

func newOriginalDownloaderCommandNative(configured DownloaderConfig, guard func(context.Context) error) (downloadercommands.QBC, downloadercommands.TRC, func(), error) {
	return native.NewSharedDownloaderCommands(native.Config{ID: configured.ID, Kind: configured.Kind, BaseURL: configured.BaseURL, Username: configured.Username, Password: configured.Password}, configured.APIKey, guard)
}
