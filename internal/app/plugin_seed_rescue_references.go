package app

import (
	"context"
	"database/sql"

	sr "light-media/internal/plugins/seedrescue"
)

// Browser select values must remain exact JavaScript integers as well as Go ints.
const seedRescueMaximumAlias int64 = 1<<53 - 1

func (p *PluginStore) seedRescueNamespaceTx(ctx context.Context, q pluginRemoveLinkJournalReader, instance string) (*reviewedSeedBinding, error) {
	in := PluginInstallation{InstanceID: instance}
	if e := q.QueryRowContext(ctx, `SELECT plugin_id,version,generation FROM plugin_installations WHERE instance_id=?`, instance).Scan(&in.PluginID, &in.Version, &in.Generation); e != nil {
		return nil, e
	}
	if in.PluginID != sr.PluginID || in.Version != sr.Version {
		return nil, invalidReviewedSeed()
	}
	protocol, e := p.readSiteReferenceProtocol(ctx, q, in)
	if e != nil {
		return nil, e
	}
	if protocol.Protocol == sourceSiteReferenceProtocol {
		return nil, nil
	}
	proof, e := p.readReviewedSeedProof(ctx, q)
	if e != nil {
		return nil, e
	}
	return &proof.Binding, nil
}

// A retained legacy namespace follows a proven installation transition. Reset
// removes the old values and creates its normal empty native namespace instead.
func (p *PluginStore) inheritSeedReferenceProtocolTx(ctx context.Context, tx *sql.Tx, in PluginInstallation, next siteReferenceProtocol) (siteReferenceProtocol, error) {
	var retained int
	if e := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_values WHERE instance_id=?`, in.InstanceID).Scan(&retained); e != nil {
		return next, e
	}
	if retained == 0 {
		return next, nil
	}
	var previous int64
	if e := tx.QueryRowContext(ctx, `SELECT generation FROM native_site_reference_protocols WHERE instance_id=? AND generation<? ORDER BY generation DESC LIMIT 1`, in.InstanceID, in.Generation).Scan(&previous); e != nil {
		return next, e
	}
	old := in
	old.Generation = previous
	protocol, e := p.readSiteReferenceProtocol(ctx, tx, old)
	if e != nil {
		return next, e
	}
	if protocol.Protocol == reviewedSeedProtocol {
		next.Protocol, next.Source = protocol.Protocol, protocol.Source
	}
	return next, nil
}
func seedRescueSiteAlias(binding *reviewedSeedBinding, id nativeSiteIdentity) (int64, error) {
	if binding == nil {
		return id.SourceID, nil
	}
	for _, s := range binding.Sites {
		if s.SiteID == id.SiteID {
			if s.NativeID != id.SourceID {
				return 0, siteIdentityError()
			}
			return s.OriginalID, nil
		}
	}
	if id.SourceID < 1 || id.SourceID > seedRescueMaximumAlias-binding.ReservedCeiling {
		return 0, siteIdentityError()
	}
	return binding.ReservedCeiling + id.SourceID, nil
}
func (p *PluginStore) seedRescueSiteIdentityTx(ctx context.Context, q pluginRemoveLinkJournalReader, binding *reviewedSeedBinding, alias int64) (nativeSiteIdentity, error) {
	if alias < 1 || alias > seedRescueMaximumAlias {
		return nativeSiteIdentity{}, siteIdentityError()
	}
	native := alias
	siteID := ""
	if binding != nil {
		if alias <= binding.ReservedCeiling {
			native = 0
			for _, s := range binding.Sites {
				if s.OriginalID == alias {
					native, siteID = s.NativeID, s.SiteID
					break
				}
			}
			if native == 0 {
				return nativeSiteIdentity{}, sql.ErrNoRows
			}
		} else {
			native = alias - binding.ReservedCeiling
			for _, s := range binding.Sites {
				if s.NativeID == native {
					return nativeSiteIdentity{}, siteIdentityError()
				}
			}
		}
	}
	out, e := p.store.readNativeSiteIdentity(ctx, q, "source_id", native)
	if e != nil {
		return out, e
	}
	if siteID != "" && out.SiteID != siteID {
		return out, siteIdentityError()
	}
	return out, nil
}
func seedRescueDownloaderOptions(settings Settings, binding *reviewedSeedBinding) ([]sr.Object, error) {
	out := []sr.Object{}
	values := map[string]bool{}
	for _, d := range settings.Downloaders {
		if !downloaderEnabled(d) || seedRescueNativeConfig(d).Kind != "qbittorrent" {
			continue
		}
		value := d.Name
		if binding != nil {
			if d.ID == binding.DownloaderID {
				value = binding.DownloaderName
			} else if value == binding.DownloaderName {
				return nil, bad("SEED_RESCUE_DOWNLOADER_UNBOUND", "下载器名称与原版引用冲突，请先调整名称")
			}
		}
		if values[value] {
			return nil, bad("SEED_RESCUE_DOWNLOADER_UNBOUND", "下载器名称不唯一")
		}
		values[value] = true
		out = append(out, sr.Object{"value": value, "title": d.Name})
	}
	return out, nil
}
func (o *pluginSeedRescueOwner) boundDownloader(ctx context.Context, name string) (DownloaderConfig, error) {
	tx, e := o.a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return DownloaderConfig{}, e
	}
	defer tx.Rollback()
	binding, e := o.a.pluginStore.seedRescueNamespaceTx(ctx, tx, o.instance)
	if e != nil {
		return DownloaderConfig{}, e
	}
	settings := o.a.Settings()
	if binding == nil {
		return seedRescueDownloader(settings, name)
	}
	if _, e = seedRescueDownloaderOptions(settings, binding); e != nil {
		return DownloaderConfig{}, e
	}
	if name != binding.DownloaderName {
		d, e := seedRescueDownloader(settings, name)
		if e == nil && d.ID == binding.DownloaderID {
			return DownloaderConfig{}, bad("SEED_RESCUE_DOWNLOADER_UNBOUND", "下载器选择不是该实例的原版引用")
		}
		return d, e
	}
	for _, d := range settings.Downloaders {
		if d.ID == binding.DownloaderID && downloaderEnabled(d) && seedRescueNativeConfig(d).Kind == "qbittorrent" {
			return d, nil
		}
	}
	return DownloaderConfig{}, bad("SEED_RESCUE_DOWNLOADER_UNBOUND", "原版绑定的下载器已删除或停用")
}
