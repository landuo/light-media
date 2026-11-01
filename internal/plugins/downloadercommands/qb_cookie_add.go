package downloadercommands

import (
	"context"
	"strings"
)

// QBAddCookiePrimitives is a fixed host-owned aggregate, never a public RPC or
// an endpoint supplied by the plugin. Native implementations must retain one
// owner guard, native lock and monotonic selected-dispatch proof across every
// primitive, including the final Add. Cookie writes are selected mutations.
type QBAddCookiePrimitives interface {
	WebAPIVersion(context.Context) (any, error)
	Cookies(context.Context) ([]any, error)
	SetCookies(context.Context, []map[string]any) (any, error)
	Add(context.Context, QBAddCall) (any, error)
}
type QBAddCookieScopePort interface {
	WithAddCookieScope(context.Context, func(context.Context, QBAddCookiePrimitives) (any, error)) (any, error)
}

// This new branch accepts only direct, definite source exceptions. It does not
// use the older generic errors.As sourceFailure helper: aggregate uncertainty,
// current-owner denial and cancellation must never become a fallback add.
func (q QBittorrent) cookieSourceCatch(ctx context.Context, level, prefix string, err error) error {
	if canceled := ctx.Err(); canceled != nil {
		return canceled
	}
	source, ok := err.(*SourceException)
	if !ok || source == nil {
		return err
	}
	if q.Log != nil {
		q.Log(ctx, level, prefix+source.Message)
	}
	return nil
}
func (q QBittorrent) syncAddCookies(ctx context.Context, ops QBAddCookiePrimitives, urls string, cookie any) (bool, error) {
	version, err := ops.WebAPIVersion(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		source, direct := err.(*SourceException)
		if !direct || source == nil {
			return false, err
		}
		switch source.Class {
		case "InvalidVersion", "TypeError", "ValueError":
			return false, nil
		}
		return false, q.cookieSourceCatch(ctx, "WARN", "获取 qbittorrent Web API 版本失败，跳过 Cookie API 兼容：", err)
	}
	if err = ctx.Err(); err != nil {
		return false, err
	}
	supported, err := QBSupportsCookieAPI(version)
	if err != nil {
		return false, err
	}
	if !supported {
		return false, nil
	}
	sites, err := qbBuildSiteCookies(urls, cookie)
	if err != nil {
		return false, q.cookieSourceCatch(ctx, "ERROR", "同步下载Cookie出错：", err)
	}
	if len(sites) == 0 {
		return false, nil
	}
	current, err := ops.Cookies(ctx)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return false, q.cookieSourceCatch(ctx, "ERROR", "同步下载Cookie出错：", err)
	}
	merged, err := qbMergeSiteCookies(current, sites)
	if err != nil {
		return false, q.cookieSourceCatch(ctx, "ERROR", "同步下载Cookie出错：", err)
	}
	// The original ignores a non-raising return value (including None/False).
	_, err = ops.SetCookies(ctx, merged)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return false, q.cookieSourceCatch(ctx, "ERROR", "同步下载Cookie出错：", err)
	}
	return true, nil
}
func (q QBittorrent) addWithCookieScope(ctx context.Context, call QBAddCall) (QBAddResult, error) {
	scope, ok := q.Client.(QBAddCookieScopePort)
	if !ok {
		return emptyQB(), &BoundaryError{"configured qB client lacks fixed URL-cookie aggregate"}
	}
	calls := 0
	value, err := scope.WithAddCookieScope(ctx, func(c context.Context, ops QBAddCookiePrimitives) (any, error) {
		calls++
		if calls != 1 || c == nil || ops == nil {
			return nil, &BoundaryError{"invalid or repeated URL-cookie aggregate callback"}
		}
		if err := c.Err(); err != nil {
			return nil, err
		}
		urls, ok := call.URLs.(string)
		if !ok || urls == "" || strings.HasPrefix(urls, "magnet:") || !truth(call.Cookie) {
			return nil, &BoundaryError{"outside fixed URL-cookie aggregate input"}
		}
		synced, err := q.syncAddCookies(c, ops, urls, call.Cookie)
		if err != nil {
			return nil, err
		}
		if synced {
			call.Cookie = nil
		}
		reply, err := ops.Add(c, call)
		if err == nil {
			err = c.Err()
		}
		if err != nil {
			return emptyQB(), q.cookieSourceCatch(c, "ERROR", "添加种子出错：", err)
		}
		result, err := parseQBReply(reply)
		if err != nil {
			return emptyQB(), q.cookieSourceCatch(c, "ERROR", "添加种子出错：", err)
		}
		return result, nil
	})
	if err != nil {
		return emptyQB(), err
	}
	if err = ctx.Err(); err != nil {
		return emptyQB(), err
	}
	result, ok := value.(QBAddResult)
	if calls != 1 || !ok {
		return emptyQB(), &BoundaryError{"URL-cookie aggregate lost source add result"}
	}
	return result, nil
}
