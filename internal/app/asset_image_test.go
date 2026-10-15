package app

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type assetTestTransport func(*http.Request) (*http.Response, error)

func (f assetTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAssetImagesUseFixedHostBoundedValidatedPublication(t *testing.T) {
	a, _, target := importTestApp(t)
	identity := importFixture(t, a, "movie")
	identity.PosterPath = "/poster.png"
	identity.BackdropPath = "/backdrop.png"
	importCache(t, a, "media:movie:123", identity)
	video := filepath.Join(target, "movie", "movie.mkv")
	importWrite(t, video, []byte("video"))
	asset := Asset{ID: "asset-images", RootID: "root", Media: identity, Path: video, State: "external", ScrapeState: "pending"}
	a.Store.Put(context.Background(), "asset", asset.ID, asset)
	var encoded bytes.Buffer
	picture := image.NewRGBA(image.Rect(0, 0, 2, 3))
	picture.Set(1, 1, color.RGBA{R: 200, A: 255})
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "image/png")
		w.Write(encoded.Bytes())
	}))
	defer server.Close()
	destination, _ := url.Parse(server.URL)
	prior := assetImageClient
	assetImageClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: assetTestTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Scheme != "https" || request.URL.Host != "image.tmdb.org" || !strings.HasPrefix(request.URL.Path, "/t/p/original/") {
			t.Error("image left official HTTPS host")
		}
		if request.Header.Get("Authorization") != "" {
			t.Error("credential forwarded to image host")
		}
		clone := request.Clone(request.Context())
		copy := *request.URL
		clone.URL = &copy
		clone.URL.Scheme, clone.URL.Host = destination.Scheme, destination.Host
		return server.Client().Transport.RoundTrip(clone)
	})}
	defer func() { assetImageClient = prior }()
	job := importClaim(t, a, "scrape", asset.ID)
	if err := a.runScrape(context.Background(), job, asset.ID); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("image requests=%d", requests)
	}
	if err := a.runScrape(context.Background(), job, asset.ID); err != nil {
		t.Fatal("repeat scrape", err)
	}
	if requests != 2 {
		t.Fatal("own verified image fetched again")
	}
	for _, name := range []string{"poster.jpg", "fanart.jpg"} {
		file, err := importOpen(filepath.Join(filepath.Dir(video), name))
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(file)
		file.Close()
		if !bytes.Equal(data, encoded.Bytes()) {
			t.Fatal("image content changed")
		}
	}
}

func TestAssetImagesRejectRedirectHTMLAndUnboundedBody(t *testing.T) {
	for _, scenario := range []string{"redirect", "html", "oversize"} {
		t.Run(scenario, func(t *testing.T) {
			a, _, target := importTestApp(t)
			identity := importFixture(t, a, "movie")
			asset := Asset{ID: "asset-test", RootID: "root", Media: identity, Path: filepath.Join(target, "movie", "movie.mkv")}
			job := Job{ID: "image-job"}
			prior := assetImageClient
			assetImageClient = &http.Client{Transport: assetTestTransport(func(request *http.Request) (*http.Response, error) {
				response := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("<html>not a poster</html>")), Request: request}
				switch scenario {
				case "redirect":
					response.StatusCode = 302
					response.Header.Set("Location", "https://untrusted.example/collect")
				case "oversize":
					response.ContentLength = 21 << 20
				}
				return response, nil
			}), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			defer func() { assetImageClient = prior }()
			output := filepath.Join(target, "movie", "poster.jpg")
			if err := a.fetchAssetImage(context.Background(), job, asset, "/image.jpg", output, asset.ID, func() error { return nil }); err == nil {
				t.Fatal("unsafe image published")
			}
			if _, err := os.Lstat(output); !os.IsNotExist(err) {
				t.Fatal("unsafe image target exists")
			}
		})
	}
}

func TestFNOSDisabledNeverClaimsVisible(t *testing.T) {
	a, _, _ := importTestApp(t)
	identity := importFixture(t, a, "movie")
	asset := Asset{ID: "fn-disabled", Media: identity, RootID: "root", State: "present", FNOSState: "pending"}
	a.Store.Put(context.Background(), "asset", asset.ID, asset)
	if err := a.runFNOSSync(context.Background(), Job{}, asset.ID); err != nil {
		t.Fatal(err)
	}
	a.Store.Get(context.Background(), "asset", asset.ID, &asset)
	if asset.FNOSState != "disabled" || asset.FNOSGUID != "" {
		t.Fatalf("false FN state %+v", asset)
	}
}
