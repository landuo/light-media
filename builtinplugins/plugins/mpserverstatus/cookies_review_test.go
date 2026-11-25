package mpserverstatus

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestIndependentActual312CookiePolicy(t *testing.T) {
	for _, path := range []string{"reference/source-cookie-review.json", "reference/source-cookie-local-review.json", "reference/source-cookie-attribute-review.json"} {
		raw, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		var source struct {
			Origin, Target string
			Cases          []struct {
				Origin, Target string
				Set            string   `json:"set_cookie"`
				Header         *string  `json:"cookie_header"`
				Domains        []string `json:"stored_domains"`
			}
		}
		if e = json.Unmarshal(raw, &source); e != nil {
			t.Fatal(e)
		}
		for _, c := range source.Cases {
			t.Run(path+"/"+c.Set, func(t *testing.T) {
				origin, target := c.Origin, c.Target
				if origin == "" {
					origin, target = source.Origin, source.Target
				}
				a, _ := url.Parse(origin)
				b, _ := url.Parse(target)
				jar := newSourceCookieJar()
				response := &http.Response{Header: http.Header{"Set-Cookie": []string{c.Set}}}
				jar.SetCookies(a, response.Cookies())
				request := &http.Request{Header: http.Header{}}
				for _, cookie := range jar.Cookies(b) {
					request.AddCookie(cookie)
				}
				want := ""
				if c.Header != nil {
					want = *c.Header
				}
				if request.Header.Get("Cookie") != want {
					t.Fatalf("source Cookie=%q got=%q", want, request.Header.Get("Cookie"))
				}
				if c.Domains != nil && strings.Join(jar.domains(), ",") != strings.Join(c.Domains, ",") {
					t.Fatal("source retained-domain state", jar.domains(), c.Domains)
				}
			})
		}
	}
}
