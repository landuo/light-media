package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func materialFixture(announce string, padding int) ([]byte, string) {
	torrent, hash := workflowTorrent("Owned.Synthetic.Film.2024.mkv")
	// Preserve the exact info bytes while varying tracker/comment bytes outside
	// the info dictionary: these carry authority beyond the common infohash.
	raw := append([]byte("d8:announce"), workflowBencode(announce)...)
	if padding > 0 {
		raw = append(raw, []byte("7:comment")...)
		raw = append(raw, workflowBencode(strings.Repeat("x", padding))...)
	}
	raw = append(raw, torrent[1:]...)
	return raw, hash
}
func pinMaterialFixture(t *testing.T, a *App, ref, hash string) {
	t.Helper()
	ctx := context.Background()
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = pinMaterialTx(ctx, tx, ref, hash); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
func assertMaterialError(t *testing.T, err error, code string) {
	t.Helper()
	var api *APIError
	if !errors.As(err, &api) || api.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}
func TestMaterialArchiveSurvivesRestartWithoutPrivateTrackerPlaintext(t *testing.T) {
	dir := t.TempDir()
	a, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if a != nil {
			a.Close()
		}
	})
	marker := "synthetic-passkey-material-test-741be22a"
	raw, hash := materialFixture("https://owned.invalid/announce?passkey="+marker, 0)
	ref, err := a.persistMaterial(context.Background(), raw, hash)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	if !strings.HasPrefix(ref, "mat_") || len(ref) != 68 || strings.Contains(ref, hex.EncodeToString(digest[:])) {
		t.Fatal("reference is not opaque keyed material identity")
	}
	pinMaterialFixture(t, a, ref, hash)
	for _, name := range []string{"light-media.db", "light-media.db-wal"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(marker)) || bytes.Contains(data, []byte("https://owned.invalid/announce")) {
			t.Fatalf("private tracker plaintext escaped into %s", name)
		}
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a = nil
	a, err = New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := a.resolveMaterial(context.Background(), ref, materialPurpose)
	if err != nil || !bytes.Equal(raw, restored) {
		t.Fatal("restart lost exact original torrent", err)
	}
	var info materialInfo
	if err = a.Store.Get(context.Background(), "material_info", ref, &info); err != nil || !info.Pinned || info.ExpiresAt != "" {
		t.Fatalf("confirmed material is expiring %+v %v", info, err)
	}
	again, err := a.persistMaterial(context.Background(), raw, hash)
	if err != nil || again != ref {
		t.Fatal("same bytes did not preserve restart reference", err)
	}
	other := newTestApp(t)
	different, err := other.persistMaterial(context.Background(), raw, hash)
	if err != nil || different == ref {
		t.Fatal("reference was not keyed to the owning application", err)
	}
}
func TestMaterialArchiveDeduplicatesExactBytesNotOnlyInfohash(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	first, hash := materialFixture("https://owned-a.invalid/announce?passkey=synthetic-a", 0)
	second, secondHash := materialFixture("https://owned-b.invalid/announce?passkey=synthetic-b", 0)
	if hash != secondHash {
		t.Fatal("fixture must share original info dictionary")
	}
	refA, err := a.persistMaterial(ctx, first, hash)
	if err != nil {
		t.Fatal(err)
	}
	refB, err := a.persistMaterial(ctx, second, hash)
	if err != nil {
		t.Fatal(err)
	}
	if refA == refB {
		t.Fatal("different private tracker material reused one infohash reference")
	}
	for _, item := range []struct {
		ref string
		raw []byte
	}{{refA, first}, {refB, second}} {
		got, err := a.resolveMaterial(ctx, item.ref, materialPurpose)
		if err != nil || !bytes.Equal(got, item.raw) {
			t.Fatal("archive did not preserve separate authority bytes", err)
		}
	}
	duplicate, err := a.persistMaterial(ctx, first, hash)
	if err != nil || duplicate != refA {
		t.Fatal("exact raw bytes were not deduplicated", err)
	}
	var blobs int
	a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE kind='material_blob'`).Scan(&blobs)
	if blobs != 2 {
		t.Fatalf("duplicate created %d blobs", blobs)
	}
}
func TestMaterialOrphanExpiryDoesNotExpirePinnedIntent(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	orphan, hash := materialFixture("https://owned.invalid/orphan", 0)
	pinned, _ := materialFixture("https://owned.invalid/pinned", 0)
	orphanRef, err := a.persistMaterial(ctx, orphan, hash)
	if err != nil {
		t.Fatal(err)
	}
	pinnedRef, err := a.persistMaterial(ctx, pinned, hash)
	if err != nil {
		t.Fatal(err)
	}
	pinMaterialFixture(t, a, pinnedRef, hash)
	for _, ref := range []string{orphanRef, pinnedRef} {
		var info materialInfo
		a.Store.Get(ctx, "material_info", ref, &info)
		info.ExpiresAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
		if err = a.Store.Put(ctx, "material_info", ref, info); err != nil {
			t.Fatal(err)
		}
	}
	_, err = a.resolveMaterial(ctx, orphanRef, materialPurpose)
	assertMaterialError(t, err, "MATERIAL_EXPIRED")
	got, err := a.resolveMaterial(ctx, pinnedRef, materialPurpose)
	if err != nil || !bytes.Equal(got, pinned) {
		t.Fatal("pinned material expired", err)
	}
	next, _ := materialFixture("https://owned.invalid/new", 0)
	if _, err = a.persistMaterial(ctx, next, hash); err != nil {
		t.Fatal(err)
	}
	var info materialInfo
	if err = a.Store.Get(ctx, "material_info", orphanRef, &info); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("expired unconfirmed material was not collected", err)
	}
	if got, err = a.resolveMaterial(ctx, pinnedRef, materialPurpose); err != nil || !bytes.Equal(got, pinned) {
		t.Fatal("orphan collection deleted pinned bytes", err)
	}
}
func TestMaterialArchiveRejectsWrongPurposeTamperingAndMissingBlob(t *testing.T) {
	for _, mode := range []string{"purpose", "ciphertext", "metadata purpose", "missing"} {
		t.Run(mode, func(t *testing.T) {
			a := newTestApp(t)
			ctx := context.Background()
			raw, hash := materialFixture("https://owned.invalid/protected", 0)
			ref, err := a.persistMaterial(ctx, raw, hash)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "purpose" {
				_, err = a.resolveMaterial(ctx, ref, "plugin-cross-seed")
				assertMaterialError(t, err, "INVALID_MATERIAL_REF")
				return
			}
			if mode == "metadata purpose" {
				var info materialInfo
				a.Store.Get(ctx, "material_info", ref, &info)
				info.Purpose = "plugin-cross-seed"
				a.Store.Put(ctx, "material_info", ref, info)
			}
			if mode == "ciphertext" {
				var sealed []byte
				a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='material_blob' AND id=?`, ref).Scan(&sealed)
				sealed[len(sealed)-1] ^= 1
				if _, err = a.Store.DB.ExecContext(ctx, `UPDATE records SET payload=? WHERE kind='material_blob' AND id=?`, sealed, ref); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "missing" {
				a.Store.DB.ExecContext(ctx, `DELETE FROM records WHERE kind='material_blob' AND id=?`, ref)
			}
			_, err = a.resolveMaterial(ctx, ref, materialPurpose)
			if mode == "missing" {
				assertMaterialError(t, err, "MATERIAL_UNAVAILABLE")
			} else {
				assertMaterialError(t, err, "MATERIAL_CORRUPT")
				_, err = a.persistMaterial(ctx, raw, hash)
				assertMaterialError(t, err, "MATERIAL_CORRUPT")
			}
		})
	}
}
func TestMaterialBudgetKeepsPinnedArchiveAndRejectsAdditionalBytes(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	if a.materialBudget() != 256<<20 {
		t.Fatal("default archive budget changed")
	}
	for _, size := range []int{32, 2048} {
		if err := validateSettings(Settings{MaterialMaxMiB: size}); err != nil {
			t.Fatal("valid archive limit rejected", size, err)
		}
	}
	for _, size := range []int{-1, 1, 31, 2049} {
		if err := validateSettings(Settings{MaterialMaxMiB: size}); err == nil {
			t.Fatal("invalid archive limit accepted", size)
		}
	}
	a.config.MaterialMaxMiB = 32
	pinned, hash := materialFixture("https://owned.invalid/large-pinned", 17<<20)
	ref, err := a.persistMaterial(ctx, pinned, hash)
	if err != nil {
		t.Fatal(err)
	}
	pinMaterialFixture(t, a, ref, hash)
	additional, _ := materialFixture("https://owned.invalid/additional", 16<<20)
	_, err = a.persistMaterial(ctx, additional, hash)
	assertMaterialError(t, err, "MATERIAL_ARCHIVE_FULL")
	got, err := a.resolveMaterial(ctx, ref, materialPurpose)
	if err != nil || !bytes.Equal(got, pinned) {
		t.Fatal("capacity pressure deleted or damaged pinned material", err)
	}
}
func TestMaterialPinCommitsOnlyWithDownloadConfirmation(t *testing.T) {
	a, _, _ := importTestApp(t)
	ctx := context.Background()
	m := importFixture(t, a, "movie")
	raw, hash := materialFixture("https://owned.invalid/transaction?passkey=synthetic-transaction", 0)
	ref, err := a.persistMaterial(ctx, raw, hash)
	if err != nil {
		t.Fatal(err)
	}
	preview := DownloadPreview{ID: "material-preview", WorkflowID: "wf-material", Media: m, RootID: "root", DownloaderID: "owned-qb", Hash: hash, MaterialRef: ref, MaterialState: "archived", ExpiresAt: time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)}
	if err = a.Store.Put(ctx, "download_preview", preview.ID, preview); err != nil {
		t.Fatal(err)
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Store.Reserve(ctx, tx, mediaKey(m), 0, nil, "another-acquisition"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = a.confirmDownload(ctx, preview.ID, false); err == nil {
		t.Fatal("conflicting target unexpectedly confirmed")
	}
	var info materialInfo
	a.Store.Get(ctx, "material_info", ref, &info)
	if info.Pinned || info.ExpiresAt == "" {
		t.Fatal("failed confirmation pinned an orphan")
	}
	a.Store.DB.ExecContext(ctx, `DELETE FROM reservations WHERE intent_id='another-acquisition'`)
	intent, err := a.confirmDownload(ctx, preview.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	info = materialInfo{}
	a.Store.Get(ctx, "material_info", ref, &info)
	if !info.Pinned || info.ExpiresAt != "" || intent.MaterialRef != ref || intent.Purpose != materialPurpose {
		t.Fatalf("successful intent did not pin original material %+v err=%v", info, err)
	}
	cookie := setupTestAdmin(t, a)
	response := callTest(t, a.Handler(), "GET", "/api/materials/"+ref, "", cookie, "")
	if response.Code != 404 || bytes.Contains(response.Body.Bytes(), raw) {
		t.Fatal("raw protected material exposed over HTTP")
	}
}

func TestMaterialScheduledCleanupDeletesOnlyExpiredUnpinned(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	refs := map[string]string{}
	payloads := map[string][]byte{}
	for _, kind := range []string{"expired-orphan", "fresh-orphan", "pinned"} {
		raw, hash := materialFixture("https://owned.invalid/"+kind, 0)
		ref, err := a.persistMaterial(ctx, raw, hash)
		if err != nil {
			t.Fatal(err)
		}
		refs[kind], payloads[kind] = ref, raw
		if kind == "pinned" {
			pinMaterialFixture(t, a, ref, hash)
		}
		if kind != "fresh-orphan" {
			var info materialInfo
			if err = a.Store.Get(ctx, "material_info", ref, &info); err != nil {
				t.Fatal(err)
			}
			info.ExpiresAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
			if err = a.Store.Put(ctx, "material_info", ref, info); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := a.cleanupMaterialOrphans(ctx); err != nil {
		t.Fatal(err)
	}
	for kind, ref := range refs {
		var count int
		if err := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE kind IN ('material_info','material_blob') AND id=?`, ref).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if kind == "expired-orphan" {
			if count != 0 {
				t.Fatal("hourly collection retained expired orphan")
			}
			continue
		}
		if count != 2 {
			t.Fatalf("hourly collection removed %s", kind)
		}
		got, err := a.resolveMaterial(ctx, ref, materialPurpose)
		if err != nil || !bytes.Equal(got, payloads[kind]) {
			t.Fatalf("hourly collection damaged %s: %v", kind, err)
		}
	}
}
