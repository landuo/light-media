package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	transfer "light-media/internal/plugins/torrenttransfer"
)

const transferRootSchema = `CREATE TABLE IF NOT EXISTS plugin_transfer_roots(instance_id TEXT NOT NULL,generation INTEGER NOT NULL,sealed BLOB NOT NULL,PRIMARY KEY(instance_id,generation));`

type transferOwnerRoot struct {
	Version                                int
	Plugin                                 string
	ReferenceProtocol                      string
	ReferenceMigration                     string
	Purpose, Instance, Event, ParentSHA    string
	Generation, ParentGeneration, Sequence int64
	Journal                                transfer.RegistryAnchor
	Runs                                   map[string]transferRunFact
}
type transferOwnerParent struct {
	Generation int64
	SHA        string
}
type transferRegistryAuthority struct {
	p          *PluginStore
	instance   string
	generation int64
}

func transferRootKey(g int64) string { return "transfer-owner-root:v1:" + strconv.FormatInt(g, 10) }
func (p *PluginStore) transferJournalKey(instance string, g int64) []byte {
	h := sha256.New()
	h.Write([]byte("light-media/transfer-native-journal-generation/v1\x00"))
	h.Write(p.store.key)
	b, _ := json.Marshal(struct {
		Instance   string
		Generation int64
	}{instance, g})
	h.Write(b)
	return h.Sum(nil)
}
func (p *PluginStore) transferRootReadTx(ctx context.Context, tx *sql.Tx, instance string, g int64) (transferOwnerRoot, []byte, error) {
	var root transferOwnerRoot
	var sealed []byte
	if e := tx.QueryRowContext(ctx, "SELECT sealed FROM plugin_transfer_roots WHERE instance_id=? AND generation=?", instance, g).Scan(&sealed); e != nil {
		return root, nil, transfer.ErrJournalIntegrity
	}
	raw, e := p.unseal("run_result", instance, transferRootKey(g), sealed)
	if e != nil || cleanupJournalStrict(raw, &root) != nil || root.Version != 1 || root.Purpose != "transfer-owner-root/v1" || root.Instance != instance || root.Generation != g || g < 1 || root.Sequence < 0 || root.Runs == nil || !pluginStorageDigest.MatchString(root.Journal.Namespace) || !pluginStorageDigest.MatchString(root.Journal.Digest) {
		return root, nil, transfer.ErrJournalIntegrity
	}
	var plugin string
	if e = tx.QueryRowContext(ctx, "SELECT plugin_id FROM plugin_installations WHERE instance_id=?", instance).Scan(&plugin); e != nil || root.Plugin != plugin || !isTorrentTransfer(root.Plugin) {
		return root, nil, transfer.ErrJournalIntegrity
	}
	if root.ReferenceProtocol != "native-downloader-name/v1" && root.ReferenceProtocol != "moviepilot-reference-unclaimed/v1" && root.ReferenceProtocol != reviewedTransferProtocol {
		return root, nil, transfer.ErrJournalIntegrity
	}
	if root.ReferenceProtocol == "native-downloader-name/v1" {
		if root.ReferenceMigration != "" {
			return root, nil, transfer.ErrJournalIntegrity
		}
	} else {
		var count int
		if e = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM plugin_migration_runs WHERE id=? AND instance_id=? AND source_plugin_id=? AND state IN ('planned','imported')", root.ReferenceMigration, root.Instance, root.Plugin).Scan(&count); e != nil || count != 1 {
			return root, nil, transfer.ErrJournalIntegrity
		}
		if root.ReferenceProtocol == reviewedTransferProtocol {
			proof, e := p.readReviewedTransferProof(ctx, tx, instance)
			if e != nil || proof.Migration != root.ReferenceMigration || proof.Binding.Generation > g {
				return root, nil, invalidReviewedTransfer()
			}
		}
	}

	if root.Event != "install" && root.Event != "reset" && root.Event != "uninstall" && root.Event != "bulk_uninstall" {
		return root, nil, transfer.ErrJournalIntegrity
	}
	if g == 1 && (root.ParentGeneration != 0 || root.ParentSHA != "" || root.Event != "install") || g > 1 && (root.ParentGeneration != g-1 || !pluginStorageDigest.MatchString(root.ParentSHA)) {
		return root, nil, transfer.ErrJournalIntegrity
	}
	return root, sealed, nil
}

// Validate the independently sealed generation chain against each actual
// authenticated JR namespace. A normal reader can never mint missing roots.
func (p *PluginStore) transferRootChainTx(ctx context.Context, tx *sql.Tx, instance string, g int64, anchor transfer.RegistryAnchor, inspectCurrent bool) (bool, error) {
	expected := ""
	first := true
	unknown, blocked := false, false
	for g > 0 {
		root, _, e := p.transferRootReadTx(ctx, tx, instance, g)
		if e != nil {
			return false, e
		}
		b, _ := json.Marshal(root)
		if expected != "" && expected != tokenHash(string(b)) {
			return false, transfer.ErrJournalIntegrity
		}
		if first && root.Journal != anchor {
			return false, transfer.ErrJournalIntegrity
		}
		if inspectCurrent || !first {
			actual, identities, hasUnknown, e := transfer.InspectJournalTx(ctx, p.store.DB, tx, p.transferJournalKey(instance, g), instance, uint64(g))
			if e != nil {
				return false, e
			}
			if actual != root.Journal {
				return false, transfer.ErrJournalIntegrity
			}
			seen := map[string]bool{}
			for _, identity := range identities {
				fact, ok := root.Runs[identity.Run]
				if !ok || identity.Revision != uint64(fact.Revision) || identity.Fingerprint != fact.InputSHA {
					return false, transfer.ErrJournalIntegrity
				}
				seen[identity.Run] = true
			}
			for id, fact := range root.Runs {
				if fact.Phase == "confirmed" && fact.Action != "init" && fact.Action != "reload" && !seen[id] {
					return false, transfer.ErrJournalIntegrity
				}
			}
			unknown = unknown || hasUnknown
		}
		if e = p.transferValidateDeferredTx(ctx, tx, root); e != nil {
			if !errors.Is(e, transfer.ErrUnconfirmed) {
				return false, e
			}
			blocked = true // A later generation cannot hide older lost evidence.
		}
		first = false
		expected, g = root.ParentSHA, root.ParentGeneration
	}
	if blocked {
		return unknown, transfer.ErrUnconfirmed
	}
	return unknown, nil
}
func (a transferRegistryAuthority) current(ctx context.Context, tx *sql.Tx) error {
	var plugin, version string
	var g int64
	if e := tx.QueryRowContext(ctx, "SELECT plugin_id,version,generation FROM plugin_installations WHERE instance_id=?", a.instance).Scan(&plugin, &version, &g); e != nil {
		return transfer.ErrJournalIntegrity
	}
	if !isTorrentTransfer(plugin) || version != "1.10.3" || g != a.generation {
		return transfer.ErrJournalIntegrity
	}
	return nil
}
func (a transferRegistryAuthority) ValidateRegistry(ctx context.Context, tx *sql.Tx, anchor transfer.RegistryAnchor) error {
	if e := a.current(ctx, tx); e != nil {
		return e
	}
	_, e := a.p.transferRootChainTx(ctx, tx, a.instance, a.generation, anchor, true)
	return e
}
func (a transferRegistryAuthority) CommitRegistry(ctx context.Context, tx *sql.Tx, previous, next transfer.RegistryAnchor) error {
	if e := a.current(ctx, tx); e != nil {
		return e
	}
	// JR child rows have already changed in this transaction. Check the prior
	// immutable root digest and older generations without rereading current rows.
	if _, e := a.p.transferRootChainTx(ctx, tx, a.instance, a.generation, previous, false); e != nil {
		return e
	}
	root, sealed, e := a.p.transferRootReadTx(ctx, tx, a.instance, a.generation)
	if e != nil {
		return e
	}
	if next.Namespace != previous.Namespace {
		return transfer.ErrJournalIntegrity
	}
	if next == previous {
		return nil
	}
	root.Journal = next
	root.Sequence++
	raw, e := json.Marshal(root)
	if e != nil {
		return e
	}
	updated, e := a.p.seal("run_result", a.instance, transferRootKey(a.generation), raw)
	if e != nil {
		return e
	}
	changed, e := tx.ExecContext(ctx, "UPDATE plugin_transfer_roots SET sealed=? WHERE instance_id=? AND generation=? AND sealed=?", updated, a.instance, a.generation, sealed)
	if e != nil {
		return e
	}
	n, e := changed.RowsAffected()
	if e != nil || n != 1 {
		return transfer.ErrJournalIntegrity
	}
	return nil
}
func (p *PluginStore) transferTransitionParentTx(ctx context.Context, tx *sql.Tx, instance string) (transferOwnerParent, error) {
	if _, e := tx.ExecContext(ctx, transferRootSchema+pluginTransferRetirementSchema+transferNoticeSchema); e != nil {
		return transferOwnerParent{}, e
	}
	var plugin, version string
	var g int64
	e := tx.QueryRowContext(ctx, "SELECT plugin_id,version,generation FROM plugin_installations WHERE instance_id=?", instance).Scan(&plugin, &version, &g)
	if errors.Is(e, sql.ErrNoRows) {
		var n int
		if e = tx.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM plugin_transfer_roots WHERE instance_id=?)+(SELECT COUNT(*) FROM plugin_runs WHERE instance_id=?)+(SELECT COUNT(*) FROM plugin_transfer_retirements WHERE instance_id=?)", instance, instance, instance).Scan(&n); e != nil {
			return transferOwnerParent{}, e
		}
		if n != 0 {
			return transferOwnerParent{}, transfer.ErrJournalIntegrity
		}
		return transferOwnerParent{}, nil
	}
	if e != nil {
		return transferOwnerParent{}, e
	}
	if !isTorrentTransfer(plugin) || version != "1.10.3" {
		return transferOwnerParent{}, transfer.ErrJournalIntegrity
	}
	root, _, e := p.transferRootReadTx(ctx, tx, instance, g)
	if e != nil {
		return transferOwnerParent{}, e
	}
	unknown, e := p.transferRootChainTx(ctx, tx, instance, g, root.Journal, true)
	if e != nil {
		return transferOwnerParent{}, e
	}
	if unknown {
		return transferOwnerParent{}, transfer.ErrUnconfirmed
	}
	raw, _ := json.Marshal(root)
	return transferOwnerParent{g, tokenHash(string(raw))}, nil
}
func (p *PluginStore) transferGenesisTx(ctx context.Context, tx *sql.Tx, instance, event string, parent transferOwnerParent) error {
	var plugin, version string
	var g int64
	if e := tx.QueryRowContext(ctx, "SELECT plugin_id,version,generation FROM plugin_installations WHERE instance_id=?", instance).Scan(&plugin, &version, &g); e != nil {
		return e
	}
	if !isTorrentTransfer(plugin) || version != "1.10.3" || g != parent.Generation+1 {
		return transfer.ErrJournalIntegrity
	}
	anchor, e := transfer.CreateJournalGenesisTx(ctx, p.store.DB, tx, p.transferJournalKey(instance, g))
	if e != nil {
		return e
	}
	root := transferOwnerRoot{Version: 1, Plugin: plugin, ReferenceProtocol: "native-downloader-name/v1", Purpose: "transfer-owner-root/v1", Instance: instance, Event: event, Generation: g, ParentGeneration: parent.Generation, ParentSHA: parent.SHA, Journal: anchor, Runs: map[string]transferRunFact{}}
	if parent.Generation > 0 && event != "reset" {
		var retained int
		if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_values WHERE instance_id=?`, instance).Scan(&retained); e != nil {
			return e
		}
		if retained > 0 {
			previous, _, e := p.transferRootReadTx(ctx, tx, instance, parent.Generation)
			if e != nil {
				return e
			}
			root.ReferenceProtocol, root.ReferenceMigration = previous.ReferenceProtocol, previous.ReferenceMigration
		}
	}
	raw, e := json.Marshal(root)
	if e != nil {
		return e
	}
	sealed, e := p.seal("run_result", instance, transferRootKey(g), raw)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, "INSERT INTO plugin_transfer_roots(instance_id,generation,sealed) VALUES(?,?,?)", instance, g, sealed)
	return e
}
func (p *PluginStore) transferLifecycleParentTx(ctx context.Context, tx *sql.Tx, instance string) (transferOwnerParent, bool, error) {
	var plugin string
	if e := tx.QueryRowContext(ctx, "SELECT plugin_id FROM plugin_installations WHERE instance_id=?", instance).Scan(&plugin); e != nil {
		return transferOwnerParent{}, false, e
	}
	if !isTorrentTransfer(plugin) {
		return transferOwnerParent{}, false, nil
	}
	parent, e := p.transferTransitionParentTx(ctx, tx, instance)
	return parent, true, e
}

// Accepted private notices remain verifiable after a later clean generation
// transition. They must be anchored through the current complete root chain;
// this authority can never update a historical registry or dispatch work.
type transferHistoricalRegistryAuthority struct {
	p          *PluginStore
	instance   string
	generation int64
}

func (a transferHistoricalRegistryAuthority) ValidateRegistry(ctx context.Context, tx *sql.Tx, anchor transfer.RegistryAnchor) error {
	var plugin, version string
	var current int64
	if e := tx.QueryRowContext(ctx, "SELECT plugin_id,version,generation FROM plugin_installations WHERE instance_id=?", a.instance).Scan(&plugin, &version, &current); e != nil {
		return transfer.ErrJournalIntegrity
	}
	if !isTorrentTransfer(plugin) || version != "1.10.3" || current < a.generation {
		return transfer.ErrJournalIntegrity
	}
	root, _, e := a.p.transferRootReadTx(ctx, tx, a.instance, current)
	if e != nil {
		return e
	}
	if _, e = a.p.transferRootChainTx(ctx, tx, a.instance, current, root.Journal, true); e != nil {
		return e
	}
	historical, _, e := a.p.transferRootReadTx(ctx, tx, a.instance, a.generation)
	if e != nil {
		return e
	}
	if historical.Journal != anchor {
		return transfer.ErrJournalIntegrity
	}
	return nil
}
func (a transferHistoricalRegistryAuthority) CommitRegistry(context.Context, *sql.Tx, transfer.RegistryAnchor, transfer.RegistryAnchor) error {
	return transfer.ErrJournalIntegrity
}
