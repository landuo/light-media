package catalog

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"light-media/internal/logging"
	"net"
	"net/http"
	"sync"
	"syscall"
)

// sourceRecognitionTransportCloner 只由宿主选择的可信 transport 实现；
// 并非 worker 可提供的工厂、URL 或代理设置。
type sourceRecognitionTransportCloner interface {
	CloneForSourceRecognition() (http.RoundTripper, error)
}

type sourceRecognitionSession struct {
	namespace string
	mu        sync.Mutex
	seed      http.RoundTripper
	factory   func() (http.RoundTripper, error)
	client    *http.Client
}

func (s *sourceRecognitionSession) configure(transport http.RoundTripper) {
	if transport == nil {
		transport = http.DefaultTransport
	}
	s.seed = transport
	switch value := transport.(type) {
	case *http.Transport:
		s.factory = func() (http.RoundTripper, error) { return sourceRecognitionTLSClone(value), nil }
	case sourceRecognitionTransportCloner:
		s.factory = value.CloneForSourceRecognition
	}
}

func (s *sourceRecognitionSession) acquire() (*http.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		return s.client, nil
	}
	return s.createLocked()
}
func (s *sourceRecognitionSession) createLocked() (*http.Client, error) {
	transport := s.seed
	if s.factory != nil {
		var err error
		transport, err = s.factory()
		if err != nil {
			return nil, &Error{Code: "recognition_session_unavailable"}
		}
	}
	if transport == nil {
		return nil, &Error{Code: "recognition_session_unavailable"}
	}
	s.client = &http.Client{Timeout: requestLimit, Transport: logging.Transport(transport), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return s.client, nil
}

// rebuild 只替换本识别 session。其它 catalog.get 的 c.http 从不被替换。
func (s *sourceRecognitionSession) rebuild(previous *http.Client) (*http.Client, error) {
	s.mu.Lock()
	if s.client != previous {
		client := s.client
		s.mu.Unlock()
		if client == nil {
			return nil, &Error{Code: "recognition_session_unavailable"}
		}
		return client, nil
	}
	if s.factory == nil {
		s.mu.Unlock()
		return nil, &Error{Code: "recognition_session_unavailable"}
	}
	old := s.client
	s.client = nil
	client, err := s.createLocked()
	s.mu.Unlock()
	if old != nil {
		old.CloseIdleConnections()
	}
	return client, err
}
func (s *sourceRecognitionSession) closeIdle() {
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()
	if client != nil {
		client.CloseIdleConnections()
	}
}

// 只承认可证明无响应的连接终止；代理通用错误、策略/权限拒绝、取消不猜测。
func sourceRecognitionDefiniteNoResponse(ctx context.Context, response *http.Response, err error) bool {
	if response != nil || err == nil || ctx.Err() != nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var connection *net.OpError
	if !errors.As(err, &connection) {
		return false
	}
	return errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNABORTED) || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNREFUSED)
}

// sourceRecognitionTLSClone 仅用于已批准 verify=False 的新识别实例。
// 原 c.http、全局 DefaultTransport 和其它 catalog 方法保持原 TLS 策略。
func sourceRecognitionTLSClone(base *http.Transport) *http.Transport {
	if base == nil {
		base = http.DefaultTransport.(*http.Transport)
	}
	transport := base.Clone()
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	}
	transport.TLSClientConfig.InsecureSkipVerify = true // Source RequestUtils default; per-instance approved.
	return transport
}

// Namespace binds the immutable credentials actually used by this client.
// Source cache excludes self/proxy; route changes remain a current-authority guard, not a new cache miss.
func sourceRecognitionNamespace(credentials Credentials) string {
	values := []string{credentials.APIKey, credentials.AccessToken}
	raw, _ := json.Marshal(values)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (c *Client) SourceRecognitionCacheNamespace() string {
	if c == nil {
		return ""
	}
	return c.recognition.namespace
}
