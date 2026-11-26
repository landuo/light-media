package larkmessager

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	sdk "light-media/builtinplugins/internal/sdk"
)

const PluginID = "LarkMessager"
const messageSource = "Lark"

var httpAPIs = []sdk.HTTPAPI{
	{Key: "webhook", Path: "/webhook", Methods: []string{"POST", "GET"}, Auth: "anonymous",
		Query: []sdk.HTTPParameter{{Name: "token", Kind: "string"}}, Headers: []string{"x-lark-signature", "x-lark-request-timestamp", "x-lark-request-nonce"}, Body: "raw"},
	{Key: "test", Path: "/test", Methods: []string{"GET"}, Auth: "bear"},
	{Key: "status", Path: "/status", Methods: []string{"GET"}, Auth: "bear"},
	{Key: "fetch_chats", Path: "/fetch_chats", Methods: []string{"GET"}, Auth: "bear"},
}

func Manifest() sdk.Manifest {
	methods := []string{"init", "get_state", "get_form", "get_page", "get_render_mode", "get_api", "get_module", "http", "message_action", "resolve_recipients", "stop_service", "close", "stop"}
	return sdk.Manifest{
		SchemaVersion: 1, PluginID: PluginID, Name: "Lark 应用消息通知",
		Description: "基于国际版飞书 Lark 开放平台应用的通知与消息交互插件，支持文本、卡片消息发送及消息回调交互。",
		Icon:        "https://raw.githubusercontent.com/jxxghp/MoviePilot-Plugins/main/icons/FeiShu_A.png",
		Version:     "1.0.0", Author: "ui-beam-9", AuthorURL: "https://github.com/ui-beam-9",
		Runtime: "go-executable", Entry: "lark-messager", HostAPIMin: 1, HostAPIMax: 1, PluginOrder: 50,
		HasPage: true, APIs: httpAPIs, ManagedPrivateScheduler: true,
		Methods: append(methods, moduleNames...),
		Capabilities: []string{endpointCapability, sdk.BaseGetInstanceMetadata, sdk.BaseGetData, sdk.BaseSaveData, sdk.BaseDelData,
			sdk.BackgroundSubmitCapability, sdk.MessageReceiveCapability, sdk.NetworkGetCapability, sdk.StorageLocalReadGrantCapability, sdk.LogWriteCapability},
		BroadcastHandlers: []sdk.BroadcastHandler{{Event: "message.action", Method: "message_action"}},
	}
}

type invocationKey struct{}

type invocation struct {
	host      sdk.HostCaller
	uncertain bool
}

func currentInvocation(ctx context.Context) *invocation {
	call, _ := ctx.Value(invocationKey{}).(*invocation)
	return call
}

type scopedKV struct{}

func (scopedKV) Get(ctx context.Context, key string) (Object, error) {
	raw, err := (sdk.Base{Host: currentInvocation(ctx).host}).GetData(ctx, &key)
	if err != nil {
		return nil, err
	}
	var value Object
	err = decode(raw, &value)
	return value, err
}

func (scopedKV) Put(ctx context.Context, key string, value Object) error {
	return (sdk.Base{Host: currentInvocation(ctx).host}).SaveData(ctx, key, value)
}

func (scopedKV) Delete(ctx context.Context, key string) error {
	return (sdk.Base{Host: currentInvocation(ctx).host}).DelData(ctx, &key)
}

func forwardMessage(ctx context.Context, payload Object) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	source := messageSource
	_, err = (sdk.Messages{Host: currentInvocation(ctx).host}).Receive(ctx, sdk.ReceiveMessageRequestV1{
		Source: &source, Body: body, Form: sdk.MessageValues{}, Args: sdk.MessageValues{"source": {source}},
	})
	return err
}

type runningPlugin struct {
	plugin   *Plugin
	provider *provider
	instance string
	lifetime context.Context
	cancel   context.CancelFunc
}

func (r *runningPlugin) stop() {
	r.cancel()
	r.plugin.Stop()
	r.provider.client.CloseIdleConnections()
}

type Worker struct {
	lifecycle sync.Mutex
	mu        sync.RWMutex
	active    *runningPlugin
}

func NewWorker() *Worker { return &Worker{} }

func (w *Worker) snapshot() *runningPlugin {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.active
}

func (w *Worker) initialize(ctx context.Context, raw json.RawMessage, host sdk.HostCaller) error {
	w.lifecycle.Lock()
	defer w.lifecycle.Unlock()
	if host == nil {
		return ErrDependency
	}
	config := Object{}
	if len(raw) != 0 {
		value, err := DecodeSource(raw)
		if err != nil {
			return err
		}
		if truth(value) {
			config = obj(value)
			if config == nil {
				return ErrUnsupported
			}
		}
	}
	state, err := ConfigFromTrusted(config)
	if err != nil {
		return err
	}
	rawOrigin, err := host(ctx, endpointCapability, json.RawMessage(`{}`))
	if err != nil {
		return err
	}
	var origin string
	if err := json.Unmarshal(rawOrigin, &origin); err != nil {
		return err
	}
	metadata, err := (sdk.Base{Host: host}).GetInstanceMetadata(ctx)
	if err != nil {
		return err
	}
	if metadata.InstanceID == "" {
		return ErrDependency
	}
	peer, err := newProvider(origin, state.AppID, state.AppSecret)
	if err != nil {
		return err
	}
	p := New(PluginHost{API: peer, newChatsAPI: peer.withChatCredentials, KV: scopedKV{}, Forward: forwardMessage, LogFailure: func(ctx context.Context, message string) {
		_ = (sdk.Logger{Host: currentInvocation(ctx).host}).Log(ctx, sdk.LogError, message)
	}})
	epoch, err := p.Init(config)
	if err != nil {
		return err
	}
	lifetime, cancel := context.WithCancel(context.Background())
	active := &runningPlugin{plugin: p, provider: peer, instance: metadata.InstanceID, lifetime: lifetime, cancel: cancel}
	w.mu.Lock()
	prior := w.active
	w.active = active
	w.mu.Unlock()
	if prior != nil {
		prior.stop()
	}
	if state.Enabled {
		err = (sdk.Manager{Host: host}).SubmitPrivateScheduler(ctx, "resolve_recipients", epoch)
		if err != nil {
			active.stop()
			return err
		}
	}
	return nil
}

func (w *Worker) Handle(ctx context.Context, method string, raw json.RawMessage, host sdk.HostCaller) (json.RawMessage, error) {
	switch method {
	case "init":
		return nil, w.initialize(ctx, raw, host)
	case "get_form":
		form, err := Form()
		return marshalResult([2]any{form["form"], form["defaults"]}, err)
	case "get_render_mode":
		return json.RawMessage(`["vuetify",null]`), nil
	case "get_api":
		return json.Marshal(httpAPIs)
	case "close", "stop_service", "stop":
		w.lifecycle.Lock()
		defer w.lifecycle.Unlock()
		if active := w.snapshot(); active != nil {
			active.stop()
		}
		return nil, nil
	}
	active := w.snapshot()
	if active == nil {
		return nil, ErrDependency
	}
	p := active.plugin
	switch method {
	case "get_state":
		return json.Marshal(p.State())
	case "get_module":
		modules := map[string]string{}
		for _, name := range p.Modules() {
			modules[name] = name
		}
		return json.Marshal(modules)
	}
	ctx, cancel := context.WithCancel(context.WithValue(ctx, invocationKey{}, &invocation{host: host}))
	defer cancel()
	release := context.AfterFunc(active.lifetime, cancel)
	defer release()
	if err := active.lifetime.Err(); err != nil {
		return nil, err
	}
	switch method {
	case "get_page":
		return marshalResult(p.Page(ctx))
	case "http":
		return p.serveHTTP(ctx, raw)
	case "resolve_recipients":
		var epoch uint64
		if err := json.Unmarshal(raw, &epoch); err != nil {
			return nil, err
		}
		return nil, p.SyncAdmins(ctx, epoch)
	case "message_action":
		var event sdk.MutableEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			return nil, err
		}
		if len(event.Data) == 0 || string(event.Data) == "null" {
			return nil, nil
		}
		data, err := DecodeSourceObject(event.Data)
		if err != nil {
			return nil, err
		}
		return nil, p.MessageAction(ctx, active.instance, data)
	default:
		return p.invokeModule(ctx, method, raw)
	}
}

func (p *Plugin) serveHTTP(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var request sdk.HTTPRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	var payload Object
	var err error
	status := 200
	switch request.Key {
	case "test":
		payload, err = p.Test(ctx)
	case "status":
		payload, err = p.Status(ctx)
	case "fetch_chats":
		payload, err = p.FetchChats(ctx)
	case "webhook":
		state := p.ConfigTrusted()
		var result SourceWebhook
		result, err = DecodeSourceWebhook(request.Body, request.Headers, request.LastQuery("token"), state.EncryptKey, state.VerificationToken)
		if err == nil && result.Accepted {
			err = p.DispatchTrusted(ctx, result.Body)
		}
		status, payload = result.Status, result.Reply
	default:
		return nil, errors.New("unsupported Lark HTTP API")
	}
	body, err := marshalResult(payload, err)
	if err != nil {
		return nil, err
	}
	return json.Marshal(sdk.HTTPResponse{Status: status, Body: body, Headers: map[string]string{"Content-Type": "application/json"}})
}

func marshalResult(value any, err error) (json.RawMessage, error) {
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
