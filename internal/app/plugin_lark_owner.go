package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"light-media/internal/networkproxy"
	"light-media/internal/plugins"
	lark "light-media/internal/plugins/larkmessager"
	larkhost "light-media/internal/plugins/larkmessager/host"
)

// Workers only perform the terminal SDK handshake. This owner outlives each
// short worker and serializes original Client target caches and configuration.
// Waiting for a gate holds no SQL connection, process slot or global mutex.
type pluginLarkRuntime struct {
	mu          sync.Mutex
	owners      map[string]*pluginLarkOwner
	closing     bool
	loopbackAPI string // private owned-test seam; production is the fixed official origin.
	client      *http.Client
}
type pluginLarkOwner struct {
	a          *App
	instance   string
	generation int64
	gate       chan struct{}
	plugin     *lark.Plugin
	revision   int64
	configSHA  string
	epoch      uint64
	tokens     lark.OwnerTokenCache
}
type pluginLarkLease struct {
	call  *pluginDeferredCall
	owner *pluginLarkOwner
}
type pluginLarkLeaseKey struct{}
type pluginLarkExecution struct {
	owner   *pluginLarkOwner
	scope   larkhost.Scope
	journal *larkhost.Store
	adapter *lark.Adapter
	binding lark.Binding
}
type pluginLarkExecutionKey struct{}

func (o *pluginLarkOwner) lock(ctx context.Context) error {
	select {
	case o.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (o *pluginLarkOwner) unlock() { <-o.gate }
func (a *App) larkOwner(ctx context.Context, in PluginInstallation) (*pluginLarkOwner, error) {
	if in.PluginID != "LarkMessager" || in.Version != "0.9.1" || !in.Installed || in.Generation < 1 {
		return nil, lark.ErrDenied
	}
	key := in.InstanceID + "/" + notificationChannelRevisionKey(in.Generation)
	a.pluginLark.mu.Lock()
	defer a.pluginLark.mu.Unlock()
	if a.pluginLark.closing || ctx.Err() != nil {
		return nil, lark.ErrDenied
	}
	if a.pluginLark.owners == nil {
		a.pluginLark.owners = map[string]*pluginLarkOwner{}
	}
	if o := a.pluginLark.owners[key]; o != nil {
		return o, nil
	}
	o := &pluginLarkOwner{a: a, instance: in.InstanceID, generation: in.Generation, gate: make(chan struct{}, 1)}
	o.plugin = lark.New(lark.PluginHost{API: pluginLarkAPI{o}, KV: pluginLarkNativeKV{o}, UpdateNotifications: o.updateNotifications, Forward: o.forward, UserName: o.userName})
	a.pluginLark.owners[key] = o
	return o, nil
}
func (d *pluginDeferredCall) lockLarkDeferred(ctx context.Context) (context.Context, func(), error) {
	o, e := d.a.larkOwner(ctx, d.installation)
	if e != nil {
		return ctx, nil, e
	}
	if e = o.lock(ctx); e != nil {
		return ctx, nil, e
	}
	lease := &pluginLarkLease{d, o}
	release := d.a.pluginStore.larkKeepLease(ctx, lease)
	return context.WithValue(ctx, pluginLarkLeaseKey{}, lease), func() { release(); o.unlock() }, nil
}
func (o *pluginLarkOwner) execution(ctx context.Context) (*pluginLarkExecution, error) {
	x, ok := ctx.Value(pluginLarkExecutionKey{}).(*pluginLarkExecution)
	if !ok || x == nil || x.owner != o || x.scope.Owner != o.instance || x.scope.Generation != o.generation {
		return nil, lark.ErrDenied
	}
	return x, nil
}
func (o *pluginLarkOwner) ensurePlugin(raw json.RawMessage, revision int64) error {
	sha := tokenHash(string(raw))
	if o.revision == revision && o.configSHA == sha {
		return nil
	}
	v, e := lark.DecodeSourceObject(raw)
	if e != nil {
		return e
	}
	epoch, e := o.plugin.Init(v)
	if e != nil {
		return e
	}
	o.epoch, o.revision, o.configSHA = epoch, revision, sha
	o.tokens.Reset()
	return nil
}

type pluginLarkAPI struct{ o *pluginLarkOwner }

func (h pluginLarkAPI) adapter(ctx context.Context) (*lark.Adapter, error) {
	x, e := h.o.execution(ctx)
	if e != nil {
		return nil, e
	}
	if x.adapter != nil {
		return x.adapter, nil
	}
	journal, e := x.journal.ForRun(ctx, x.scope)
	if e != nil {
		return nil, e
	}
	adapter, e := lark.NewAdapter(x.binding, journal, lark.Options{Guard: func(ctx context.Context, s lark.Scope) error {
		if s.Owner != x.scope.Owner || s.Generation != x.scope.Generation || s.Revision != x.scope.Revision || s.InvocationID != x.scope.InvocationID {
			return lark.ErrDenied
		}
		return x.journal.ValidateRun(ctx, x.scope)
	}, Client: h.o.a.larkHTTPClient(), TestAPIOrigin: h.o.a.pluginLark.loopbackAPI})
	if e != nil {
		return nil, e
	}
	if e = h.o.tokens.Bind(adapter); e != nil {
		return nil, e
	}
	x.adapter = adapter
	return adapter, nil
}
func (h pluginLarkAPI) API(ctx context.Context, r lark.Request) (lark.Response, error) {
	a, e := h.adapter(ctx)
	if e != nil {
		return lark.Response{}, e
	}
	defer h.o.tokens.Capture(a)
	return a.API(ctx, r)
}
func (h pluginLarkAPI) ReadFile(ctx context.Context, ref string) (lark.Blob, error) {
	a, e := h.adapter(ctx)
	if e != nil {
		return lark.Blob{}, e
	}
	return a.ReadFile(ctx, ref)
}
func (h pluginLarkAPI) FetchImage(ctx context.Context, ref string) (lark.Blob, error) {
	a, e := h.adapter(ctx)
	if e != nil {
		return lark.Blob{}, e
	}
	return a.FetchImage(ctx, ref)
}

func (o *pluginLarkOwner) updateNotifications(ctx context.Context, epoch uint64, fn func(any) (any, bool, error)) error {
	if epoch != o.epoch || fn == nil {
		return lark.ErrDenied
	}
	x, e := o.execution(ctx)
	if e != nil {
		return e
	}
	tx, e := o.a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = o.guardTx(ctx, tx, x.scope); e != nil {
		return e
	}
	old, e := o.a.pluginStore.notificationChannelsReadTx(ctx, tx)
	if e != nil {
		return e
	}
	v, changed, e := fn(old.Value)
	if e != nil {
		return e
	}
	if changed {
		value, ok := v.([]any)
		if !ok {
			return lark.ErrUnsupported
		}
		if _, e = o.a.pluginStore.notificationChannelsSaveTx(ctx, tx, old.Revision, value); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (o *pluginLarkOwner) userName(ctx context.Context, open, user string) (string, error) {
	id := open
	if id == "" {
		id = user
	}
	if id == "" {
		return "", nil
	}
	v, e := lark.NewClient(pluginLarkAPI{o}).UserInfo(ctx, id)
	if e != nil {
		return "", e
	}
	if name, ok := v["name"].(string); ok {
		return name, nil
	}
	return "", nil
}
func (o *pluginLarkOwner) forward(ctx context.Context, payload lark.Object) error {
	// The source starts MessageChain asynchronously. The durable continuation
	// producer is deliberately a separate boundary: it must not reenter this
	// owner's gate while DispatchTrusted is still holding it.
	return o.enqueueIncoming(ctx, payload)
}

func larkBindingForRun(o *pluginLarkOwner, run PluginRun, raw json.RawMessage, call *pluginLarkSourceCall) (lark.Binding, error) {
	v, e := lark.DecodeSourceObject(raw)
	if e != nil {
		return lark.Binding{}, e
	}
	c, e := lark.ConfigFromTrusted(v)
	if e != nil {
		return lark.Binding{}, e
	}
	b := lark.Binding{Owner: o.instance, Generation: o.generation, Revision: run.ConfigRevision, InvocationID: run.ID, AppID: c.AppID, AppSecret: c.AppSecret, Targets: map[string][]string{}, Resources: map[string][]string{}, Files: map[string]lark.Blob{}, Images: map[string]lark.Blob{}}
	add := func(kind, id string) {
		id = strings.TrimSpace(id)
		if id != "" {
			for _, prior := range b.Targets[kind] {
				if prior == id {
					return
				}
			}
			b.Targets[kind] = append(b.Targets[kind], id)
		}
	}
	add("chat_id", c.ChatID)
	if o.revision == run.ConfigRevision && o.configSHA == tokenHash(string(raw)) {
		resolved := o.plugin.ConfigTrusted().UserID
		if strings.HasPrefix(resolved, "ou_") {
			add("open_id", resolved)
		}
	}
	if strings.HasPrefix(c.UserRaw, "ou_") {
		add("open_id", c.UserRaw)
	}
	for _, id := range c.Admins {
		b.ContactIdentifiers = append(b.ContactIdentifiers, id)
	}
	if c.UserRaw != "" {
		b.ContactIdentifiers = append(b.ContactIdentifiers, c.UserRaw)
		b.RecipientIdentifiers = append(b.RecipientIdentifiers, c.UserRaw)
	}
	if call != nil {
		m := call.Module
		if m.Name == "message_parser" {
			larkGrantMessagePayload(&b, m.Metadata)
		}
		if meta, ok := m.Status["metadata"].(map[string]any); ok {
			id := larkSourceText(m.Status["message_id"])
			if id == "" {
				id = m.MessageID
			}
			if rid := larkSourceText(meta["reaction_id"]); rid != "" {
				b.Resources["reaction:"+id] = []string{rid}
			}
			if id != "" {
				b.Messages = append(b.Messages, id)
			}
		}
		if streaming, ok := m.Metadata["feishu_streaming"].(map[string]any); ok {
			if card := larkSourceText(streaming["card_id"]); card != "" {
				b.Cards = append(b.Cards, card)
			}
		}
		t := lark.MessageTarget(m.Notification)
		add(t.ReceiveIDType, t.UserID)
		add("chat_id", t.ChatID)
		add("chat_id", m.ChatID)
		for _, id := range []string{m.MessageID, m.Notification.OriginalMessageID} {
			if id != "" {
				b.Messages = append(b.Messages, id)
			}
		}
		if m.Ref != "" {
			b.Resources["image"] = append(b.Resources["image"], m.Ref)
			b.Resources["file"] = append(b.Resources["file"], m.Ref)
		}
		if m.ReactionID != "" {
			b.Resources["reaction:"+m.MessageID] = []string{m.ReactionID}
		}
	}
	return b, nil
}

func (a *App) pluginLarkHost(ctx context.Context, in PluginInstallation, run *PluginRun, method string, raw json.RawMessage) (json.RawMessage, error) {
	if in.PluginID != "LarkMessager" || in.Version != "0.9.1" {
		return nil, lark.ErrDenied
	}
	if method == lark.FormCapability {
		if run != nil || !deferredPayloadEqual(raw, []byte(`{}`)) {
			return nil, lark.ErrDenied
		}
		v, e := lark.Form()
		if e != nil {
			return nil, e
		}
		return json.Marshal(v)
	}
	if e := a.requireDeferredExecution(ctx, run, method); e != nil {
		return nil, e
	}
	lease, ok := ctx.Value(pluginLarkLeaseKey{}).(*pluginLarkLease)
	if !ok || lease == nil || lease.call.a != a || lease.call.run.ID != run.ID {
		return nil, lark.ErrDenied
	}
	o := lease.owner
	var inv plugins.Invocation
	if cleanupJournalStrict(run.Input, &inv) != nil {
		return nil, larkEvidenceError()
	}
	tx, e := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	source, call, e := a.larkSourceTx(ctx, tx, *run, method)
	if e == nil {
		e = tx.Commit()
	} else {
		tx.Rollback()
	}
	if e != nil {
		return nil, e
	}
	journal, e := larkhost.Attach(ctx, a.Store.DB, a.Store.key, larkhost.Group{Owner: o.instance, Generation: o.generation}, pluginLarkAuthority{a.pluginStore, o.instance, o.generation}, o.guardTx)
	if e != nil {
		return nil, e
	}
	scope := larkhost.Scope{Group: larkhost.Group{Owner: o.instance, Generation: o.generation}, Revision: run.ConfigRevision, InvocationID: run.ID, SourceID: source, InputSHA: deferredInputSHA(run.Input), ConfigSHA: tokenHash(string(inv.Config)), Action: method}
	if _, e = journal.ForRun(ctx, scope); e != nil {
		return nil, e
	}
	if method == lark.ConfigureCapability {
		o.revision = 0
	}
	if e = o.ensurePlugin(inv.Config, run.ConfigRevision); e != nil {
		return nil, e
	}
	binding, e := larkBindingForRun(o, *run, inv.Config, call)
	if e != nil {
		return nil, e
	}
	ctx = context.WithValue(ctx, pluginLarkExecutionKey{}, &pluginLarkExecution{owner: o, scope: scope, journal: journal, binding: binding})
	var result json.RawMessage
	switch method {
	case lark.ConfigureCapability:
		e = o.plugin.SyncAdmins(ctx, o.epoch)
		result = json.RawMessage(`null`)
	case lark.ModuleCapability:
		if call == nil {
			return nil, larkEvidenceError()
		}
		if slices.Contains(o.plugin.Modules(), call.Module.Name) {
			result, e = invokeLarkModule(ctx, o.plugin, call.Module)
		} else {
			result = json.RawMessage(`null`)
		}
	case lark.PageCapability:
		var value []any
		value, e = o.plugin.Page(ctx)
		if e == nil {
			result, e = json.Marshal(value)
		}
	case lark.APICapability:
		result, e = o.invokeAPI(ctx, *run)
	default:
		e = lark.ErrDenied
	}
	if errors.Is(e, lark.ErrUnknown) || errors.Is(e, larkhost.ErrIntegrity) || errors.Is(e, larkhost.ErrReplay) || larkRecoveryErrorCode(e) != "" {
		return nil, e
	}
	code := ""
	if e != nil {
		code = "LARK_SOURCE_FAILED"
		result = json.RawMessage(`null`)
	}
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if save := o.saveOutcome(finish, *run, method, result, code); save != nil {
		return nil, save
	}
	return result, e
}

func (a *App) larkHTTPClient() *http.Client {
	a.pluginLark.mu.Lock()
	defer a.pluginLark.mu.Unlock()
	if a.pluginLark.client == nil {
		a.pluginLark.client = &http.Client{Transport: networkproxy.NewRequestsTransport(http.DefaultTransport.(*http.Transport).Clone(), nil)}
	}
	return a.pluginLark.client
}
