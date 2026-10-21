package app

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
)

const siteAccountSnapshotPrefix = "light-media/site-account/v1:"

func siteAccountSnapshotKind(kind string) bool {
	return kind == "site_connection" || kind == "site_stats_latest" || kind == "site_stats_history"
}

// Bind private account observations to their record kind and identity. Legacy
// JSON remains readable; every subsequent collection writes an encrypted value.
func (s *Store) sealSiteAccountSnapshot(kind, key string, raw []byte) ([]byte, error) {
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	aad, _ := json.Marshal([]string{siteAccountSnapshotPrefix, kind, key})
	return append([]byte(siteAccountSnapshotPrefix), g.Seal(nonce, nonce, raw, aad)...), nil
}

func (s *Store) openSiteAccountSnapshot(kind, key string, raw []byte, dst any) error {
	if bytes.HasPrefix(raw, []byte(siteAccountSnapshotPrefix)) {
		sealed := raw[len(siteAccountSnapshotPrefix):]
		block, err := aes.NewCipher(s.key)
		if err != nil {
			return err
		}
		g, err := cipher.NewGCM(block)
		if err != nil {
			return err
		}
		if len(sealed) < g.NonceSize() {
			return invalidSiteAccountSnapshot()
		}
		aad, _ := json.Marshal([]string{siteAccountSnapshotPrefix, kind, key})
		raw, err = g.Open(nil, sealed[:g.NonceSize()], sealed[g.NonceSize():], aad)
		if err != nil {
			return invalidSiteAccountSnapshot()
		}
	}
	if json.Unmarshal(raw, dst) != nil {
		return invalidSiteAccountSnapshot()
	}
	return nil
}

func invalidSiteAccountSnapshot() error {
	return bad("SITE_STATISTICS_CORRUPT", "站点账户快照无法通过校验")
}

func (s *Store) putSiteAccountSnapshotTx(ctx context.Context, tx *sql.Tx, kind, key string, v siteSnapshot) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	sealed, err := s.sealSiteAccountSnapshot(kind, key, raw)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO records VALUES(?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, kind, key, sealed, now())
	return err
}
