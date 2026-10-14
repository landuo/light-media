package main

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSyntheticImageReferenceScope(t *testing.T) {
	host := "https://synthetic.invalid:8080/v"
	for _, ref := range []string{"/opaque/item/poster.jpg", "opaque/poster.jpg", "/v/api/v1/sys/img/opaque.jpg", "https://synthetic.invalid:8080/v/api/v1/sys/img/opaque.jpg"} {
		target, shape := imageReference(host, ref)
		if target == "" || shape["fetch_allowed"] != true {
			t.Fatalf("valid local ref refused %v", shape)
		}
	}
	for _, ref := range []string{"https://external.invalid/a.jpg", "//external.invalid/a.jpg", "http://synthetic.invalid:8080/v/api/v1/sys/img/a.jpg", "/v/api/v1/sys/rimg/a.jpg", "/v/api/v1/sys/img/../secret", "/foo/../../secret", "/foo/%2e%2e/secret", "/foo/%2fsecret", "/a.jpg?token=secret", "/a.jpg#secret", "https://user:secret@synthetic.invalid:8080/v/api/v1/sys/img/a.jpg", "data:image/png;base64,abc"} {
		target, shape := imageReference(host, ref)
		if target != "" || shape["fetch_allowed"] != false {
			t.Fatalf("unsafe ref accepted %v", shape)
		}
		for _, v := range shape {
			if strings.Contains(strings.ToLower(strings.TrimSpace(toText(v))), "secret") {
				t.Fatal("reference leaked in shape")
			}
		}
	}
}
func toText(v any) string { s, _ := v.(string); return s }
func syntheticJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	img.Set(1, 1, color.RGBA{R: 230, G: 10, B: 20, A: 255})
	var b bytes.Buffer
	if e := jpeg.Encode(&b, img, nil); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestSyntheticImageBytesHashAndRedirect(t *testing.T) {
	b := syntheticJPEG(t)
	destHits := 0
	dest := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { destHits++ }))
	defer dest.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Error("image resize/query supplied")
		}
		if r.Header.Get("Authorization") != "synthetic-token" {
			t.Error("missing authentication")
		}
		if strings.HasSuffix(r.URL.Path, "redirect.jpg") {
			http.Redirect(w, r, dest.URL, 302)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(b)
	}))
	defer srv.Close()
	a := api{host: srv.URL + "/v", token: "synthetic-token", client: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	hashes := map[string][]string{shaHex(b): {"Synthetic:poster.jpg"}}
	out, e := a.fetchImage(context.Background(), srv.URL+localImageRoute+"poster.jpg", hashes)
	if e != nil || out["fixture_exact_match"] != true || out["width"] != 3 || out["height"] != 2 || out["bytes_verified"] != true {
		t.Fatalf("verification %+v %v", out, e)
	}
	if _, e = a.fetchImage(context.Background(), srv.URL+localImageRoute+"redirect.jpg", hashes); e == nil {
		t.Fatal("redirect accepted")
	}
	if destHits != 0 {
		t.Fatal("external target called")
	}
	if _, e = a.fetchImage(context.Background(), dest.URL+localImageRoute+"poster.jpg", hashes); e == nil {
		t.Fatal("direct external URL accepted")
	}
}
