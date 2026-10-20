package app

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"

	"light-media/internal/metainfo"
)

const materialPurpose = "core_download"

type materialInfo struct {
	Ref       string `json:"ref"`
	Hash      string `json:"hash"`
	Digest    string `json:"sha256"`
	Purpose   string `json:"purpose"`
	Size      int    `json:"size"`
	Pinned    bool   `json:"pinned"`
	ExpiresAt string `json:"expires_at,omitempty"`
	CreatedAt string `json:"created_at"`
}

func (a *App) materialBudget() int64 {
	n := a.Settings().MaterialMaxMiB
	if n == 0 {
		n = 256
	}
	return int64(n) << 20
}
func (a *App) materialAEAD() (cipher.AEAD, error) {
	block, e := aes.NewCipher(a.Store.key)
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(block)
}

// Archive original bytes, including private tracker data, only as authenticated
// ciphertext. A reference is opaque and is never a URL or a filesystem path.
func (a *App) persistMaterial(ctx context.Context, raw []byte, hash string) (string, error) {
	if len(raw) == 0 || len(raw) > 32<<20 {
		return "", bad("INVALID_MATERIAL", "种子材料超过允许大小")
	}
	material, e := metainfo.ParseMaterial(raw)
	if e != nil || material.Hash != hash {
		return "", bad("INVALID_MATERIAL", "种子原始材料与稳定标识不符")
	}
	digest := sha256.Sum256(raw)
	mac := hmac.New(sha256.New, a.Store.key)
	mac.Write([]byte("torrent-material-v1:"))
	mac.Write(digest[:])
	ref := "mat_" + hex.EncodeToString(mac.Sum(nil))
	a.materialMu.Lock()
	defer a.materialMu.Unlock()
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return "", e
	}
	defer tx.Rollback()
	var previous []byte
	e = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='material_info' AND id=?`, ref).Scan(&previous)
	if e == nil {
		var info materialInfo
		if json.Unmarshal(previous, &info) != nil || info.Ref != ref || info.Purpose != materialPurpose || info.Size != len(raw) || info.Hash != hash || info.Digest != hex.EncodeToString(digest[:]) {
			return "", bad("MATERIAL_CORRUPT", "已有材料归档无法核对")
		}
		var sealed []byte
		if e = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='material_blob' AND id=?`, ref).Scan(&sealed); errors.Is(e, sql.ErrNoRows) {
			return "", bad("MATERIAL_UNAVAILABLE", "材料归档缺失，需人工核对")
		} else if e != nil {
			return "", e
		}
		aead, err := a.materialAEAD()
		if err != nil {
			return "", err
		}
		if len(sealed) != len(raw)+aead.NonceSize()+aead.Overhead() {
			return "", bad("MATERIAL_CORRUPT", "已有材料归档大小不符")
		}
		original, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte("light-media/material/v1/"+ref))
		if err != nil || !bytes.Equal(original, raw) {
			return "", bad("MATERIAL_CORRUPT", "已有材料归档完整性校验失败")
		}
		if !info.Pinned {
			info.ExpiresAt = time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)
			if e = txPut(ctx, tx, "material_info", ref, info); e != nil {
				return "", e
			}
		}
		return ref, tx.Commit()
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return "", e
	}
	if e = cleanupMaterialOrphansTx(ctx, tx); e != nil {
		return "", e
	}
	aead, e := a.materialAEAD()
	if e != nil {
		return "", e
	}
	nonce := make([]byte, aead.NonceSize())
	if _, e = io.ReadFull(rand.Reader, nonce); e != nil {
		return "", e
	}
	sealed := aead.Seal(nonce, nonce, raw, []byte("light-media/material/v1/"+ref))
	var used int64
	if e = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(length(payload)),0) FROM records WHERE kind='material_blob'`).Scan(&used); e != nil {
		return "", e
	}
	if used+int64(len(sealed)) > a.materialBudget() {
		return "", bad("MATERIAL_ARCHIVE_FULL", "加密种子归档达到上限，请在设置提高归档容量；在用材料未删除")
	}
	info := materialInfo{Ref: ref, Hash: hash, Digest: hex.EncodeToString(digest[:]), Purpose: materialPurpose, Size: len(raw), CreatedAt: now(), ExpiresAt: time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)}
	if e = txPut(ctx, tx, "material_info", ref, info); e != nil {
		return "", e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO records VALUES('material_blob',?,?,?)`, ref, sealed, now()); e != nil {
		return "", e
	}
	return ref, tx.Commit()
}

func cleanupMaterialOrphansTx(ctx context.Context, tx *sql.Tx) error {
	rows, e := tx.QueryContext(ctx, `SELECT id FROM records WHERE kind='material_info' AND json_extract(payload,'$.pinned')=0 AND json_extract(payload,'$.expires_at')<? LIMIT 4096`, now())
	if e != nil {
		return e
	}
	var expired []string
	for rows.Next() {
		var key string
		if e = rows.Scan(&key); e != nil {
			rows.Close()
			return e
		}
		expired = append(expired, key)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, key := range expired {
		if _, e = tx.ExecContext(ctx, `DELETE FROM records WHERE kind IN ('material_info','material_blob') AND id=?`, key); e != nil {
			return e
		}
	}
	return nil
}

func (a *App) cleanupMaterialOrphans(ctx context.Context) error {
	a.materialMu.Lock()
	defer a.materialMu.Unlock()
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = cleanupMaterialOrphansTx(ctx, tx); e != nil {
		return e
	}
	return tx.Commit()
}

func pinMaterialTx(ctx context.Context, tx *sql.Tx, ref, hash string) error {
	if ref == "" {
		return nil
	} // Existing validation fixtures and pre-archive records.
	var raw []byte
	if e := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='material_info' AND id=?`, ref).Scan(&raw); e != nil {
		return bad("MATERIAL_UNAVAILABLE", "原种子材料归档不存在，请重新预览")
	}
	var info materialInfo
	if json.Unmarshal(raw, &info) != nil || info.Ref != ref || info.Hash != hash || info.Purpose != materialPurpose {
		return bad("MATERIAL_CORRUPT", "材料归档身份不符，请重新预览")
	}
	if !info.Pinned {
		expires, e := time.Parse(time.RFC3339Nano, info.ExpiresAt)
		if e != nil || time.Now().After(expires) {
			return bad("MATERIAL_EXPIRED", "未确认的材料归档已过期，请重新预览")
		}
	}
	var count int
	if e := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE kind='material_blob' AND id=?`, ref).Scan(&count); e != nil {
		return e
	}
	if count != 1 {
		return bad("MATERIAL_UNAVAILABLE", "原种子材料已不可用")
	}
	info.Pinned = true
	info.ExpiresAt = ""
	return txPut(ctx, tx, "material_info", ref, info)
}

// resolveMaterial is an internal core capability. P1 must put permission checks
// around its host resolver; this function has no HTTP or plugin passthrough.
func (a *App) resolveMaterial(ctx context.Context, ref, purpose string) ([]byte, error) {
	if purpose != materialPurpose || len(ref) != 68 || ref[:4] != "mat_" {
		return nil, bad("INVALID_MATERIAL_REF", "材料引用或用途无效")
	}
	var info materialInfo
	if e := a.Store.Get(ctx, "material_info", ref, &info); e != nil {
		return nil, bad("MATERIAL_UNAVAILABLE", "受保护的原种子材料不可用")
	}
	if info.Ref != ref || info.Purpose != purpose {
		return nil, bad("MATERIAL_CORRUPT", "材料引用无法核对")
	}
	if !info.Pinned {
		expires, e := time.Parse(time.RFC3339Nano, info.ExpiresAt)
		if e != nil || time.Now().After(expires) {
			return nil, bad("MATERIAL_EXPIRED", "未确认的种子材料引用已过期")
		}
	}
	var sealed []byte
	if e := a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='material_blob' AND id=?`, ref).Scan(&sealed); e != nil {
		return nil, bad("MATERIAL_UNAVAILABLE", "受保护的原种子材料不可用")
	}
	aead, e := a.materialAEAD()
	if e != nil {
		return nil, e
	}
	if len(sealed) < aead.NonceSize()+aead.Overhead() || len(sealed) > 32<<20+aead.NonceSize()+aead.Overhead() {
		return nil, bad("MATERIAL_CORRUPT", "种子材料归档大小无效")
	}
	raw, e := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte("light-media/material/v1/"+ref))
	if e != nil {
		return nil, bad("MATERIAL_CORRUPT", "种子材料完整性校验失败")
	}
	digest := sha256.Sum256(raw)
	material, e := metainfo.ParseMaterial(raw)
	if e != nil || material.Hash != info.Hash || hex.EncodeToString(digest[:]) != info.Digest {
		return nil, bad("MATERIAL_CORRUPT", "种子材料身份不一致")
	}
	return raw, nil
}

func (a *App) acceptDownload(ctx context.Context, in Intent, j Job) error {
	source, e := a.loadDownloadContext(ctx, "intent", in.ID, in.Hash)
	if e != nil {
		return e
	}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = a.saveSourceEpisodeSelectionTx(ctx, tx, in); e != nil {
		return e
	}
	if e = txPut(ctx, tx, "intent", in.ID, in); e != nil {
		return e
	}
	if e = a.acceptSubscriptionQualityTx(ctx, tx, in); e != nil {
		return e
	}
	event := DomainEvent{SchemaVersion: 1, EventID: domainEventID("download.accepted", in.ID), Type: "download.accepted", OccurredAt: now(), WorkflowID: in.WorkflowID, JobID: j.ID, MediaID: mediaKey(in.Media), IntentID: in.ID, Purpose: in.Purpose}
	snapshot := downloadAddedSnapshot{DownloadID: in.ID, DownloadKind: "intent", Hash: in.Hash, SiteID: in.SiteID, MediaType: in.Media.MediaType, TMDBID: in.Media.ID, HasContext: source != nil, HasHistory: true, Context: source}
	if e = a.recordDownloadAddedTx(ctx, tx, event, snapshot); e != nil {
		return e
	}
	if e = a.larkAcceptDownloadChildTx(ctx, tx, in, j); e != nil {
		return e
	}
	if e = a.acceptAcquisitionChildTx(ctx, tx, j, in); e != nil {
		return e
	}
	if e = a.enqueueSubscriptionCompletionPreparationTx(ctx, tx, in); e != nil {
		return e
	}
	return tx.Commit()
}
