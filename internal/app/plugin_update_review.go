package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
)

const pluginUpdateReviewField = "host_update_review"
const pluginUpdateReviewPurpose = "update-explicit-config-save/v1"

type pluginUpdateReview struct {
	Purpose    string `json:"purpose"`
	Instance   string `json:"instance"`
	Plugin     string `json:"plugin"`
	Version    string `json:"version"`
	Generation int64  `json:"generation"`
	Revision   int64  `json:"revision"`
	ConfigSHA  string `json:"config_sha"`
}

func pluginUpdateReviewPending() error {
	return bad("PLUGIN_UPDATE_REVIEW_REQUIRED", "版本已迁移，旧一次性执行意图尚未确认；请打开设置核对并保存，仅确认当前实例")
}

// A missing record is an error only when the authenticated lifecycle marker says
// it is required. Ordinary installations do not acquire an inferred enabled gate.
func pluginUpdateReviewFromFields(in PluginInstallation, marker pluginLifecycleMarker, fields map[string]json.RawMessage) (*pluginUpdateReview, error) {
	raw, present := fields[pluginUpdateReviewField]
	if !marker.UpdateReviewRequired {
		if present {
			return nil, pluginLifecycleInvalid()
		}
		return nil, nil
	}
	if !present || (marker.State != pluginLifecycleCold && marker.State != pluginLifecycleInactive) {
		return nil, pluginLifecycleInvalid()
	}
	var review pluginUpdateReview
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&review) != nil || d.Decode(new(any)) != io.EOF || review.Purpose != pluginUpdateReviewPurpose || review.Instance != in.InstanceID || review.Plugin != in.PluginID || review.Version != in.Version || review.Generation != in.Generation || review.Revision < 0 || len(review.ConfigSHA) != 64 {
		return nil, pluginLifecycleInvalid()
	}
	for _, c := range review.ConfigSHA {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return nil, pluginLifecycleInvalid()
		}
	}
	return &review, nil
}

func pluginUpdateReviewRecord(in PluginInstallation) (*pluginUpdateReview, error) {
	if _, err := pluginLifecycleState(in); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(in.Manifest, &fields)
	var marker pluginLifecycleMarker
	if raw, ok := fields[pluginLifecycleField]; ok {
		_ = json.Unmarshal(raw, &marker)
	}
	return pluginUpdateReviewFromFields(in, marker, fields)
}

func pluginUpdateReviewManifest(in PluginInstallation, state string, revision int64, configSHA string) (json.RawMessage, error) {
	raw, err := pluginLifecycleManifest(in, state)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	var marker pluginLifecycleMarker
	_ = json.Unmarshal(fields[pluginLifecycleField], &marker)
	marker.UpdateReviewRequired = true
	fields[pluginLifecycleField], err = json.Marshal(marker)
	if err != nil {
		return nil, err
	}
	fields[pluginUpdateReviewField], err = json.Marshal(pluginUpdateReview{pluginUpdateReviewPurpose, in.InstanceID, in.PluginID, in.Version, in.Generation, revision, configSHA})
	if err != nil {
		return nil, err
	}
	raw, err = json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	check := in
	check.Manifest = raw
	if _, err = pluginLifecycleState(check); err != nil {
		return nil, err
	}
	return raw, nil
}

func (p *PluginStore) pluginUpdateReviewRead(ctx context.Context, q PluginProofReader, in PluginInstallation) (*pluginUpdateReview, error) {
	review, err := pluginUpdateReviewRecord(in)
	if err != nil || review == nil {
		return review, err
	}
	var revision int64
	var sealed []byte
	if err = q.QueryRowContext(ctx, `SELECT revision,sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, in.InstanceID).Scan(&revision, &sealed); err != nil {
		return nil, pluginLifecycleInvalid()
	}
	raw, err := p.unseal("config", in.InstanceID, "main", sealed)
	if err != nil {
		return nil, err
	}
	if revision != review.Revision || deferredInputSHA(raw) != review.ConfigSHA {
		return nil, pluginLifecycleInvalid()
	}
	return review, nil
}

// The existing install transaction writes clone metadata after its initial config.
// This gate needs only the manifest AEAD identity, not an incomplete clone join.
func (p *PluginStore) pluginUpdateReviewInstallation(ctx context.Context, q PluginProofReader, instance string) (PluginInstallation, error) {
	var in PluginInstallation
	var sealed []byte
	err := q.QueryRowContext(ctx, `SELECT instance_id,plugin_id,version,generation,installed,desired_enabled,manifest FROM plugin_installations WHERE instance_id=?`, instance).Scan(&in.InstanceID, &in.PluginID, &in.Version, &in.Generation, &in.Installed, &in.DesiredEnabled, &sealed)
	if err != nil {
		return in, err
	}
	in.Manifest, err = p.unseal("manifest", instance, "main", sealed)
	return in, err
}

func (p *PluginStore) requirePluginUpdateActivation(ctx context.Context, q PluginProofReader, instance string) error {
	in, err := p.pluginUpdateReviewInstallation(ctx, q, instance)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	review, err := p.pluginUpdateReviewRead(ctx, q, in)
	if err != nil {
		return err
	}
	if review != nil {
		return pluginUpdateReviewPending()
	}
	return nil
}

// Only the authenticated configuration PUT marker can release this gate. The
// manifest change and the subsequent configuration CAS/run-origin write share
// the caller's transaction; any failed save rolls the release back.
func (p *PluginStore) releasePluginUpdateReviewForSaveTx(ctx context.Context, tx *sql.Tx, instance string, expectedRevision int64) error {
	in, err := p.pluginLifecycleInstallation(ctx, tx, instance)
	if err != nil {
		return err
	}
	review, err := p.pluginUpdateReviewRead(ctx, tx, in)
	if err != nil || review == nil {
		return err
	}
	if !in.Installed {
		return pluginUpdateReviewPending()
	}
	authenticated, _ := ctx.Value(pluginConfigSaveRequestKey{}).(string)
	if authenticated != instance {
		return pluginUpdateReviewPending()
	}
	if expectedRevision != review.Revision {
		return bad("PLUGIN_REVISION_CONFLICT", "插件配置已变化，请重新读取后保存")
	}
	return p.writePluginLifecycleTx(ctx, tx, in, pluginLifecycleCold)
}

// Uninstall retains configuration, so its new inactive generation retains the
// review gate as well. A real reset that removed configuration has no remaining
// one-shot intent to protect and clears it. Clone uses a new cold marker and its
// existing explicit enable/enabled=false copying rule, never source authority.
func (p *PluginStore) carryUninstalledUpdateReviewTx(ctx context.Context, tx *sql.Tx, previous, current PluginInstallation) (bool, error) {
	review, err := pluginUpdateReviewRecord(previous)
	if err != nil || review == nil || current.Installed {
		return false, err
	}
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, current.InstanceID).Scan(&exists); err != nil {
		return false, err
	}
	if exists == 0 {
		return false, nil
	}
	if _, err = p.pluginUpdateReviewRead(ctx, tx, previous); err != nil {
		return false, err
	}
	raw, err := pluginUpdateReviewManifest(current, pluginLifecycleInactive, review.Revision, review.ConfigSHA)
	if err != nil {
		return false, err
	}
	sealed, err := p.seal("manifest", current.InstanceID, "main", raw)
	if err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE plugin_installations SET manifest=? WHERE instance_id=? AND generation=? AND installed=0`, sealed, current.InstanceID, current.Generation)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return false, pluginLifecycleInvalid()
	}
	return true, nil
}
