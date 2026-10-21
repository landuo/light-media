package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"light-media/internal/plugins/statistic"
)

const sourceAccountSchema = `
CREATE TABLE IF NOT EXISTS source_site_account_rows(
 source_fingerprint TEXT NOT NULL,original_id INTEGER NOT NULL,
 domain TEXT NOT NULL,updated_day TEXT NOT NULL,updated_time TEXT NOT NULL,good INTEGER NOT NULL,
 sealed BLOB NOT NULL,PRIMARY KEY(source_fingerprint,original_id));
CREATE INDEX IF NOT EXISTS source_site_account_day ON source_site_account_rows(source_fingerprint,updated_day,original_id);
CREATE INDEX IF NOT EXISTS source_site_account_domain ON source_site_account_rows(source_fingerprint,domain,updated_day,updated_time);`

const sourceAccountKind = "original_site_account_history"

// Keep the entire SQLite row, including its JSON text and nullable values. The
// plugin's six numeric chart fields are only a projection of this shared data.
type sourceAccountRow struct {
	ID                    int64    `json:"id"`
	Domain                *string  `json:"domain"`
	Name                  *string  `json:"name"`
	Username              *string  `json:"username"`
	UserID                *string  `json:"userid"`
	UserLevel             *string  `json:"user_level"`
	JoinAt                *string  `json:"join_at"`
	Bonus                 *float64 `json:"bonus"`
	Upload                *float64 `json:"upload"`
	Download              *float64 `json:"download"`
	Ratio                 *float64 `json:"ratio"`
	Seeding               *float64 `json:"seeding"`
	Leeching              *float64 `json:"leeching"`
	SeedingSize           *float64 `json:"seeding_size"`
	LeechingSize          *float64 `json:"leeching_size"`
	SeedingInfo           *string  `json:"seeding_info"`
	MessageUnread         *int64   `json:"message_unread"`
	MessageUnreadContents *string  `json:"message_unread_contents"`
	Error                 *string  `json:"err_msg"`
	Day                   *string  `json:"updated_day"`
	Time                  *string  `json:"updated_time"`
}
type sourceAccountHeader struct {
	ID                int64
	Domain, Day, Time string
	Good              bool
	SHA               string
}
type sourceAccountSite struct {
	OriginalID, NativeID        int64
	SiteID, Origin, Domain, URL string
}
type sourceAccountBinding struct {
	Version                                                        int
	SourceFingerprint, ConfigSHA, DataSHA, CatalogSHA, SettingsSHA string
	Sites                                                          []sourceAccountSite
	Rows                                                           []sourceAccountHeader
}
type sourceAccountProof struct {
	Binding                 sourceAccountBinding
	Migration, ImportDigest string
}

func invalidSourceAccounts() error {
	return bad("SITE_ACCOUNT_HISTORY_INVALID", "原站点账户历史或迁移收据无法校验")
}
func accountString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func decodeSourceAccount(raw []byte) (sourceAccountRow, sourceAccountHeader, error) {
	var row sourceAccountRow
	if len(raw) > pluginDataValueLimit || sourceSettingsJSON(raw, &row) != nil {
		return row, sourceAccountHeader{}, invalidSourceAccounts()
	}
	if _, e := sourceSettingsObject(raw, "id", "domain", "name", "username", "userid", "user_level", "join_at", "bonus", "upload", "download", "ratio", "seeding", "leeching", "seeding_size", "leeching_size", "seeding_info", "message_unread", "message_unread_contents", "err_msg", "updated_day", "updated_time"); e != nil || row.ID < 1 || row.Domain == nil || *row.Domain == "" || row.Day == nil || row.Time == nil {
		return row, sourceAccountHeader{}, invalidSourceAccounts()
	}
	if _, e := time.Parse("2006-01-02", *row.Day); e != nil {
		return row, sourceAccountHeader{}, invalidSourceAccounts()
	}
	if _, e := time.Parse("15:04:05", *row.Time); e != nil {
		return row, sourceAccountHeader{}, invalidSourceAccounts()
	}
	for _, value := range []*string{row.SeedingInfo, row.MessageUnreadContents} {
		if value != nil && !json.Valid([]byte(*value)) {
			return row, sourceAccountHeader{}, invalidSourceAccounts()
		}
	}
	return row, sourceAccountHeader{row.ID, *row.Domain, *row.Day, *row.Time, accountString(row.Error) == "", tokenHash(string(raw))}, nil
}
func (r sourceAccountRow) statistic(siteID, domain string) statistic.Data {
	return statistic.Data{SiteID: siteID, Domain: domain, Name: accountString(r.Name), Username: r.Username, UserLevel: r.UserLevel, Upload: r.Upload, Download: r.Download, Ratio: r.Ratio, Bonus: r.Bonus, Seeding: r.Seeding, SeedingSize: r.SeedingSize, UpdatedDay: accountString(r.Day), UpdatedTime: accountString(r.Time), Error: accountString(r.Error)}
}

func (p *PluginStore) sourceAccountProofTx(ctx context.Context, tx pluginRemoveLinkJournalReader) (*sourceAccountProof, error) {
	var sealed []byte
	e := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id='main'`, sourceAccountKind).Scan(&sealed)
	if errors.Is(e, sql.ErrNoRows) {
		var imported int
		if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_migration_receipts WHERE source_plugin_id='SiteStatistic' AND instance_id='SiteStatistic' AND source_fingerprint=?`, reviewedStatisticFingerprint()).Scan(&imported); e != nil || imported != 0 {
			return nil, invalidSourceAccounts()
		}
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	raw, e := p.unseal(sourceAccountKind, "SiteStatistic", "main", sealed)
	var proof sourceAccountProof
	if e != nil || cleanupJournalStrict(raw, &proof) != nil || !validSourceAccountBinding(proof.Binding) || !pluginStorageID.MatchString(proof.Migration) || !pluginStorageDigest.MatchString(proof.ImportDigest) {
		return nil, invalidSourceAccounts()
	}
	var digest, state string
	if tx.QueryRowContext(ctx, `SELECT r.digest,m.state FROM plugin_migration_receipts r JOIN plugin_migration_runs m ON m.id=r.run_id AND m.source_fingerprint=r.source_fingerprint AND m.source_plugin_id=r.source_plugin_id AND m.instance_id=r.instance_id WHERE r.run_id=? AND r.source_fingerprint=? AND r.source_plugin_id='SiteStatistic' AND r.instance_id='SiteStatistic'`, proof.Migration, proof.Binding.SourceFingerprint).Scan(&digest, &state) != nil || digest != proof.ImportDigest || state != "imported" {
		return nil, invalidSourceAccounts()
	}
	return &proof, nil
}

// Authenticate the complete searchable index before using SQL selection. A
// missing row or changed day/domain must not silently change a chart's result.
func sourceAccountHeadersTx(ctx context.Context, tx *sql.Tx, b sourceAccountBinding) error {
	rows, e := tx.QueryContext(ctx, `SELECT original_id,domain,updated_day,updated_time,good FROM source_site_account_rows WHERE source_fingerprint=? ORDER BY original_id`, b.SourceFingerprint)
	if e != nil {
		return e
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
		var h sourceAccountHeader
		if rows.Scan(&h.ID, &h.Domain, &h.Day, &h.Time, &h.Good) != nil || i >= len(b.Rows) {
			return invalidSourceAccounts()
		}
		h.SHA = b.Rows[i].SHA
		if h != b.Rows[i] {
			return invalidSourceAccounts()
		}
		i++
	}
	if rows.Err() != nil || i != len(b.Rows) {
		return invalidSourceAccounts()
	}
	return nil
}
func (p *PluginStore) sourceAccountRowTx(ctx context.Context, tx *sql.Tx, b sourceAccountBinding, h sourceAccountHeader) (sourceAccountRow, error) {
	var sealed []byte
	if tx.QueryRowContext(ctx, `SELECT sealed FROM source_site_account_rows WHERE source_fingerprint=? AND original_id=?`, b.SourceFingerprint, h.ID).Scan(&sealed) != nil {
		return sourceAccountRow{}, invalidSourceAccounts()
	}
	raw, e := p.unseal(sourceAccountKind, b.SourceFingerprint, strconv.FormatInt(h.ID, 10), sealed)
	if e != nil {
		return sourceAccountRow{}, invalidSourceAccounts()
	}
	row, actual, e := decodeSourceAccount(raw)
	if e != nil || actual != h {
		return row, invalidSourceAccounts()
	}
	return row, nil
}

// Use the original model's latest GOOD day, then retain every row on that day,
// including errors and duplicates; its ordering is by time, not date or ID.
func sourceAccountLatestIDsTx(ctx context.Context, tx *sql.Tx, fp string) ([]int64, error) {
	rows, e := tx.QueryContext(ctx, `SELECT s.original_id FROM source_site_account_rows s JOIN (SELECT domain,MAX(updated_day) AS latest_day FROM source_site_account_rows WHERE source_fingerprint=? AND good=1 GROUP BY domain) d ON s.domain=d.domain AND s.updated_day=d.latest_day WHERE s.source_fingerprint=? ORDER BY s.updated_time DESC`, fp, fp)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type accountObservation struct {
	Header sourceAccountHeader
	SiteID string
	Native *siteSnapshot
	Order  int64
}

func accountObservationLess(a, b accountObservation, rank map[int64]int) bool {
	if a.Header.Time != b.Header.Time {
		return a.Header.Time > b.Header.Time
	}
	i, knownA := rank[a.Header.ID]
	j, knownB := rank[b.Header.ID]
	if !knownA {
		i = len(rank)
	}
	if !knownB {
		j = len(rank)
	}
	return i < j
}
