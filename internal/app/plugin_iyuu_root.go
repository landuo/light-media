package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	iyuu "light-media/internal/plugins/iyuuautoseed"
)

const iyuuRootSchema = `CREATE TABLE IF NOT EXISTS plugin_iyuu_roots(instance_id TEXT NOT NULL,generation INTEGER NOT NULL,sealed BLOB NOT NULL,PRIMARY KEY(instance_id,generation));`

type iyuuOwnerRoot struct {
	Version                                int
	Purpose, Instance, Event, ParentSHA    string
	Generation, ParentGeneration, Sequence int64
	Journal                                iyuu.RegistryAnchor
	Runs                                   map[string]iyuuRunFact
}
type iyuuOwnerParent struct {
	Generation int64
	SHA        string
}
type iyuuRegistryAuthority struct {
	p          *PluginStore
	instance   string
	generation int64
}

func iyuuRootKey(g int64) string { return "iyuu-owner-root:v1:" + strconv.FormatInt(g, 10) }
func (p *PluginStore) iyuuJournalKey(instance string, g int64) []byte {
	h := sha256.New()
	h.Write([]byte("light-media/iyuu-native-journal-generation/v1\x00"))
	h.Write(p.store.key)
	b, _ := json.Marshal(struct {
		Instance   string
		Generation int64
	}{instance, g})
	h.Write(b)
	return h.Sum(nil)
}
func (p *PluginStore) iyuuRootReadTx(ctx context.Context, tx *sql.Tx, instance string, g int64) (iyuuOwnerRoot, []byte, error) {
	var root iyuuOwnerRoot
	var sealed []byte
	if e := tx.QueryRowContext(ctx, "SELECT sealed FROM plugin_iyuu_roots WHERE instance_id=? AND generation=?", instance, g).Scan(&sealed); e != nil {
		return root, nil, iyuu.ErrJournalIntegrity
	}
	raw, e := p.unseal("run_result", instance, iyuuRootKey(g), sealed)
	if e != nil || cleanupJournalStrict(raw, &root) != nil || root.Version != 1 || root.Purpose != "iyuu-owner-root/v1" || root.Instance != instance || root.Generation != g || g < 1 || root.Sequence < 0 || root.Runs == nil || !pluginStorageDigest.MatchString(root.Journal.Namespace) || !pluginStorageDigest.MatchString(root.Journal.Digest) {
		return root, nil, iyuu.ErrJournalIntegrity
	}
	if root.Event != "install" && root.Event != "reset" && root.Event != "uninstall" && root.Event != "bulk_uninstall" {
		return root, nil, iyuu.ErrJournalIntegrity
	}
	if g == 1 && (root.ParentGeneration != 0 || root.ParentSHA != "" || root.Event != "install") || g > 1 && (root.ParentGeneration != g-1 || !pluginStorageDigest.MatchString(root.ParentSHA)) {
		return root, nil, iyuu.ErrJournalIntegrity
	}
	return root, sealed, nil
}

// Validate the independently sealed generation chain against each actual
// authenticated JR namespace. A normal reader can never mint missing roots.
func (p *PluginStore) iyuuRootChainTx(ctx context.Context, tx *sql.Tx, instance string, g int64, anchor iyuu.RegistryAnchor, inspectCurrent bool) (bool, error) {
	expected := ""
	first := true
	unknown, blocked := false, false
	for g > 0 {
		root, _, e := p.iyuuRootReadTx(ctx, tx, instance, g)
		if e != nil {
			return false, e
		}
		b, _ := json.Marshal(root)
		if expected != "" && expected != tokenHash(string(b)) {
			return false, iyuu.ErrJournalIntegrity
		}
		if first && root.Journal != anchor {
			return false, iyuu.ErrJournalIntegrity
		}
		if inspectCurrent || !first {
			actual, identities, hasUnknown, e := iyuu.InspectJournalTx(ctx, p.store.DB, tx, p.iyuuJournalKey(instance, g), instance, uint64(g))
			if e != nil {
				return false, e
			}
			if actual != root.Journal {
				return false, iyuu.ErrJournalIntegrity
			}
			seen := map[string]bool{}
			for _, identity := range identities {
				fact, ok := root.Runs[identity.Run]
				if !ok || identity.Revision != uint64(fact.Revision) || identity.Fingerprint != fact.InputSHA {
					return false, iyuu.ErrJournalIntegrity
				}
				seen[identity.Run] = true
			}
			for id, fact := range root.Runs {
				if fact.Phase == "confirmed" && fact.Action != "init" && fact.Action != "reload" && !seen[id] {
					return false, iyuu.ErrJournalIntegrity
				}
			}
			unknown = unknown || hasUnknown
		}
		if e = p.iyuuValidateDeferredTx(ctx, tx, root); e != nil {
			if !errors.Is(e, iyuu.ErrUnconfirmed) {
				return false, e
			}
			blocked = true // A later generation cannot hide older lost evidence.
		}
		first = false
		expected, g = root.ParentSHA, root.ParentGeneration
	}
	if blocked {
		return unknown, iyuu.ErrUnconfirmed
	}
	return unknown, nil
}
func (a iyuuRegistryAuthority) current(ctx context.Context, tx *sql.Tx) error {
	var plugin, version string
	var g int64
	if e := tx.QueryRowContext(ctx, "SELECT plugin_id,version,generation FROM plugin_installations WHERE instance_id=?", a.instance).Scan(&plugin, &version, &g); e != nil {
		return iyuu.ErrJournalIntegrity
	}
	if plugin != "IYUUAutoSeed" || version != "2.19" || g != a.generation {
		return iyuu.ErrJournalIntegrity
	}
	return nil
}
func (a iyuuRegistryAuthority) ValidateRegistry(ctx context.Context, tx *sql.Tx, anchor iyuu.RegistryAnchor) error {
	if e := a.current(ctx, tx); e != nil {
		return e
	}
	_, e := a.p.iyuuRootChainTx(ctx, tx, a.instance, a.generation, anchor, true)
	return e
}
func (a iyuuRegistryAuthority) CommitRegistry(ctx context.Context, tx *sql.Tx, previous, next iyuu.RegistryAnchor) error {
	if e := a.current(ctx, tx); e != nil {
		return e
	}
	// JR child rows have already changed in this transaction. Check the prior
	// immutable root digest and older generations without rereading current rows.
	if _, e := a.p.iyuuRootChainTx(ctx, tx, a.instance, a.generation, previous, false); e != nil {
		return e
	}
	root, sealed, e := a.p.iyuuRootReadTx(ctx, tx, a.instance, a.generation)
	if e != nil {
		return e
	}
	if next.Namespace != previous.Namespace {
		return iyuu.ErrJournalIntegrity
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
	updated, e := a.p.seal("run_result", a.instance, iyuuRootKey(a.generation), raw)
	if e != nil {
		return e
	}
	changed, e := tx.ExecContext(ctx, "UPDATE plugin_iyuu_roots SET sealed=? WHERE instance_id=? AND generation=? AND sealed=?", updated, a.instance, a.generation, sealed)
	if e != nil {
		return e
	}
	n, e := changed.RowsAffected()
	if e != nil || n != 1 {
		return iyuu.ErrJournalIntegrity
	}
	return nil
}
func (p *PluginStore) iyuuTransitionParentTx(ctx context.Context, tx *sql.Tx, instance string) (iyuuOwnerParent, error) {
	if _, e := tx.ExecContext(ctx, iyuuRootSchema+pluginIYUURetirementSchema); e != nil {
		return iyuuOwnerParent{}, e
	}
	var plugin, version string
	var g int64
	e := tx.QueryRowContext(ctx, "SELECT plugin_id,version,generation FROM plugin_installations WHERE instance_id=?", instance).Scan(&plugin, &version, &g)
	if errors.Is(e, sql.ErrNoRows) {
		var n int
		if e = tx.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM plugin_iyuu_roots WHERE instance_id=?)+(SELECT COUNT(*) FROM plugin_runs WHERE instance_id=?)+(SELECT COUNT(*) FROM plugin_iyuu_retirements WHERE instance_id=?)", instance, instance, instance).Scan(&n); e != nil {
			return iyuuOwnerParent{}, e
		}
		if n != 0 {
			return iyuuOwnerParent{}, iyuu.ErrJournalIntegrity
		}
		return iyuuOwnerParent{}, nil
	}
	if e != nil {
		return iyuuOwnerParent{}, e
	}
	if plugin != "IYUUAutoSeed" || version != "2.19" {
		return iyuuOwnerParent{}, iyuu.ErrJournalIntegrity
	}
	root, _, e := p.iyuuRootReadTx(ctx, tx, instance, g)
	if e != nil {
		return iyuuOwnerParent{}, e
	}
	unknown, e := p.iyuuRootChainTx(ctx, tx, instance, g, root.Journal, true)
	if e != nil {
		return iyuuOwnerParent{}, e
	}
	if unknown {
		return iyuuOwnerParent{}, iyuu.ErrUnconfirmed
	}
	raw, _ := json.Marshal(root)
	return iyuuOwnerParent{g, tokenHash(string(raw))}, nil
}
func (p *PluginStore) iyuuGenesisTx(ctx context.Context, tx *sql.Tx, instance, event string, parent iyuuOwnerParent) error {
	var plugin, version string
	var g int64
	if e := tx.QueryRowContext(ctx, "SELECT plugin_id,version,generation FROM plugin_installations WHERE instance_id=?", instance).Scan(&plugin, &version, &g); e != nil {
		return e
	}
	if plugin != "IYUUAutoSeed" || version != "2.19" || g != parent.Generation+1 {
		return iyuu.ErrJournalIntegrity
	}
	anchor, e := iyuu.CreateJournalGenesisTx(ctx, p.store.DB, tx, p.iyuuJournalKey(instance, g))
	if e != nil {
		return e
	}
	root := iyuuOwnerRoot{Version: 1, Purpose: "iyuu-owner-root/v1", Instance: instance, Event: event, Generation: g, ParentGeneration: parent.Generation, ParentSHA: parent.SHA, Journal: anchor, Runs: map[string]iyuuRunFact{}}
	raw, e := json.Marshal(root)
	if e != nil {
		return e
	}
	sealed, e := p.seal("run_result", instance, iyuuRootKey(g), raw)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, "INSERT INTO plugin_iyuu_roots(instance_id,generation,sealed) VALUES(?,?,?)", instance, g, sealed)
	return e
}
func (p *PluginStore) iyuuLifecycleParentTx(ctx context.Context, tx *sql.Tx, instance string) (iyuuOwnerParent, bool, error) {
	var plugin string
	if e := tx.QueryRowContext(ctx, "SELECT plugin_id FROM plugin_installations WHERE instance_id=?", instance).Scan(&plugin); e != nil {
		return iyuuOwnerParent{}, false, e
	}
	if plugin != "IYUUAutoSeed" {
		return iyuuOwnerParent{}, false, nil
	}
	parent, e := p.iyuuTransitionParentTx(ctx, tx, instance)
	return parent, true, e
}

// Accepted private notices remain verifiable after a later clean generation
// transition. They must be anchored through the current complete root chain;
// this authority can never update a historical registry or dispatch work.
type iyuuHistoricalRegistryAuthority struct {
	p          *PluginStore
	instance   string
	generation int64
}

func (a iyuuHistoricalRegistryAuthority) ValidateRegistry(ctx context.Context, tx *sql.Tx, anchor iyuu.RegistryAnchor) error {
	var plugin, version string
	var current int64
	if e := tx.QueryRowContext(ctx, "SELECT plugin_id,version,generation FROM plugin_installations WHERE instance_id=?", a.instance).Scan(&plugin, &version, &current); e != nil {
		return iyuu.ErrJournalIntegrity
	}
	if plugin != "IYUUAutoSeed" || version != "2.19" || current < a.generation {
		return iyuu.ErrJournalIntegrity
	}
	root, _, e := a.p.iyuuRootReadTx(ctx, tx, a.instance, current)
	if e != nil {
		return e
	}
	if _, e = a.p.iyuuRootChainTx(ctx, tx, a.instance, current, root.Journal, true); e != nil {
		return e
	}
	historical, _, e := a.p.iyuuRootReadTx(ctx, tx, a.instance, a.generation)
	if e != nil {
		return e
	}
	if historical.Journal != anchor {
		return iyuu.ErrJournalIntegrity
	}
	return nil
}
func (a iyuuHistoricalRegistryAuthority) CommitRegistry(context.Context, *sql.Tx, iyuu.RegistryAnchor, iyuu.RegistryAnchor) error {
	return iyuu.ErrJournalIntegrity
}
