package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"light-media/internal/download"
)

func TestDownloaderAPIKeySettingsProjectionRetentionClearAndBinding(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	request := func(method, path, body string) string {
		t.Helper()
		w := callTest(t, a.Handler(), method, path, body, cookie, "")
		if w.Code != 200 {
			t.Fatal(method, path, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	request("POST", "/api/downloaders/manage", `{"id":"owned-qb","kind":"qbittorrent","name":"Owned qB","base_url":"http://127.0.0.1:19080","username":"owned-user","password":"owned-password","api_key":"owned-downloader-api-secret","auto_category":true,"sequential":true,"first_last_piece":true}`)
	for _, path := range []string{"/api/settings", "/api/downloaders/manage"} {
		body := request("GET", path, "")
		if strings.Contains(body, "owned-downloader-api-secret") || strings.Contains(body, "owned-password") {
			t.Fatal("credential leaked", path)
		}
	}
	body := request("GET", "/api/downloaders/manage", "")
	if !strings.Contains(body, `"api_key":true`) || !strings.Contains(body, `"auto_category":true`) || !strings.Contains(body, `"sequential":true`) || !strings.Contains(body, `"first_last_piece":true`) {
		t.Fatal("metadata/options missing", body)
	}
	var projection struct {
		Data map[string]any `json:"data"`
	}
	if json.Unmarshal([]byte(request("GET", "/api/settings", "")), &projection) != nil {
		t.Fatal("public settings decode")
	}
	raw, _ := json.Marshal(projection.Data)
	request("PUT", "/api/settings", string(raw))
	saved, err := a.configuredDownloader("owned-qb")
	if err != nil || saved.APIKey != "owned-downloader-api-secret" || saved.Password != "owned-password" {
		t.Fatal("unrelated section lost secret", err)
	}
	if strings.Contains(a.clean("failed key owned-downloader-api-secret"), "owned-downloader-api-secret") {
		t.Fatal("log redaction omitted new key")
	}
	var sealed []byte
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='config' AND id='main'`).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sealed), "owned-downloader-api-secret") || strings.Contains(string(sealed), "owned-password") {
		t.Fatal("credential stored outside encryption")
	}
	var persisted Settings
	if err := a.Store.Unseal(sealed, &persisted); err != nil || len(persisted.Downloaders) != 1 || persisted.Downloaders[0].APIKey != "owned-downloader-api-secret" {
		t.Fatal("encrypted key was not recoverable", err)
	}
	request("POST", "/api/downloaders/manage", `{"id":"owned-qb","kind":"qbittorrent","name":"Owned qB","base_url":"http://127.0.0.1:19080","username":"owned-user","clear_api_key":true}`)
	saved, _ = a.configuredDownloader("owned-qb")
	if saved.APIKey != "" || saved.Password != "owned-password" || saved.ClearAPIKey {
		t.Fatal("explicit clear/persisted command incorrect")
	}
	request("POST", "/api/downloaders/manage", `{"id":"owned-qb","kind":"qbittorrent","name":"Owned qB","base_url":"http://127.0.0.1:19080","username":"owned-user","api_key":"owned-replacement-key"}`)
	request("POST", "/api/downloaders/manage", `{"id":"owned-qb","kind":"qbittorrent","name":"Owned qB","base_url":"http://127.0.0.1:19081","username":"owned-user"}`)
	saved, _ = a.configuredDownloader("owned-qb")
	if saved.APIKey != "" || saved.Password != "" {
		t.Fatal("credential crossed daemon endpoint")
	}
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='config' AND id='main'`).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	persisted = Settings{} // JSON omits a cleared key; decode into a fresh config, like startup.
	if err := a.Store.Unseal(sealed, &persisted); err != nil || len(persisted.Downloaders) != 1 || persisted.Downloaders[0].ClearAPIKey || persisted.Downloaders[0].APIKey != "" {
		t.Fatal("transient clear command or old credential persisted", err)
	}
}

func TestDownloaderDestinationSnapshotIncludesAutoManagementButPreservesLegacy(t *testing.T) {
	d := DownloaderConfig{Config: download.Config{ID: "owned-qb", Kind: "qbittorrent", BaseURL: "http://127.0.0.1:19080"}, DownloadPath: "/downloads"}
	raw, _ := json.Marshal(struct {
		Kind, BaseURL, DownloadPath string
		Mappings                    []PathMapping
	}{d.Kind, d.BaseURL, d.DownloadPath, d.Mappings})
	sum := sha256.Sum256(raw)
	legacy := hex.EncodeToString(sum[:])
	if downloaderSnapshot(d) != legacy {
		t.Fatal("default changed existing reviewed destination fingerprints")
	}
	d.Password = "rotation"
	d.APIKey = "rotation"
	d.Sequential = true
	d.FirstLast = true
	if downloaderSnapshot(d) != legacy {
		t.Fatal("non-destination settings invalidated destination identity")
	}
	d.AutoCategory = true
	if downloaderSnapshot(d) == legacy {
		t.Fatal("automatic category management changed destination without snapshot change")
	}
}

func TestDownloaderCredentialPreservationDoesNotCrossKind(t *testing.T) {
	old := DownloaderConfig{Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: "http://127.0.0.1:19080", Username: "u", Password: "p", APIKey: "key"}}
	next := DownloaderConfig{Config: download.Config{ID: "owned", Kind: "transmission", BaseURL: old.BaseURL, Username: old.Username}}
	preserveDownloaderCredentials(&next, old)
	if next.Password != "" || next.APIKey != "" {
		t.Fatal("credentials crossed downloader implementation")
	}
}
