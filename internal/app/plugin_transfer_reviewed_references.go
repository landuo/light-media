package app

import (
	"context"
	"database/sql"
	"errors"

	transfer "light-media/internal/plugins/torrenttransfer"
)

func (p *PluginStore) transferNamespaceTx(ctx context.Context, tx *sql.Tx, in PluginInstallation) (*reviewedTransferBinding, error) {
	root, _, e := p.transferRootReadTx(ctx, tx, in.InstanceID, in.Generation)
	if e != nil {
		return nil, e
	}
	if root.ReferenceProtocol == "native-downloader-name/v1" {
		return nil, nil
	}
	if root.ReferenceProtocol != reviewedTransferProtocol {
		return nil, bad("TORRENT_TRANSFER_REFERENCES_UNCLAIMED", "原版下载器引用尚未确认映射")
	}
	proof, e := p.readReviewedTransferProof(ctx, tx, in.InstanceID)
	if e != nil {
		return nil, e
	}
	return &proof.Binding, nil
}

// Keep the original select/history name as this instance's stable value, while
// the service itself is always the independently reviewed native downloader ID.
func transferReferenceOptions(settings Settings, binding *reviewedTransferBinding) ([]map[string]any, map[string]string, error) {
	options := []map[string]any{}
	aliases := map[string]string{}
	seen := map[string]bool{}
	for _, d := range settings.Downloaders {
		value := d.Name
		if binding != nil {
			for _, mapped := range binding.Downloaders {
				if d.ID == mapped.NativeID {
					if reviewedIYUUKind(d.Kind) != mapped.Kind {
						return nil, nil, invalidReviewedTransfer()
					}
					value = mapped.Name
					break
				}
			}
			for _, mapped := range binding.Downloaders {
				if value == mapped.Name && d.ID != mapped.NativeID {
					return nil, nil, bad("TORRENT_TRANSFER_NAME_CONFLICT", "下载器名称与原版引用冲突，请先调整名称")
				}
			}
		}
		if binding != nil && seen[value] {
			return nil, nil, bad("TORRENT_TRANSFER_NAME_CONFLICT", "下载器名称不唯一")
		}
		seen[value] = true
		aliases[d.ID] = value
		options = append(options, map[string]any{"value": value, "title": d.Name})
	}
	return options, aliases, nil
}
func (a *App) transferBoundReferences(ctx context.Context, in PluginInstallation, settings Settings) (*reviewedTransferBinding, []map[string]any, map[string]string, error) {
	tx, e := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, nil, nil, e
	}
	defer tx.Rollback()
	var current PluginInstallation
	e = tx.QueryRowContext(ctx, `SELECT plugin_id,version,generation FROM plugin_installations WHERE instance_id=?`, in.InstanceID).Scan(&current.PluginID, &current.Version, &current.Generation)
	if errors.Is(e, sql.ErrNoRows) && in.Generation == 0 {
		// Market installation loads the original default form before creating its
		// first installation. Only this absent instance has no reference owner.
		options, aliases, e := transferReferenceOptions(settings, nil)
		return nil, options, aliases, e
	}
	if e != nil || current.PluginID != in.PluginID || current.Version != in.Version || in.Generation > 0 && in.Generation != current.Generation {
		return nil, nil, nil, invalidReviewedTransfer()
	}
	in.Generation = current.Generation
	binding, e := a.pluginStore.transferNamespaceTx(ctx, tx, in)
	if e != nil {
		return nil, nil, nil, e
	}
	options, aliases, e := transferReferenceOptions(settings, binding)
	return binding, options, aliases, e
}
func transferSetFormReferenceOptions(value any, options []map[string]any) {
	switch v := value.(type) {
	case []any:
		for _, child := range v {
			transferSetFormReferenceOptions(child, options)
		}
	case map[string]any:
		if props, ok := v["props"].(map[string]any); ok {
			if props["model"] == "fromdownloader" || props["model"] == "todownloader" {
				props["items"] = options
			}
		}
		for _, child := range v {
			transferSetFormReferenceOptions(child, options)
		}
	}
}
func (a *App) transferBindingsFor(ctx context.Context, in PluginInstallation, settings Settings) ([]transfer.NativeBinding, error) {
	_, _, aliases, e := a.transferBoundReferences(ctx, in, settings)
	if e != nil {
		return nil, e
	}
	bindings := a.transferBindings(settings)
	for i := range bindings {
		bindings[i].Service.Name = aliases[bindings[i].Service.Ref]
	}
	return bindings, nil
}
