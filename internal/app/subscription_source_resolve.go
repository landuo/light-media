package app

import (
	"context"
	"time"

	"light-media/internal/catalog"
)

// MoviePilot resolves douban/bangumi/anilist identities through its media
// chain and stores the TMDB media. The local provider path keeps the source id
// when the plugin already understands it, so a subscription created from one
// of those sources only becomes a TMDB subscription when the resolution is
// unambiguous. Failures stay on the provider-native path instead of blocking
// the creation.
func (a *App) resolveSubscriptionAltIdentity(ctx context.Context, source, kind, id string) (catalog.Media, bool) {
	if source != "douban" && source != "bangumi" && source != "anilist" {
		return catalog.Media{}, false
	}
	if kind != "movie" && kind != "tv" {
		return catalog.Media{}, false
	}
	transport, err := a.metadataAPITransportForConfig()
	if err != nil {
		return catalog.Media{}, false
	}
	explorer := catalog.NewExplorer(a.catalog(), transport)
	defer explorer.CloseIdleConnections()
	resolveCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	resolved, err := explorer.Resolve(resolveCtx, source, kind, id)
	if err != nil || resolved.Media == nil || !validMedia(kind, resolved.Media.ID) {
		return catalog.Media{}, false
	}
	return *resolved.Media, true
}
