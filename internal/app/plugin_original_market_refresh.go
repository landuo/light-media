package app

import (
	"context"
	"time"
)

// MoviePilot's 30-minute scheduled job refreshes metadata only. It does not
// install updates or replace running instances.
func (a *App) runOriginalMarketRefresh(ctx context.Context) {
	ticker := time.NewTicker(originalMarketTTL)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.originalMarketDeclarations(ctx, true)
		}
	}
}
