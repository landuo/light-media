package app

import (
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"light-media/internal/media/words"
	"light-media/internal/plugins/iyuuautoseed/compat/urlprep"
)

const originalRemoteIdentifiersContentLimit = words.MaxBytes

func originalRemoteIdentifiersTaskScope(ctx context.Context, scope originalBaseCallScope) bool {
	if !originalRemoteIdentifiersTask(scope) {
		return false
	}
	if scope.phase == "private_once" {
		return scope.object.originalPrivateSchedulerScopeAllowed(ctx)
	}
	if scope.phase == "refresh_continue" {
		lease, _ := ctx.Value(originalSubmittedObjectKey{}).(*originalSubmittedLease)
		return scope.object.originalPrivateSchedulerScopeAllowed(ctx) ||
			lease != nil && lease.sourceKind == originalSourceScheduler && scope.object.originalPublicServiceScopeAllowed(ctx)
	}
	service, _ := ctx.Value(originalServiceCallKey{}).(*originalServiceCall)
	return service != nil && service.job != nil && service.job.object == scope.object &&
		service.job.definition.Handler == scope.phase && scope.object.originalPublicServiceScopeAllowed(ctx)
}

func originalRemoteIdentifiersURL(raw string) (*url.URL, string, bool, error) {
	invalid := func() error { return bad("INVALID_IDENTIFIERS_URL", "远程词表地址须为有效的HTTP(S)地址") }
	if len(raw) > 8192 || !utf8.ValidString(raw) || strings.ContainsAny(raw, "\x00\r\n") {
		return nil, "", false, invalid()
	}
	prepared, err := urlprep.Prepare(raw)
	if err != nil {
		return nil, "", false, invalid()
	}
	address, err := url.Parse(prepared)
	if err != nil || !originalRemoteIdentifiersTargetAllowed(address) {
		return nil, "", false, invalid()
	}
	private := originalRemoteIdentifiersURLPrivate(address)
	auth, err := originalRemoteIdentifiersBasic(address.User)
	if err != nil {
		return nil, "", false, err
	}
	address.User = nil
	address.Fragment, address.RawFragment = "", ""
	return address, auth, private, nil
}

func originalRemoteIdentifiersBasic(user *url.Userinfo) (string, error) {
	if user == nil {
		return "", nil
	}
	username := user.Username()
	password, paired := user.Password()
	if !paired || (username == "" && password == "") {
		return "", nil
	}
	// Requests prepares URL auth after headers and uses Latin-1 for Basic.
	pair := username + ":" + password
	value := make([]byte, 0, len(pair))
	for _, character := range pair {
		if character > 255 {
			return "", bad("IDENTIFIERS_SOURCE_UNICODE_ERROR", "远程词表认证包含原版不支持的字符")
		}
		value = append(value, byte(character))
	}
	return "Basic " + base64.StdEncoding.EncodeToString(value), nil
}

func originalRemoteIdentifiersRedirect(next *http.Request, previous *url.URL, private *bool) error {
	address := next.URL
	if !originalRemoteIdentifiersTargetAllowed(address) || address.User != nil {
		return bad("IDENTIFIERS_REDIRECT_FAILED", "远程词表重定向地址无效")
	}
	if previous.Scheme == "https" && address.Scheme != "https" {
		return bad("IDENTIFIERS_REDIRECT_FAILED", "远程词表不允许HTTPS降级")
	}
	nextPrivate := originalRemoteIdentifiersURLPrivate(address)
	if (*private || nextPrivate) && (address.Scheme != previous.Scheme || !strings.EqualFold(address.Host, previous.Host)) {
		return bad("IDENTIFIERS_REDIRECT_FAILED", "私有订阅地址不允许跨来源重定向")
	}
	*private = *private || nextPrivate
	address.Fragment, address.RawFragment = "", ""
	return nil
}

func originalRemoteIdentifiersTargetAllowed(address *url.URL) bool {
	if address == nil || address.Hostname() == "" || address.Opaque != "" || (address.Scheme != "http" && address.Scheme != "https") {
		return false
	}
	ip, err := netip.ParseAddr(address.Hostname())
	return err != nil || originalRemoteIdentifiersIPAllowed(ip)
}

func originalRemoteIdentifiersIPAllowed(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsGlobalUnicast() || ip.IsLoopback()
}

func originalRemoteIdentifiersDial(dial func(context.Context, string, string) (net.Conn, error), timeout time.Duration) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		// Allow configured LAN Etherpad/loopback. Bind each local socket to a
		// checked address so DNS cannot substitute a link-local destination.
		for _, ip := range addresses {
			if !originalRemoteIdentifiersIPAllowed(ip) {
				return nil, bad("INVALID_IDENTIFIERS_URL", "远程词表地址不允许访问此网络范围")
			}
		}
		for _, ip := range addresses {
			conn, dialErr := dial(ctx, network, net.JoinHostPort(ip.String(), port))
			if dialErr == nil {
				return conn, nil
			}
		}
		return nil, bad("IDENTIFIERS_FETCH_FAILED", "远程词表连接失败")
	}
}
