package app

import (
	"encoding/json"
	"reflect"
	"testing"

	"light-media/internal/download"
)

func TestUnrelatedSettingsSavePreservesLegacyDirectorySnapshots(t *testing.T) {
	a, source, _ := importTestApp(t)
	for _, id := range []string{"q", "t"} {
		a.config.Downloaders = append(a.config.Downloaders, DownloaderConfig{Config: download.Config{ID: id, Kind: "qb", BaseURL: "http://127.0.0.1:1"}, DownloadPath: "/downloads", Mappings: []PathMapping{{From: "/downloads", To: source}}})
		a.config.Directories = append(a.config.Directories, DirectoryRule{ID: id, Name: id, DownloaderID: id, DownloadPath: "/downloads", RootID: "root", MediaType: "movie", Mode: "hardlink", MonitorType: "none"})
	}
	old := a.Settings().Directories
	projection := a.publicSettings()
	delete(projection, "secret_state")
	raw, e := json.Marshal(projection)
	if e != nil {
		t.Fatal(e)
	}
	var incoming Settings
	if e = json.Unmarshal(raw, &incoming); e != nil {
		t.Fatal(e)
	}
	incoming.LogLevel = "WARN"
	a.preserveSecrets(&incoming)
	if !reflect.DeepEqual(incoming.Directories, old) {
		t.Fatalf("unrelated settings changed legacy directory ownership: %+v", incoming.Directories)
	}
	for i := range old {
		if directoryFingerprint(incoming.Directories[i]) != directoryFingerprint(old[i]) {
			t.Fatal("unrelated settings invalidated existing download plan")
		}
	}
}
