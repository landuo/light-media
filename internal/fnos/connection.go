package fnos

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"syscall"
)

// NormalizeHost accepts the fnOS Media root or /v entry point. Plain HTTP is
// limited to literal private/loopback addresses and localhost; HTTPS always
// retains the HTTP transport's normal certificate verification.
func NormalizeHost(value string) (string, error) {
	value = strings.TrimSpace(value)
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || strings.HasSuffix(u.Host, ":") || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(value, "#") || u.RawPath != "" || (u.Path != "" && u.Path != "/" && u.Path != "/v" && u.Path != "/v/") || (u.Scheme != "http" && u.Scheme != "https") {
		return "", &ServiceError{Code: "invalid_config"}
	}
	if port := u.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return "", &ServiceError{Code: "invalid_config"}
		}
	}
	if u.Scheme == "http" && !strings.EqualFold(u.Hostname(), "localhost") {
		address, err := netip.ParseAddr(u.Hostname())
		if err != nil || address.Zone() != "" || (!address.Unmap().IsPrivate() && !address.Unmap().IsLoopback()) {
			return "", &ServiceError{Code: "invalid_config"}
		}
	}
	u.Path = "/v"
	return u.String(), nil
}

// Return only local fixed codes: wrapped network errors contain the target URL
// and must never be returned through the application API.
func transportErrorCode(err error) string {
	var verification *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	if errors.As(err, &verification) || errors.As(err, &unknown) || errors.As(err, &hostname) || errors.As(err, &invalid) {
		return "tls_certificate_failed"
	}
	var network net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &network) && network.Timeout()) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "connection_refused"
	}
	return "unavailable"
}

func probeMessage(code string) string {
	switch code {
	case "INVALID_HOST":
		return (&ServiceError{Code: "invalid_config"}).Error()
	case "TLS_CERTIFICATE_FAILED", "TIMEOUT", "CONNECTION_REFUSED", "CANCELED":
		return (&ServiceError{Code: strings.ToLower(code)}).Error()
	case "TRANSPORT_FAILED":
		return "无法连接飞牛影视，请检查地址、端口和网络"
	case "CREDENTIALS_REQUIRED":
		return (&ServiceError{Code: "not_configured"}).Error()
	case "AUTHENTICATION_FAILED", "EMPTY_TOKEN":
		return "飞牛影视认证失败，请检查账号密码及登录权限"
	case "ACCESS_CODE_SESSION_REQUIRED":
		return "飞牛影视需要账号密码登录，暂不支持访问码登录"
	default:
		return "飞牛影视连接验证失败，请检查连接配置和服务状态"
	}
}
