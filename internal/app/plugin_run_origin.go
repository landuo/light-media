package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// This marker is created only after the management HTTP authentication and
// same-origin checks. Neither a worker payload nor an init input can create it.
type pluginConfigSaveRequestKey struct{}

func pluginConfigSaveRequest(r *http.Request) *http.Request {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/"), "/")
	if r.Method == http.MethodPut && len(parts) == 3 && parts[0] == "plugins" && parts[2] == "config" && pluginStorageID.MatchString(parts[1]) {
		return r.WithContext(context.WithValue(r.Context(), pluginConfigSaveRequestKey{}, parts[1]))
	}
	return r
}

type pluginRunOrigin struct {
	Schema     int    `json:"schema"`
	Kind       string `json:"kind"`
	Run        string `json:"run"`
	Instance   string `json:"instance"`
	Plugin     string `json:"plugin"`
	Version    string `json:"version"`
	Generation int64  `json:"generation"`
	Revision   int64  `json:"revision"`
	Action     string `json:"action"`
	InputSHA   string `json:"input_sha"`
	ConfigSHA  string `json:"config_sha"`
}

type pluginRunConfigTransition struct {
	Schema        int    `json:"schema"`
	Run           string `json:"run"`
	Instance      string `json:"instance"`
	Plugin        string `json:"plugin"`
	Version       string `json:"version"`
	Generation    int64  `json:"generation"`
	Action        string `json:"action"`
	FromRevision  int64  `json:"from_revision"`
	ToRevision    int64  `json:"to_revision"`
	FromInputSHA  string `json:"from_input_sha"`
	ToInputSHA    string `json:"to_input_sha"`
	FromConfigSHA string `json:"from_config_sha"`
	ToConfigSHA   string `json:"to_config_sha"`
}

func (p *PluginStore) recordRunOriginTx(ctx context.Context, tx *sql.Tx, plugin string, run PluginRun, config json.RawMessage) error {
	if err := (*PluginStore)(nil).checkPluginFormWrite(ctx, "", pluginFormWriteLifecycle); err != nil {
		return err
	}
	instance, _ := ctx.Value(pluginConfigSaveRequestKey{}).(string)
	if instance != run.InstanceID || run.Action != "init" {
		return nil
	}
	origin := pluginRunOrigin{1, "config-save", run.ID, run.InstanceID, plugin, run.Version, run.Generation, run.ConfigRevision, run.Action, deferredInputSHA(run.Input), deferredInputSHA(config)}
	raw, err := json.Marshal(origin)
	if err != nil {
		return err
	}
	sealed, err := p.seal("run_origin", run.InstanceID, run.ID, raw)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_run_origins(run_id,sealed) VALUES(?,?)`, run.ID, sealed)
	return err
}

// recordRunConfigTransitionTx is called only by the framework's transactional
// configuration propagation. It never rewrites the original run origin or an
// accepted external-operation receipt. Unique edges prevent forked history.
func (p *PluginStore) recordRunConfigTransitionTx(ctx context.Context, tx *sql.Tx, plugin string, run PluginRun, nextRevision int64, nextInput, previousConfig, nextConfig json.RawMessage) error {
	if err := p.checkPluginFormConfigProjection(ctx, tx, run.InstanceID); err != nil {
		return err
	}
	if nextRevision <= run.ConfigRevision {
		return bad("PLUGIN_RUN_PROOF_INVALID", "插件配置修订链无效")
	}
	proof := pluginRunConfigTransition{1, run.ID, run.InstanceID, plugin, run.Version, run.Generation, run.Action, run.ConfigRevision, nextRevision, deferredInputSHA(run.Input), deferredInputSHA(nextInput), deferredInputSHA(previousConfig), deferredInputSHA(nextConfig)}
	raw, err := json.Marshal(proof)
	if err != nil {
		return err
	}
	key := run.ID + ":" + strconv.FormatInt(run.ConfigRevision, 10)
	sealed, err := p.seal("run_config_transition", run.InstanceID, key, raw)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_run_config_transitions(run_id,from_revision,to_revision,sealed) VALUES(?,?,?,?)`, run.ID, run.ConfigRevision, nextRevision, sealed)
	return err
}

// verifyRunConfigChainTx proves an immutable authority snapshot reaches the
// exact current invocation, including pending invocations whose input changed.
// Missing, corrupted, discontinuous, cross-instance or forked edges fail closed.
func (p *PluginStore) verifyRunConfigChainTx(ctx context.Context, tx PluginProofReader, plugin string, run PluginRun, revision int64, inputSHA, configSHA string) (string, error) {
	if revision < 0 || revision > run.ConfigRevision {
		return "", bad("PLUGIN_RUN_PROOF_INVALID", "插件配置修订链无效")
	}
	for steps := 0; revision < run.ConfigRevision; steps++ {
		if steps >= 4096 {
			return "", bad("PLUGIN_RUN_PROOF_INVALID", "插件配置修订链超出验证范围")
		}
		var sealed []byte
		if err := tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_run_config_transitions WHERE run_id=? AND from_revision=?`, run.ID, revision).Scan(&sealed); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", bad("PLUGIN_RUN_PROOF_INVALID", "插件配置修订证明缺失")
			}
			return "", err
		}
		raw, err := p.unseal("run_config_transition", run.InstanceID, run.ID+":"+strconv.FormatInt(revision, 10), sealed)
		if err != nil {
			return "", err
		}
		var edge pluginRunConfigTransition
		if json.Unmarshal(raw, &edge) != nil || edge.Schema != 1 || edge.Run != run.ID || edge.Instance != run.InstanceID || edge.Plugin != plugin || edge.Version != run.Version || edge.Generation != run.Generation || edge.Action != run.Action || edge.FromRevision != revision || edge.FromInputSHA != inputSHA || edge.ToRevision <= revision || edge.ToRevision > run.ConfigRevision || !pluginStorageDigest.MatchString(edge.FromConfigSHA) || !pluginStorageDigest.MatchString(edge.ToConfigSHA) || (configSHA != "" && edge.FromConfigSHA != configSHA) {
			return "", bad("PLUGIN_RUN_PROOF_INVALID", "插件配置修订证明不匹配")
		}
		revision, inputSHA, configSHA = edge.ToRevision, edge.ToInputSHA, edge.ToConfigSHA
	}
	if inputSHA != deferredInputSHA(run.Input) {
		return "", bad("PLUGIN_RUN_PROOF_INVALID", "插件执行输入证明不匹配")
	}
	return configSHA, nil
}

// ConfigSaveOrigin is a trusted host capability, not a worker-controlled flag.
// Only a live invocation for the current installed snapshot may consume it.
// Old runs without an authenticated origin are deliberately NOT config saves.
func (p *PluginStore) ConfigSaveOrigin(ctx context.Context, run PluginRun) (bool, error) {
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := p.configSaveOriginTx(ctx, tx, run)
	if err != nil {
		return false, err
	}
	return result, tx.Commit()
}

// configSaveOriginTx supports trusted host adapters already inside the same
// store transaction. It does not commit or open a nested connection.
func (p *PluginStore) configSaveOriginTx(ctx context.Context, tx *sql.Tx, run PluginRun) (bool, error) {
	actual, err := p.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, run.ID))
	if err != nil {
		return false, err
	}
	if actual.State != "running" || actual.InstanceID != run.InstanceID || actual.Version != run.Version || actual.Generation != run.Generation || actual.ConfigRevision != run.ConfigRevision || actual.Action != run.Action || !bytes.Equal(actual.Input, run.Input) {
		return false, bad("PLUGIN_RUN_PROOF_INVALID", "插件执行身份已变化")
	}
	var plugin, version string
	var generation, revision int64
	var installed, enabled bool
	var configSealed []byte
	err = tx.QueryRowContext(ctx, `SELECT i.plugin_id,i.version,i.generation,i.installed,i.desired_enabled,v.revision,v.sealed FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=?`, run.InstanceID).Scan(&plugin, &version, &generation, &installed, &enabled, &revision, &configSealed)
	if err != nil {
		return false, err
	}
	if !installed || !enabled || version != run.Version || generation != run.Generation || revision != run.ConfigRevision {
		return false, bad("PLUGIN_RUN_PROOF_INVALID", "插件实例快照已变化")
	}
	if _, err = p.instanceMetadata(ctx, tx, run.InstanceID); err != nil {
		return false, err
	}
	var sealed []byte
	err = tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_run_origins WHERE run_id=?`, run.ID).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	raw, err := p.unseal("run_origin", run.InstanceID, run.ID, sealed)
	if err != nil {
		return false, err
	}
	var origin pluginRunOrigin
	if json.Unmarshal(raw, &origin) != nil || origin.Schema != 1 || origin.Kind != "config-save" || origin.Run != run.ID || origin.Instance != run.InstanceID || origin.Plugin != plugin || origin.Version != run.Version || origin.Generation != run.Generation || origin.Action != "init" || run.Action != "init" || !pluginStorageDigest.MatchString(origin.ConfigSHA) {
		return false, bad("PLUGIN_RUN_PROOF_INVALID", "插件保存来源证明不匹配")
	}
	// A retained successor proves the SQL head was replayed backwards. Do not
	// mistake an authenticated historical prefix for current authority.
	var successor bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM plugin_run_config_transitions WHERE run_id=? AND from_revision>=?)`, run.ID, run.ConfigRevision).Scan(&successor); err != nil {
		return false, err
	}
	if successor {
		return false, bad("PLUGIN_RUN_PROOF_INVALID", "插件配置修订头已回退")
	}
	configSHA, err := p.verifyRunConfigChainTx(ctx, tx, plugin, run, origin.Revision, origin.InputSHA, origin.ConfigSHA)
	if err != nil {
		return false, err
	}
	config, err := p.unseal("config", run.InstanceID, "main", configSealed)
	if err != nil {
		return false, err
	}
	if deferredInputSHA(config) != configSHA {
		return false, bad("PLUGIN_RUN_PROOF_INVALID", "插件配置内容证明不匹配")
	}
	return true, nil
}
