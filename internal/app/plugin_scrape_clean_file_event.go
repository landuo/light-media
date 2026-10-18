package app

import (
	"bytes"
	"context"
	"encoding/hex"
)

// The opaque bus reference is not authority. Reopen the exact producer's
// authenticated journal, confirmed effect, core receipt and same-TX handoff.
// RemoveLink and Scrape never accept one another's sealed producer records.
func (p *PluginStore) scrapeDownloadFileEvent(ctx context.Context, q pluginRemoveLinkJournalReader, eventID string) (pluginDownloadFileEvent, error) {
	var out pluginDownloadFileEvent
	if !pluginFileEventIDPattern.MatchString(eventID) {
		return out, scrapeJournalCorrupt()
	}
	var instance string
	var generation int64
	var sealed []byte
	if err := q.QueryRowContext(ctx, `SELECT instance_id,generation,sealed FROM plugin_download_file_events WHERE file_event_id=?`, eventID).Scan(&instance, &generation, &sealed); err != nil {
		return out, err
	}
	raw, err := p.unseal("scrape_download_file_event", instance, eventID, sealed)
	if err != nil {
		return out, err
	}
	if scrapeJournalStrict(raw, &out) != nil || out.Version != 1 || out.Hash != "" || out.ID != eventID || out.Instance != instance || out.Generation != generation || out.Generation < 1 || out.Revision < 1 || out.EffectID == "" || !importPath(out.Src) {
		return out, scrapeJournalCorrupt()
	}
	j := &pluginScrapeCleanJournal{p: p, instance: instance, generation: generation}
	state, _, _, err := j.read(ctx, q)
	if err != nil {
		return out, err
	}
	x, ok := state.Effects[out.EffectID]
	if !ok || x.ID != out.EffectID || x.Binding.InstanceID != instance || x.Binding.Generation != generation || x.Binding.Revision != out.Revision || x.Path != out.Src || x.Kind != "download_file_deleted" || x.State != "confirmed" || !x.Result {
		return out, scrapeJournalCorrupt()
	}
	e := &pluginScrapeCleanEffects{j: j}
	token := e.token(x.ID)
	if eventID != "sfe_"+hex.EncodeToString(j.mac("file-event", []byte(token))) {
		return out, scrapeJournalCorrupt()
	}
	receipt, err := e.receipt(ctx, q, x)
	if err != nil || !receipt.Result {
		if err != nil {
			return out, err
		}
		return out, scrapeJournalCorrupt()
	}
	var boxSealed, handoffSealed []byte
	if err = q.QueryRowContext(ctx, `SELECT sealed FROM plugin_scrape_private_outbox WHERE instance_id=? AND generation=? AND token=?`, instance, generation, token).Scan(&boxSealed); err != nil {
		return out, err
	}
	boxRaw, err := p.unseal("scrape_journal", instance, e.aad("outbox", token), boxSealed)
	if err != nil {
		return out, err
	}
	var box pluginScrapeCleanPrivateOutbox
	if scrapeJournalStrict(boxRaw, &box) != nil || box.Version != 1 || box.State != "published" {
		return out, scrapeJournalCorrupt()
	}
	if err = q.QueryRowContext(ctx, `SELECT sealed FROM plugin_scrape_outbox_handoffs WHERE instance_id=? AND generation=? AND token=?`, instance, generation, token).Scan(&handoffSealed); err != nil {
		return out, err
	}
	handoffRaw, err := p.unseal("scrape_journal", instance, e.aad("handoff", token), handoffSealed)
	if err != nil {
		return out, err
	}
	var h pluginScrapeHandoff
	_, identity, err := e.identity(x)
	if err != nil || scrapeJournalStrict(handoffRaw, &h) != nil || h.Version != 1 || h.Kind != x.Kind || h.Stage != "published" || h.Reference != eventID || !bytes.Equal(identity, h.Effect) || !bytes.Equal(identity, box.Effect) {
		return out, scrapeJournalCorrupt()
	}
	return out, nil
}
