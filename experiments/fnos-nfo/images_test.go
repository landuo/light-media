package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
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
	expected := expectedImage{Label: "Synthetic:poster.jpg", Hash: shaHex(b), Format: "jpeg", Width: 3, Height: 2}
	out, e := a.fetchImage(context.Background(), srv.URL+localImageRoute+"poster.jpg", expected)
	if e != nil || out["fixture_exact_match"] != true || out["width"] != 3 || out["height"] != 2 || out["bytes_verified"] != true {
		t.Fatalf("verification %+v %v", out, e)
	}
	if _, e = a.fetchImage(context.Background(), srv.URL+localImageRoute+"redirect.jpg", expected); e == nil {
		t.Fatal("redirect accepted")
	}
	if destHits != 0 {
		t.Fatal("external target called")
	}
	if _, e = a.fetchImage(context.Background(), dest.URL+localImageRoute+"poster.jpg", expected); e == nil {
		t.Fatal("direct external URL accepted")
	}
}

func TestWrongRoleOrWorkHashCannotPass(t *testing.T) {
	poster := syntheticJPEG(t)
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 4, 1))
	img.Set(2, 0, color.RGBA{G: 255, A: 255})
	png.Encode(&buf, img)
	fanart := buf.Bytes()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(fanart) }))
	defer srv.Close()
	a := api{host: srv.URL + "/v", client: srv.Client()}
	expected := expectedImage{Label: "WorkA:poster.jpg", Hash: shaHex(poster), Format: "jpeg", Width: 3, Height: 2}
	out, e := a.fetchImage(context.Background(), srv.URL+localImageRoute+"poster.jpg", expected)
	if e == nil || out["fixture_exact_match"] != false || out["fixture_expected"] != "WorkA:poster.jpg" {
		t.Fatalf("swapped-role accepted %+v %v", out, e)
	}
	expected = expectedImage{Label: "WorkA:fanart.png", Hash: shaHex(fanart), Format: "png", Width: 4, Height: 1}
	out, e = a.fetchImage(context.Background(), srv.URL+localImageRoute+"fanart.png", expected)
	if e != nil || out["fixture_exact_match"] != true || out["format"] != "png" {
		t.Fatalf("PNG failed %+v %v", out, e)
	}
	expected.Hash = shaHex(poster)
	out, e = a.fetchImage(context.Background(), srv.URL+localImageRoute+"fanart.png", expected)
	if e == nil || out["fixture_exact_match"] != false {
		t.Fatal("different-work hash accepted")
	}
}
func TestImagesBindIdentityAndFieldToLocalFixtures(t *testing.T) {
	scratch(t)
	id := syntheticID
	root := "work/new-fixtures"
	os.Mkdir(root, 0700)
	type fixture struct {
		title, kind, guid string
		refs              map[string]string
	}
	works := []fixture{}
	payload := map[string][]byte{}
	for i, suffix := range []string{"-Movie (2024)", "-Series", "-Animation"} {
		dir := filepath.Join(root, prefix+id+suffix)
		os.Mkdir(dir, 0700)
		title := prefix + id + " Synthetic " + suffix
		kind, xmlRoot, nfo := "TV", "tvshow", "tvshow.nfo"
		if i == 0 {
			kind, xmlRoot, nfo = "Movie", "movie", prefix+id+suffix+".nfo"
		}
		os.WriteFile(filepath.Join(dir, nfo), []byte("<"+xmlRoot+"><title>"+title+"</title></"+xmlRoot+">"), 0600)
		f := fixture{title: title, kind: kind, guid: fmt.Sprintf("item%d", i), refs: map[string]string{}}
		for j, v := range []struct{ field, file string }{{"posters", "poster.jpg"}, {"backdrops", "fanart.png"}} {
			img := image.NewRGBA(image.Rect(0, 0, 3+j, 2+i))
			img.Set(1, 1, color.RGBA{R: uint8(30 + i*60), G: uint8(50 + j*100), A: 255})
			var b bytes.Buffer
			if j == 0 {
				jpeg.Encode(&b, img, nil)
			} else {
				png.Encode(&b, img)
			}
			os.WriteFile(filepath.Join(dir, v.file), b.Bytes(), 0600)
			ref := fmt.Sprintf("/%d/%s", i, v.file)
			payload[localImageRoute+strings.TrimPrefix(ref, "/")] = b.Bytes()
			f.refs[v.field] = ref
		}
		works = append(works, f)
	}
	imageGets := 0
	foreignTitle := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if b, ok := payload[r.URL.Path]; ok {
			imageGets++
			w.Write(b)
			return
		}
		var d any
		if r.URL.Path == "/v/api/v1/item/list" {
			list := []any{}
			for _, f := range works {
				list = append(list, map[string]string{"guid": f.guid, "type": f.kind})
			}
			d = map[string]any{"list": list}
		} else {
			for _, f := range works {
				if r.URL.Path == "/v/api/v1/item/"+f.guid {
					title := f.title
					if foreignTitle {
						title = "unknown source title"
					}
					d = map[string]string{"guid": f.guid, "type": f.kind, "title": title, "ancestor_guid": "owned", "parent_guid": "", "posters": f.refs["posters"], "backdrops": f.refs["backdrops"]}
				}
			}
		}
		if d == nil {
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": d})
	}))
	defer srv.Close()
	a := api{host: srv.URL + "/v", client: srv.Client()}
	out, e := a.images(context.Background(), receipt{ExperimentID: id, OwnedGUID: "owned"}, true, root)
	if e != nil || len(out) != 3 || imageGets != 6 {
		t.Fatalf("verification failed %v images=%d", e, imageGets)
	}

	before, e := fixtureCatalog(id, root)
	if e != nil {
		t.Fatal(e)
	}
	canonicalDirs := []string{"合成电影 (2024)", "合成普通剧", "合成国漫"}
	for i, suffix := range []string{"-Movie (2024)", "-Series", "-Animation"} {
		old := prefix + id + suffix
		dir := filepath.Join(root, canonicalDirs[i])
		if e := os.Rename(filepath.Join(root, old), dir); e != nil {
			t.Fatal(e)
		}
		if i == 0 {
			if e := os.Rename(filepath.Join(dir, old+".nfo"), filepath.Join(dir, canonicalDirs[i]+".nfo")); e != nil {
				t.Fatal(e)
			}
		}
	}
	after, e := fixtureCatalog(id, root)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("canonical layout changed identity or exact role hashes")
	}
	imageGets = 0
	a.requests = 0
	if _, e = a.images(context.Background(), receipt{ExperimentID: id, OwnedGUID: "owned"}, true, root); e != nil || imageGets != 6 {
		t.Fatalf("canonical image verification failed: %v", e)
	}
	foreignTitle = true
	imageGets = 0
	a.requests = 0
	if _, e = a.images(context.Background(), receipt{ExperimentID: id, OwnedGUID: "owned"}, true, root); e == nil || imageGets != 0 {
		t.Fatal("unknown identity fetched images")
	}
	// Identical historical poster/fanart cannot establish role distinction.
	dir := filepath.Join(root, "合成普通剧")
	b, _ := os.ReadFile(filepath.Join(dir, "poster.jpg"))
	os.Remove(filepath.Join(dir, "fanart.png"))
	os.WriteFile(filepath.Join(dir, "fanart.jpg"), b, 0600)
	if _, e = fixtureCatalog(id, root); e == nil {
		t.Fatal("ambiguous same-byte fixture roles accepted")
	}
}
