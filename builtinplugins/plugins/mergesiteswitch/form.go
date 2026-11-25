package mergesiteswitch

import (
	"context"
	_ "embed"
	"encoding/json"
	"slices"

	sdk "light-media/builtinplugins/internal/sdk"
)

//go:embed form.json
var sourceForm []byte

type node struct {
	Component string         `json:"component"`
	Props     map[string]any `json:"props,omitempty"`
	Content   []node         `json:"content,omitempty"`
	Text      string         `json:"text,omitempty"`
}

type option struct {
	Title string `json:"title"`
	Value any    `json:"value"`
}

func form(ctx context.Context, host sdk.HostCaller) (json.RawMessage, error) {
	service := sdk.Sites{Host: host}
	sites, err := service.Read(ctx, sdk.SiteHostRequestV1{OrderByPriority: true})
	if err != nil {
		return nil, err
	}
	options := make([]option, 0, len(sites.Items))
	for _, site := range sites.Items {
		options = append(options, option{site.Name, site.SourceID})
	}
	var custom []customSite
	if err = call(ctx, host, request{Action: "custom"}, &custom); err != nil {
		return nil, err
	}
	all := slices.Clone(options)
	for _, site := range custom {
		all = append(all, option{site.Name + " [自定义]", site.ID})
	}
	ids, err := installed(ctx, host)
	if err != nil {
		return nil, err
	}
	var nodes []node
	if err := json.Unmarshal(sourceForm, &nodes); err != nil {
		return nil, err
	}
	rows := nodes[0].Content[:0]
	for _, row := range nodes[0].Content {
		include := true
		for _, col := range row.Content {
			for _, input := range col.Content {
				if input.Component != "VSelect" {
					continue
				}
				input.Props["items"] = options
				for _, s := range selections {
					if s.field == input.Props["model"] {
						include = include && s.applies(ids)
						if s.custom {
							input.Props["items"] = all
						}
						break
					}
				}
			}
		}
		if include {
			rows = append(rows, row)
		}
	}
	nodes[0].Content = rows
	if err := refreshFormConfig(ctx, host, service); err != nil {
		return nil, err
	}
	return json.Marshal([]any{nodes, map[string]any{}})
}

func refreshFormConfig(ctx context.Context, host sdk.HostCaller, service sdk.Sites) error {
	base := sdk.Base{Host: host}
	raw, err := base.GetConfig(ctx)
	if err != nil {
		return err
	}
	config := map[string]json.RawMessage{}
	if truth(raw) {
		if err := json.Unmarshal(raw, &config); err != nil {
			return err
		}
	}
	active, err := service.Read(ctx, sdk.SiteHostRequestV1{ActiveOnly: true, OrderByPriority: true})
	if err != nil {
		return err
	}
	ids := make([]int64, 0, len(active.Items))
	for _, site := range active.Items {
		ids = append(ids, site.SourceID)
	}
	config["enable_sites"], err = json.Marshal(ids)
	if err != nil {
		return err
	}
	for _, s := range selections[:2] {
		config[s.field], err = get(ctx, host, s.field)
		if err != nil {
			return err
		}
	}
	plugins, err := installed(ctx, host)
	if err != nil {
		return err
	}
	for _, s := range selections[2:] {
		if s.applies(plugins) {
			config[s.field], err = get(ctx, host, s.field)
			if err != nil {
				return err
			}
		}
	}
	_, err = base.UpdateConfig(ctx, config)
	return err
}
