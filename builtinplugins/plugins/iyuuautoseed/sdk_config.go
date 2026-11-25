package iyuuautoseed

import (
	"context"
	"encoding/json"
	"strconv"

	sdk "light-media/builtinplugins/internal/sdk"
)

// formFromHost assembles the source's native Vuetify form from the current
// host inventory. Custom site configuration is private; only names and IDs
// are copied into the form.
func formFromHost(ctx context.Context, host sdk.HostCaller) (json.RawMessage, error) {
	sites := sdk.Sites{Host: host}
	custom, err := customSiteOptions(ctx, sites)
	if err != nil {
		return nil, err
	}
	managed, err := managedSiteOptions(ctx, sites)
	if err != nil {
		return nil, err
	}
	downloaders := sdk.Downloaders{Host: host}
	names, err := downloaderFormNames(ctx, downloaders)
	if err != nil {
		return nil, err
	}
	destinations, err := downloaderFormNames(ctx, downloaders)
	if err != nil {
		return nil, err
	}
	form, model, err := Form(FormInventory{
		Sites: managed, CustomSites: custom, Downloaders: names, DestinationDownloaders: destinations,
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal([]any{form, model})
}

func managedSiteOptions(ctx context.Context, sites sdk.Sites) ([]SiteOption, error) {
	reply, err := sites.Read(ctx, sdk.SiteHostRequestV1{OrderByPriority: true, SourceProjection: true})
	if err != nil {
		return nil, err
	}
	out := make([]SiteOption, 0, len(reply.Items))
	for _, row := range reply.Items {
		if len(row.SourceFields) == 0 {
			var id any = row.ID
			if row.SourceID != 0 {
				id = json.Number(strconv.FormatInt(row.SourceID, 10))
			}
			out = append(out, SiteOption{ID: id, Name: row.Name})
			continue
		}
		fields, err := decodeJSON(row.SourceFields)
		if err != nil {
			return nil, err
		}
		site, ok := fields.(map[string]any)
		if !ok {
			return nil, sourceFault("AttributeError", "managed site projection")
		}
		id, hasID := site["id"]
		name, hasName := site["name"]
		if !hasID || !hasName {
			return nil, sourceFault("AttributeError", "managed site id or name")
		}
		out = append(out, SiteOption{ID: id, Name: name})
	}
	return out, nil
}

func customSiteOptions(ctx context.Context, sites sdk.Sites) ([]SiteOption, error) {
	reply, err := sites.SourceCustomConfig(ctx)
	if err != nil {
		return nil, err
	}
	config, err := sourceConfigObject(reply.Config)
	if err != nil || !truth(config["enabled"]) {
		return nil, err
	}
	items, err := sourceList(config["sites"])
	if err != nil {
		return nil, err
	}
	out := make([]SiteOption, 0, len(items))
	for _, item := range items {
		site, ok := item.(map[string]any)
		if !ok {
			return nil, sourceFault("AttributeError", "custom site.get")
		}
		out = append(out, SiteOption{ID: site["id"], Name: site["name"]})
	}
	return out, nil
}

func downloaderFormNames(ctx context.Context, downloaders sdk.Downloaders) ([]string, error) {
	reply, err := downloaders.Configurations(ctx)
	if err != nil {
		return nil, err
	}
	names := []string{}
	seen := map[string]bool{}
	for _, row := range reply.Downloaders {
		if row.Enabled && row.Name != "" && row.Kind != "" && !seen[row.Name] {
			names = append(names, row.Name)
			seen[row.Name] = true
		}
	}
	return names, nil
}

// prepareConfigFromHost performs the first, pre-stop write in init_plugin.
// The source filters selected sites in managed-priority then CustomSites order.
func prepareConfigFromHost(ctx context.Context, host sdk.HostCaller, config *Config, raw json.RawMessage) error {
	input, err := sourceConfigObject(raw)
	if err != nil {
		return err
	}
	if len(input) > 0 {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		config.skipRaw = fields["skipverify"]
	}
	sites := sdk.Sites{Host: host}
	write, err := config.PrepareInit(input, func() ([]any, error) {
		managed, err := managedSiteOptions(ctx, sites)
		if err != nil {
			return nil, err
		}
		custom, err := customSiteOptions(ctx, sites)
		if err != nil {
			return nil, err
		}
		ids := make([]any, 0, len(managed)+len(custom))
		for _, group := range [][]SiteOption{managed, custom} {
			for _, site := range group {
				ids = append(ids, site.ID)
			}
		}
		return ids, nil
	})
	if err != nil || write == nil {
		return err
	}
	_, err = (sdk.Base{Host: host}).UpdateConfig(ctx, withRawSkip(write, config.skipRaw))
	return err
}

func siteDeletedFromHost(ctx context.Context, host sdk.HostCaller, config *Config, raw json.RawMessage) error {
	var event sdk.MutableEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		return err
	}
	value, err := decodeJSON(event.Data)
	if err != nil {
		return err
	}
	fields, ok := value.(map[string]any)
	if !ok {
		return sourceFault("AttributeError", "event_data.get")
	}
	base := sdk.Base{Host: host}
	saved, err := base.GetConfig(ctx)
	if err != nil {
		return err
	}
	old, err := sourceConfigObject(saved)
	if err != nil {
		return err
	}
	changed, err := config.SiteDeleted(old, fields["site_id"])
	if err != nil || !changed {
		return err
	}
	write, err := config.Snapshot()
	if err != nil {
		return err
	}
	_, err = base.UpdateConfig(ctx, withRawSkip(write, config.skipRaw))
	return err
}

func sourceConfigObject(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	v, err := decodeJSON(raw)
	if err != nil || !truth(v) {
		return nil, err
	}
	config, ok := v.(map[string]any)
	if !ok {
		return nil, sourceFault("AttributeError", "config.get")
	}
	return config, nil
}
