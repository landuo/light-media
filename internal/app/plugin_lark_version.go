package app

import (
	"context"
	"net/http"
	"net/url"

	"light-media/internal/notice"
	"light-media/internal/systemversion"
	"light-media/web"
)

type larkVersionResult struct {
	Server, Front                  systemversion.Release
	LocalServer, LocalFront, Title string
	NetworkSHA                     []string
}

func (a *App) larkCommandVersion(ctx context.Context, core *pluginLarkCoreExecution, user string) error {
	result, e := larkCoreRead(ctx, a, core, "command.version.query", struct{ Contract string }{"installed-system-version/v1"}, func(ctx context.Context) (larkVersionResult, error) {
		out := larkVersionResult{}
		fetch := func(target string) (systemversion.Release, error) {
			settings, e := a.identifiersSourceNetwork(ctx)
			if e != nil {
				if ctx.Err() != nil {
					return systemversion.Release{}, ctx.Err()
				}
				return systemversion.Release{}, bad("LARK_VERSION_SETTINGS_DEPENDENCY", "版本查询所需的原版网络设置尚未迁移或无效")
			}
			env, e := identifiersProxyEnvironment(settings)
			if e != nil {
				return systemversion.Release{}, e
			}
			out.NetworkSHA = append(out.NetworkSHA, tokenHash(string(mustCleanSeedJSON(settings))))
			base, ok := http.DefaultTransport.(*http.Transport)
			if !ok {
				return systemversion.Release{}, systemversion.ErrDependency
			}
			return systemversion.Fetch(ctx, base, env, settings.GitHubHeaders, target, func(c context.Context, u *url.URL) error { return a.larkVersionScope(c, core) })
		}
		var e error
		if out.Server, e = fetch(systemversion.BackendURL); e != nil {
			return out, e
		}
		if out.Front, e = fetch(systemversion.FrontendURL); e != nil {
			return out, e
		}
		for i, r := range []systemversion.Release{out.Server, out.Front} {
			if r.Version == nil {
				level := "WARN"
				if r.Caught || r.Status >= 400 {
					level = "ERROR"
				}
				component := "后端"
				if i == 1 {
					component = "前端"
				}
				a.event(ctx, core.job, level, "system", "version_lookup_failed", component+"发布版本查询未返回可用的 v2 版本")
			}
		}
		out.LocalServer = appVersion
		if out.LocalFront, e = web.FrontendVersion(); e != nil {
			return out, e
		}
		out.Title = systemversion.Message(out.Server, out.Front, out.LocalServer, out.LocalFront)
		return out, nil
	})
	if e != nil {
		return e
	}
	return a.larkPostBusinessNotice(ctx, core, sourceBusinessNotice{AbsentContext: true, Envelope: notice.Object{"channel": "飞书", "source": "Lark", "title": result.Title, "userid": user, "save_history": false}, Kwargs: notice.Object{}})
}

func (a *App) larkVersionScope(ctx context.Context, core *pluginLarkCoreExecution) error {
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	_, _, e = a.larkCoreScopeTx(ctx, tx, core)
	return e
}
