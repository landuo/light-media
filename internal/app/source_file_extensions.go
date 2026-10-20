package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"

	"light-media/internal/fileextensions"
)

// Native inputs and original captured inputs are separate namespaces. Reading
// current native defaults never writes or blesses a migrated original setting.
func (a *App) sourceFileExtensions(ctx context.Context) (fileextensions.Snapshot, error) {
	if e := ctx.Err(); e != nil {
		return fileextensions.Snapshot{}, e
	}
	s := a.Settings()
	in := fileextensions.Inputs{Namespace: "native.settings", SourceID: "current", EnvironmentKnown: true, ConfigurationKnown: true, Environment: map[string]string{}, Configuration: map[string]json.RawMessage{}}
	raw := map[string]json.RawMessage{fileextensions.MediaKey: s.RMTMediaExt, fileextensions.SubtitleKey: s.RMTSubExt, fileextensions.AudioKey: s.RMTAudioExt}
	for _, key := range fileextensions.Keys() {
		if len(raw[key]) > 0 {
			in.Configuration[key] = append(json.RawMessage(nil), raw[key]...)
		}
		if value, present := os.LookupEnv(key); present {
			in.Environment[key] = value
		}
	}
	return fileextensions.Resolve(in)
}

// The original-source reader requires its own authenticated, fully observed
// three-field record. Existing statistics/network/runtime/migration records are
// never rewritten, nor used as evidence of missing extension fields.
func (a *App) capturedSourceFileExtensions(ctx context.Context) (fileextensions.Snapshot, error) {
	var sealed []byte
	e := a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='source_file_extension_settings' AND id='main'`).Scan(&sealed)
	if errors.Is(e, sql.ErrNoRows) {
		return fileextensions.Snapshot{}, fileextensions.ErrDependency
	}
	if e != nil {
		return fileextensions.Snapshot{}, e
	}
	raw, e := a.pluginStore.unseal("source_file_extension_settings", "system", "main", sealed)
	if e != nil {
		return fileextensions.Snapshot{}, e
	}
	var record struct {
		Version int
		Inputs  fileextensions.Inputs
	}
	if cleanupJournalStrict(raw, &record) != nil || record.Version != 1 || record.Inputs.Namespace != "moviepilot.source" {
		return fileextensions.Snapshot{}, fileextensions.ErrDependency
	}
	return fileextensions.Resolve(record.Inputs)
}
