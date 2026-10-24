package app

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"sync/atomic"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/sites"
)

const ownedBrushInventoryLargeSourceID int64 = 9007199254740993

// This boundary fixture changes only the AUTOINCREMENT sequence in this owned
// temporary App database. It does not insert/update an identity, forge its MAC,
// inject a resource result or import a migration binding. The next ordinary
// saveSettings call must allocate the ID and authenticate it through core code.
func ownedBrushInventorySeedSequenceBoundary(t *testing.T, a *App, previousID int64) {
	t.Helper()
	ctx := context.Background()
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var identities, sequences int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM native_site_identities`).Scan(&identities); err != nil || identities != 0 {
		t.Fatalf("large-ID fixture requires an empty owned identity table: %d %v", identities, err)
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_sequence WHERE name='native_site_identities'`).Scan(&sequences); err != nil {
		t.Fatal(err)
	}
	switch sequences {
	case 0:
		_, err = tx.ExecContext(ctx, `INSERT INTO sqlite_sequence(name,seq) VALUES('native_site_identities',?)`, previousID)
	case 1:
		_, err = tx.ExecContext(ctx, `UPDATE sqlite_sequence SET seq=? WHERE name='native_site_identities'`, previousID)
	default:
		t.Fatal("owned SQLite sequence has duplicate rows")
	}
	if err != nil {
		t.Fatal(err)
	}
	var actual int64
	if err = tx.QueryRowContext(ctx, `SELECT seq FROM sqlite_sequence WHERE name='native_site_identities'`).Scan(&actual); err != nil || actual != previousID {
		t.Fatal("owned sequence did not retain its exact integer boundary", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// callOriginalMethod returns the actual worker's wire JSON. The existing owned
// worker calls the production HostCaller, decodes plugins.BrushInventoryV1 with
// its int64 SourceID, then marshals its typed snapshot. Inspect both typed int64
// and RawMessage tokens here: neither verification uses map[string]any or a
// floating-point intermediate, which would hide the precise regression.
func ownedBrushInventoryReadExactLargeID(t *testing.T, object *originalPluginInstance, want int64) ownedBrushInventorySDKSnapshot {
	t.Helper()
	result, err := object.callOriginalMethod(context.Background(), "snapshot", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("actual large-ID SDK resource callback failed: %v", err)
	}
	var typed ownedBrushInventorySDKSnapshot
	if err = json.Unmarshal(result.Result, &typed); err != nil {
		t.Fatal(err)
	}
	if typed.PID <= 0 || typed.PID == os.Getpid() || !typed.StaleDenied || typed.Inventory.SchemaVersion != 1 || len(typed.Inventory.Sites) != 1 || typed.Inventory.Sites[0].SourceID != want {
		t.Fatal("actual worker did not return the exact large SourceID in its typed DTO")
	}
	var tokens struct {
		Inventory struct {
			Sites []struct {
				SourceID json.RawMessage `json:"source_id"`
			} `json:"sites"`
		} `json:"inventory"`
	}
	if err = json.Unmarshal(result.Result, &tokens); err != nil || len(tokens.Inventory.Sites) != 1 {
		t.Fatal("actual worker snapshot did not contain the expected raw source token", err)
	}
	wantToken := strconv.FormatInt(want, 10)
	if got := string(tokens.Inventory.Sites[0].SourceID); got != wantToken {
		t.Fatalf("wire SourceID token = %s, want exact unquoted integer %s", got, wantToken)
	}
	return typed
}

func TestOriginalBrushInventoryActualSDKLargeSourceIDLossless(t *testing.T) {
	a, cookie, object := ownedBrushInventorySDKFixture(t, true)
	ownedBrushInventorySeedSequenceBoundary(t, a, ownedBrushInventoryLargeSourceID-1)
	var requests atomic.Int32
	origin := ownedBrushInventoryEndpoint(t, &requests)
	config := a.Settings()
	config.Sites = []SiteConfig{{Config: sites.Config{ID: "owned-large-source", Kind: "jiayuan", Name: "Owned large source", BaseURL: origin}, Enabled: false}}
	// This is the normal publication/allocation path, not a hand-written native
	// identity row. identityRequire below verifies the MAC produced by core.
	ownedBrushInventorySave(t, a, config)
	identity := identityRequire(t, a, "owned-large-source")
	if identity.SourceID != ownedBrushInventoryLargeSourceID || identity.State != "active" || identity.CanonicalOrigin != origin {
		t.Fatal("ordinary settings save did not allocate and authenticate the exact large SourceID")
	}
	before := ownedBrushInventoryChanges(t, a)
	first := ownedBrushInventoryReadExactLargeID(t, object, ownedBrushInventoryLargeSourceID)
	ownedBrushInventoryNoWrites(t, a, before)
	if requests.Load() != 0 {
		t.Fatal("large-ID inventory contacted the owned site")
	}

	config = a.Settings()
	config.Sites[0].Name = "Owned renamed large source"
	ownedBrushInventorySave(t, a, config)
	if current := identityRequire(t, a, "owned-large-source"); current != identity {
		t.Fatal("display-name edit altered the large native identity or its authenticated mapping")
	}
	before = ownedBrushInventoryChanges(t, a)
	renamed := ownedBrushInventoryReadExactLargeID(t, object, ownedBrushInventoryLargeSourceID)
	if renamed.PID != first.PID || !renamed.HadPrevious || renamed.Inventory.Sites[0].Name != "Owned renamed large source" {
		t.Fatal("rename replaced the worker or did not preserve its exact large-ID result")
	}
	ownedBrushInventoryNoWrites(t, a, before)

	response := callTest(t, a.Handler(), "POST", "/api/plugins/"+object.installation.PluginID+"/clone", `{"suffix":"Large"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	value, ok := a.pluginRunningInstances.Load(object.installation.PluginID + "large")
	if !ok || value == object {
		t.Fatal("public clone did not create an independent actual worker")
	}
	clone := value.(*originalPluginInstance)
	before = ownedBrushInventoryChanges(t, a)
	cloned := ownedBrushInventoryReadExactLargeID(t, clone, ownedBrushInventoryLargeSourceID)
	if cloned.PID == renamed.PID || cloned.Inventory.Sites[0].Name != renamed.Inventory.Sites[0].Name {
		t.Fatal("clone did not preserve the same shared large SourceID through its own typed SDK process")
	}
	ownedBrushInventoryNoWrites(t, a, before)
	if current := identityRequire(t, a, "owned-large-source"); current != identity {
		t.Fatal("reading through the clone changed or reassigned the large native identity")
	}
	if requests.Load() != 0 {
		t.Fatal("large-ID rename or clone read triggered network access")
	}
}

func TestOriginalBrushInventoryDTOSourceIDExactJSONIntegers(t *testing.T) {
	for _, sourceID := range []int64{ownedBrushInventoryLargeSourceID, 9223372036854775807} {
		t.Run(strconv.FormatInt(sourceID, 10), func(t *testing.T) {
			value := plugins.BrushInventoryV1{SchemaVersion: 1, Sites: []plugins.BrushSiteReferenceV1{{SourceID: sourceID, Name: "Owned integer boundary", Kind: "nexusphp", LocalStatistics: plugins.BrushLocalStatisticsV1{Status: "missing"}}}, Downloaders: []plugins.BrushDownloaderReferenceV1{}}
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			var typed plugins.BrushInventoryV1
			if err = json.Unmarshal(raw, &typed); err != nil || len(typed.Sites) != 1 || typed.Sites[0].SourceID != sourceID {
				t.Fatal("SDK DTO round trip lost the exact signed int64 SourceID", err)
			}
			var tokens struct {
				Sites []struct {
					SourceID json.RawMessage `json:"source_id"`
				} `json:"sites"`
			}
			if err = json.Unmarshal(raw, &tokens); err != nil || len(tokens.Sites) != 1 || string(tokens.Sites[0].SourceID) != strconv.FormatInt(sourceID, 10) {
				t.Fatal("SDK DTO emitted a rounded, quoted or non-integer SourceID token", err)
			}
		})
	}
}
