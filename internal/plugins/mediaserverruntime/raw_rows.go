package mediaserverruntime

import (
	"context"
	"encoding/json"
	"errors"

	"light-media/internal/plugins"
)

// RawConfig reads the current established private MediaServers fact. The host
// adapter must return its owned JSON copy after releasing store/cache locks.
// An unestablished legacy origin, guard failure, or persistence error stays an
// error: this adapter does not turn it into null, empty rows, or source success.
type RawConfig func(context.Context) (json.RawMessage, error)

// ConfigReport observes the leaf's existing credential-free diagnostics. It
// must not retain host locks or convert them into public config.updated values.
type ConfigReport func(plugins.MediaServerConfigDiagnostic)

// NewRawConfigRows connects the exact ServiceConfigHelper validated-list layer
// to the six-module lifecycle, not to ServiceBaseHelper's name projection.
// Each invocation reads current private raw data and returns newly owned rows;
// there is no cache, deduplication, vendor construction, event-value input or
// registry fallback here. The caller still supplies every concrete factory.
func NewRawConfigRows(read RawConfig, report ConfigReport) (ConfigRows, error) {
	if read == nil || report == nil {
		return nil, errors.New("media configuration rows require private reader and diagnostic adapter")
	}
	return func(ctx context.Context) ([]plugins.MediaServerConfigEntry, error) {
		if err := contextOK(ctx); err != nil {
			return nil, err
		}
		raw, err := read(ctx)
		if err != nil {
			return nil, err
		}
		if err := contextOK(ctx); err != nil {
			return nil, err
		}
		parsed, err := plugins.ParseMediaServerConfigRows(raw)
		for _, diagnostic := range parsed.Diagnostics {
			report(diagnostic)
		}
		if err != nil {
			// Only the exact leaf's explicit ordinary exceptions cross into the
			// module manager's source catch. Do not errors.As through unknown or
			// joined errors, or classify private-reader errors by message text.
			if source, ok := err.(*plugins.MediaServerConfigSourceError); ok && source != nil {
				switch source.Class {
				case "TypeError", "OverflowError":
					return nil, &SourceError{Class: source.Class, Cause: source}
				}
			}
			return nil, err
		}
		return parsed.Entries, nil
	}, nil
}
