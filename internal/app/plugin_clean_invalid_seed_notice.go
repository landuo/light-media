package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"time"

	"light-media/internal/notice"
	seed "light-media/internal/plugins/cleaninvalidseed"
	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
)

// This is the validated source call, not an external-send receipt. The original
// source payload stays byte-for-byte in Notice and in the action journal.
type pluginCleanInvalidSeedPrivateNotice struct {
	Version                                  int
	Notice                                   seedhost.Notice
	Envelope                                 json.RawMessage
	HistoryID, State, CreatedAt, CurrentTime string
	HistoryDigest                            []byte
}

type pluginCleanInvalidSeedNoticeReceipt struct {
	Version                  int
	OutboxSHA                string
	RouteID, RouteInitialSHA string
}

func cleanSeedSourceNotice(raw json.RawMessage) (*notice.Envelope, error) {
	var input notice.Object
	if cleanupJournalStrict(raw, &input) != nil || input == nil {
		return nil, seedhost.ErrUnsafe
	}
	// The pure compiled source represents this exact Python enum member by its
	// name. The actual Notification constructor receives its value. Event/user
	// strings do not get an analogous enum-name or invalid-channel fallback.
	if input["mtype"] == "SiteMessage" {
		input["mtype"] = "站点"
	}
	env, err := notice.NewEnvelope(input)
	if err != nil {
		return nil, &seed.SourceError{Type: "ValidationError", Operation: "Notification"}
	}
	return env, nil
}

func cleanSeedNoticeHistoryDigest(j *pluginCleanInvalidSeedJournal, n Notification) []byte {
	n.Read = false
	return j.mac("notice-history-value", mustCleanSeedJSON(n))
}

// The caller already holds and verifies the real source action grant. All local
// history, full-envelope acceptance and original Core receipt commit together.
func (c *pluginCleanInvalidSeedCore) acceptNoticeTx(ctx context.Context, tx *sql.Tx, inv seedhost.Invocation, n seedhost.Notice, effect seedhost.Effect) error {
	env, err := cleanSeedSourceNotice(n.Payload)
	if err != nil {
		return err
	}
	envelope, err := cleanupJournalJSON(env.Dump())
	if err != nil {
		return err
	}
	at := time.Now().UTC()
	if c.o.a.pluginCleanInvalidSeed.now != nil {
		at = c.o.a.pluginCleanInvalidSeed.now().UTC()
	}
	zone, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return err
	}
	box := pluginCleanInvalidSeedPrivateNotice{Version: 2, Notice: n, Envelope: envelope, State: "history_skipped", CreatedAt: at.Format(time.RFC3339Nano), CurrentTime: at.In(zone).Format("2006-01-02 15:04:05")}
	var local Notification
	if env.Get("save_history") == true {
		title, _ := env.Get("title").(string)
		text, _ := env.Get("text").(string)
		mtype, _ := env.Get("mtype").(string)
		link, _ := env.Get("link").(string)
		settings := c.o.a.Settings()
		box.HistoryID = "notice_" + hex.EncodeToString(c.o.j.mac("notice-id", []byte(n.ID)))
		local = Notification{ID: box.HistoryID, Title: cleanWithSettings(title, settings), Message: cleanWithSettings(text, settings), MType: mtype, Link: link, CreatedAt: box.CreatedAt}
		box.HistoryDigest = cleanSeedNoticeHistoryDigest(c.o.j, local)
		box.State = "history_saved"
	}
	payload, err := cleanupJournalJSON(box)
	if err != nil || len(payload) > pluginCleanInvalidSeedJournalLimit {
		return seedhost.ErrUnsafe
	}
	token := c.o.j.token("outbox", n.ID, -1)
	sealed, err := c.o.a.pluginStore.seal("clean_seed_journal", c.o.instance, c.o.j.key("outbox", token), payload)
	if err != nil {
		return err
	}
	if box.HistoryID != "" {
		if err = putCleanupNoticeTx(ctx, tx, local); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_clean_seed_outbox VALUES(?,?,?,?)`, c.o.instance, c.o.generation, token, sealed); err != nil {
		return err
	}
	routeID, initialSHA, err := c.o.a.pluginStore.initCleanNoticeRouteTx(ctx, tx, c.o.j, box, payload)
	if err != nil {
		return err
	}
	proof, err := cleanupJournalJSON(pluginCleanInvalidSeedNoticeReceipt{Version: 1, OutboxSHA: tokenHash(string(payload)), RouteID: routeID, RouteInitialSHA: initialSHA})
	if err != nil {
		return err
	}
	return cleanSeedSaveReceipt(ctx, tx, c.o.j, pluginCleanInvalidSeedReceipt{Version: 1, Invocation: inv, ID: n.ID, Kind: "notice", Digest: cleanSeedEffectDigest(effect), Payload: proof})
}

// Historical acceptance can outlive a source revision or a failed later effect.
// It never confers a live source/native grant. Old title-only outboxes are not
// silently upgraded by reconstructing recipients or attachments from the inbox.
func readCleanSeedPrivateNotice(ctx context.Context, q pluginRemoveLinkJournalReader, j *pluginCleanInvalidSeedJournal, token string) (pluginCleanInvalidSeedPrivateNotice, error) {
	var box pluginCleanInvalidSeedPrivateNotice
	if !pluginStorageDigest.MatchString(token) {
		return box, seedhost.ErrUnsafe
	}
	var sealed []byte
	if err := q.QueryRowContext(ctx, `SELECT sealed FROM plugin_clean_seed_outbox WHERE instance_id=? AND generation=? AND token=?`, j.instance, j.generation, token).Scan(&sealed); err != nil {
		return box, err
	}
	raw, err := j.p.unseal("clean_seed_journal", j.instance, j.key("outbox", token), sealed)
	if err != nil {
		return box, err
	}
	if cleanupJournalStrict(raw, &box) != nil || box.Version != 2 || box.Notice.ID == "" || box.Notice.RunID == "" || box.Notice.State != "pending" || j.token("outbox", box.Notice.ID, -1) != token {
		return box, seedhost.ErrUnsafe
	}
	at, err := time.Parse(time.RFC3339Nano, box.CreatedAt)
	zone, zoneErr := time.LoadLocation("Asia/Shanghai")
	if err != nil || zoneErr != nil || box.CurrentTime != at.In(zone).Format("2006-01-02 15:04:05") {
		return box, seedhost.ErrUnsafe
	}
	env, err := cleanSeedSourceNotice(box.Notice.Payload)
	if err != nil {
		return box, err
	}
	envelope, err := cleanupJournalJSON(env.Dump())
	if err != nil || !bytes.Equal(envelope, box.Envelope) {
		return box, seedhost.ErrUnsafe
	}
	state, _, err := j.read(ctx, q)
	if err != nil {
		return box, err
	}
	n, ok := state.Notices[box.Notice.ID]
	if !ok || !bytes.Equal(mustCleanSeedJSON(n), mustCleanSeedJSON(box.Notice)) {
		return box, seedhost.ErrUnsafe
	}
	e, ok := state.Effects[n.ID]
	run, hasRun := state.Runs[n.RunID]
	if !ok || !hasRun || run.Invocation.RunID != n.RunID || e.ID != n.ID || e.RunID != n.RunID || e.Kind != "notice" || e.Phase != "confirmed" || e.Binding != run.Invocation.Binding || !bytes.Equal(e.Args, n.Payload) {
		return box, seedhost.ErrUnsafe
	}
	if err = cleanSeedReadEffectReceipt(ctx, q, j, run.Invocation, e); err != nil {
		return box, err
	}
	receipt, err := cleanSeedReadReceipt(ctx, q, j, n.ID)
	var proof pluginCleanInvalidSeedNoticeReceipt
	if err != nil || cleanupJournalStrict(receipt.Payload, &proof) != nil || proof.Version != 1 || proof.OutboxSHA != tokenHash(string(raw)) {
		return box, seedhost.ErrUnsafe
	}
	if env.Get("save_history") == true {
		expected := "notice_" + hex.EncodeToString(j.mac("notice-id", []byte(n.ID)))
		if box.State != "history_saved" || box.HistoryID != expected || len(box.HistoryDigest) != 32 {
			return box, seedhost.ErrUnsafe
		}
		var publicRaw []byte
		if err = q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='notification' AND id=?`, box.HistoryID).Scan(&publicRaw); err != nil {
			return box, err
		}
		var history Notification
		if cleanupJournalStrict(publicRaw, &history) != nil || history.ID != expected || history.CreatedAt != box.CreatedAt || !hmac.Equal(box.HistoryDigest, cleanSeedNoticeHistoryDigest(j, history)) {
			return box, seedhost.ErrUnsafe
		}
	} else if box.State != "history_skipped" || box.HistoryID != "" || len(box.HistoryDigest) != 0 {
		return box, seedhost.ErrUnsafe
	}
	return box, nil
}
