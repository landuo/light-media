package autosignin

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sync"
)

// OrdinaryOCRRequest is a host-side typed request, not SDK configuration.
// Binding supplies authority and frozen proxy routing. No caller TLS switch.
type OrdinaryOCRRequest struct{ Method, URL, Base64Image string }
type OrdinaryOCRResponse struct {
	Status      int
	Content     []byte
	ContentType string
}
type ordinaryOCRMode struct{ contentType string }

func (b *BoundSiteHTTP) DoOrdinaryOCR(ctx context.Context, input OrdinaryOCRRequest) (*OrdinaryOCRResponse, error) {
	request := PageRequest{Method: input.Method, URL: input.URL, Binary: true}
	switch input.Method {
	case "GET":
	case "POST":
		request.NoCookies = true
		request.JSON = Object{"base64_img": input.Base64Image}
		// This fresh RequestUtils instance has no image Cookie/UA/referrer.
		request.Headers = Object{"User-Agent": nil, "Content-Type": "application/json", "Accept": nil}
	default:
		return nil, ErrSiteTargetDenied
	}
	mode := &ordinaryOCRMode{}
	response, err := b.doWithOCR(ctx, request, "", mode)
	if err != nil || response == nil {
		return nil, err
	}
	return &OrdinaryOCRResponse{Status: response.Status, Content: response.Content, ContentType: mode.contentType}, nil
}

func ordinaryOCRRedirect(original string, request *http.Request) error {
	u := request.URL
	if u == nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ErrSiteTargetDenied
	}
	// Never widen credentials to another origin, including same hostname on
	// another scheme or port. The native ordinary-site path still rejects hops.
	if originOf(u) != original {
		request.Header.Del("Cookie")
		request.Header.Del("Authorization")
		request.Header.Del("Proxy-Authorization")
	}
	return nil
}

// requests receives response cookies during redirects. Keep those semantics
// only inside each exact origin, even for Domain cookies or reused host ports.
type ordinaryOCRJar struct {
	mu      sync.Mutex
	origins map[string]*cookiejar.Jar
}

func newOrdinaryOCRJar() *ordinaryOCRJar {
	return &ordinaryOCRJar{origins: map[string]*cookiejar.Jar{}}
}
func (j *ordinaryOCRJar) Cookies(u *url.URL) []*http.Cookie {
	j.mu.Lock()
	defer j.mu.Unlock()
	jar := j.origins[originOf(u)]
	if jar == nil {
		return nil
	}
	return jar.Cookies(u)
}
func (j *ordinaryOCRJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	key := originOf(u)
	jar := j.origins[key]
	if jar == nil {
		jar, _ = cookiejar.New(nil)
		j.origins[key] = jar
	}
	jar.SetCookies(u, cookies)
}
