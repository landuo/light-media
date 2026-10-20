package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"light-media/internal/fileextensions"
	"light-media/internal/sites"
)

func ownedCleanExtensionEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range fileextensions.Keys() {
		key := key
		v, had := os.LookupEnv(key)
		if e := os.Unsetenv(key); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			if had {
				_ = os.Setenv(key, v)
			} else {
				_ = os.Unsetenv(key)
			}
		})
	}
}

func TestActualExtensionRecognitionFreezesSelectedValuesAcrossCurrentChange(t *testing.T) {
	ownedCleanExtensionEnvironment(t)
	a, _, _ := importTestApp(t)
	ctx := context.Background()
	a.config.RMTMediaExt = json.RawMessage(`[".owned", ".mp4"]`)
	a.config.RMTSubExt = json.RawMessage(`[".ownedsub"]`)
	a.config.RMTAudioExt = json.RawMessage(`[".ownedaudio"]`)
	options, e := a.globalRecognitionOptions(ctx)
	if e != nil || !reflect.DeepEqual(options.MediaExts, []string{".owned", ".mp4", ".ownedsub", ".ownedaudio"}) {
		t.Fatal("actual global parser ignored configured media extensions", options, e)
	}
	scoped, e := withResourceRecognition(ctx, options)
	if e != nil {
		t.Fatal(e)
	}
	_, hash := workflowTorrent("Owned.S01E01.owned")
	source, e := a.prepareDownloadContext(scoped, "intent", "owned-extension-selection", hash, sites.Resource{Title: "Owned.S01E01.owned"})
	if e != nil {
		t.Fatal(e)
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if e = a.saveDownloadContextTx(ctx, tx, source); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	t.Setenv(fileextensions.MediaKey, `[]`)
	t.Setenv(fileextensions.SubtitleKey, `[]`)
	t.Setenv(fileextensions.AudioKey, `[]`)
	current, e := a.globalRecognitionOptions(ctx)
	if e != nil || current.MediaExts == nil || len(current.MediaExts) != 0 {
		t.Fatal("explicit empty native list was replaced by defaults", current, e)
	}
	loaded, e := a.intentRecognitionContext(ctx, Intent{ID: source.DownloadID, Hash: hash})
	if e != nil || !reflect.DeepEqual(loaded.Value(resourceRecognitionKey{}).(resourceRecognition).options.MediaExts, options.MediaExts) {
		t.Fatal("saved accepted source was reinterpreted with current extensions", e)
	}
	t.Setenv(fileextensions.MediaKey, `null`)
	if _, e = a.globalRecognitionOptions(ctx); !errors.Is(e, fileextensions.ErrSourceValidation) {
		t.Fatal("invalid explicit native extension list silently fell back", e)
	}
	if _, e = a.intentRecognitionContext(ctx, Intent{ID: source.DownloadID, Hash: hash}); e != nil {
		t.Fatal("current invalid setting invalidated immutable selected context", e)
	}
}
func TestActualExtensionSourceReadersKeepNativeAndCapturedOriginsIndependent(t *testing.T) {
	ownedCleanExtensionEnvironment(t)
	a, _, _ := importTestApp(t)
	ctx := context.Background()
	before := map[string][]byte{}
	for _, kind := range []string{"source_statistics_settings", "source_network_settings", "source_runtime_settings", "source_settings_migration"} {
		sealed, e := a.pluginStore.seal(kind, "system", "main", json.RawMessage(`{"owned":"unchanged"}`))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = a.Store.DB.Exec(`INSERT INTO records(kind,id,payload,updated_at) VALUES(?,?,?,'owned')`, kind, "main", sealed); e != nil {
			t.Fatal(e)
		}
		before[kind] = sealed
	}
	native, e := a.sourceFileExtensions(ctx)
	if e != nil || native.Inputs.Namespace != "native.settings" || len(native.Values(fileextensions.MediaKey)) != 18 {
		t.Fatal("native declaration did not use actual source defaults", native, e)
	}
	if _, e = a.capturedSourceFileExtensions(ctx); !errors.Is(e, fileextensions.ErrDependency) {
		t.Fatal("missing original values were inferred from this process", e)
	}
	a.config.RMTMediaExt = json.RawMessage(`[".configuration"]`)
	t.Setenv(fileextensions.MediaKey, `[".native-current"]`)
	native, e = a.sourceFileExtensions(ctx)
	if e != nil || !reflect.DeepEqual(native.Values(fileextensions.MediaKey), []string{".native-current"}) {
		t.Fatal("exact current native env precedence", native, e)
	}
	inputs := fileextensions.Inputs{Namespace: "moviepilot.source", SourceID: "owned-source-observation-original-three-fields-absent", EnvironmentKnown: true, ConfigurationKnown: true, Environment: map[string]string{}, Configuration: map[string]json.RawMessage{}}
	record := struct {
		Version int
		Inputs  fileextensions.Inputs
	}{1, inputs}
	sealed, e := a.pluginStore.seal("source_file_extension_settings", "system", "main", mustCleanSeedJSON(record))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Store.DB.Exec(`INSERT INTO records(kind,id,payload,updated_at) VALUES('source_file_extension_settings','main',?,'owned')`, sealed); e != nil {
		t.Fatal(e)
	}
	original, e := a.capturedSourceFileExtensions(ctx)
	if e != nil || len(original.Values(fileextensions.MediaKey)) != 18 || original.Inputs.Namespace != "moviepilot.source" || original.InputSHA == native.InputSHA {
		t.Fatal("captured original source borrowed current native environment", original, e)
	}
	for kind, want := range before {
		var got []byte
		if e = a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind=? AND id='main'`, kind).Scan(&got); e != nil || !bytes.Equal(got, want) {
			t.Fatal("extension read changed existing captured setting", kind, e)
		}
	}
}
