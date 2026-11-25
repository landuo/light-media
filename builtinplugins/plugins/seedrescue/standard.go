package seedrescue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"time"
	_ "time/tzdata"

	sdk "light-media/builtinplugins/internal/sdk"
)

func Manifest() sdk.Manifest {
	return sdk.Manifest{
		SchemaVersion: 1, PluginID: PluginID, Name: "稀缺保种", Version: Version,
		Description: "读取目标站RSS，可选择跨站同源候选下载后辅种，也可直接下载保种站点原种。",
		Icon:        "IYUU.png", Author: "landuo", AuthorURL: "https://github.com/landuo", PluginOrder: 18,
		Runtime: "go-executable", Entry: "seed-rescue", HostAPIMin: 1, HostAPIMax: 1,
		Methods: []string{"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "get_form", "get_page",
			"get_sidebar_nav", "http", "scan", "scan_background", "close", "stop_service", "stop"},
		Capabilities: []string{sdk.BaseGetData, sdk.BaseSaveData, sdk.BaseUpdateConfig, sdk.BackgroundSubmitCapability,
			sdk.LogWriteCapability, sdk.PostMessageCapability, iyuuCapability, serviceCapability, siteCapability,
			indexerCapability, optionsCapability, rssCapability, seedInfoCapability, torrentURLCapability, searchCapability,
			detailCapability, downloadMetadataCapability, queryTorrentsCapability, downloadingCapability, addTorrentCapability},
		ManagedPublicServices: true, ManagedPrivateScheduler: true, HasPage: true, APIs: httpAPIs(),
		Frontend: Frontend(), RuntimeSupport: &sdk.RuntimeSupport{Platforms: []string{"linux"}, StorageTypes: []string{"local"}},
	}
}

// One worker belongs to one loaded installation. Host callers live only in
// the callback context, including those used by concurrent site probes.
type Plugin struct {
	host        standardHost
	machine     *Machine
	initMu      sync.Mutex
	initialized bool
	nextScan    atomic.Uint64
}

func NewPlugin() *Plugin {
	zone := time.Local
	if name := os.Getenv("TZ"); name != "" {
		if location, err := time.LoadLocation(name); err == nil {
			zone = location
		}
	}
	p := &Plugin{host: standardHost{zone: zone}}
	p.machine = newMachine(&p.host)
	return p
}

func (p *Plugin) Handle(ctx context.Context, method string, raw json.RawMessage, caller sdk.HostCaller) (json.RawMessage, error) {
	inv := &invocationHost{caller: caller}
	ctx = context.WithValue(ctx, invocationHostKey{}, inv)
	result, err := p.handle(ctx, method, raw)
	return result, errors.Join(err, inv.iyuuError)
}

func (p *Plugin) handle(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	m := p.machine
	switch method {
	case "init":
		var config Object
		if len(raw) > 0 {
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			if err := decoder.Decode(&config); err != nil {
				return nil, err
			}
		}
		p.initMu.Lock()
		defer p.initMu.Unlock()
		if !p.initialized {
			if err := m.restore(ctx); err != nil {
				return nil, err
			}
			p.initialized = true
		}
		return nil, m.Init(ctx, config)
	case "get_state":
		return json.Marshal(m.State())
	case "get_service":
		services, err := m.Services(ctx)
		if err != nil {
			return nil, err
		}
		rows := make([]Object, 0, len(services))
		for _, service := range services {
			trigger := Object{"kind": service.Trigger, "local_timezone": true}
			if service.Trigger == "date" {
				trigger["run_at"] = service.RunAt.Format(time.RFC3339Nano)
			} else {
				trigger["cron"] = service.Cron
			}
			rows = append(rows, Object{"id": service.ID, "name": service.Name, "handler": service.Method, "trigger": trigger, "input": service.Kwargs})
		}
		return json.Marshal(rows)
	case "get_command", "get_page":
		return json.RawMessage(`[]`), nil
	case "get_form":
		return json.Marshal([]any{[]any{}, m.Config()})
	case "get_api":
		return json.Marshal(httpAPIs())
	case "get_render_mode":
		return json.RawMessage(`["vue","dist/assets"]`), nil
	case "get_sidebar_nav":
		return json.Marshal(m.Sidebar())
	case "http":
		return p.http(ctx, m, raw)
	case "scan":
		return nil, m.Scan(ctx)
	case "scan_background":
		var id uint64
		if err := json.Unmarshal(raw, &id); err != nil {
			return nil, err
		}
		return nil, m.RunSubmittedScan(ctx, id)
	case "stop_service", "stop":
		return nil, m.Stop(ctx)
	case "close":
		return nil, nil
	default:
		return nil, errors.New("unsupported SeedRescue method")
	}
}
