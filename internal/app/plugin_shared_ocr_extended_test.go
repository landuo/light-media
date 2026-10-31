package app

import (
	"encoding/binary"
	"io"
	"light-media/internal/plugins"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestSharedOCRExtendedSourceHTTP(t *testing.T) {
	var mode atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/captcha/base64" {
			t.Error("unexpected owned extended route")
			w.WriteHeader(502)
			return
		}
		switch mode.Load() {
		case 0:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"result":["X",123456789012345678901234567890]}`)
		case 1:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `broken-json`)
		case 2:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `[]`)
		case 3:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(500)
			_, _ = io.WriteString(w, `broken-json`)
		case 4:
			w.Header().Set("Content-Type", "application/octet-stream")
			raw := []byte{255, 254}
			for _, r := range `{"result":"BOM"}` {
				pair := make([]byte, 2)
				binary.LittleEndian.PutUint16(pair, uint16(r))
				raw = append(raw, pair...)
			}
			_, _ = w.Write(raw)
		}
	}))
	defer server.Close()
	a := newOwnedOCRApp(t, server.URL)
	b64 := "QQ"
	reply, err := ownedOCRCall(t, a, plugins.OCRRequest{ImageBase64: &b64})
	if err != nil || reply.Value == nil || reply.Value.Kind != "list" || len(reply.Value.Items) != 2 || reply.Value.Items[1].Number != "123456789012345678901234567890" {
		t.Fatalf("typed source result %v %#v", err, reply)
	}
	for i, class := range []string{"JSONDecodeError", "AttributeError"} {
		mode.Store(int32(i + 1))
		reply, err = ownedOCRCall(t, a, plugins.OCRRequest{ImageBase64: &b64})
		if err != nil || reply.SourceError != class || reply.Value != nil {
			t.Fatalf("typed source failure %s %v %#v", class, err, reply)
		}
	}
	mode.Store(3)
	reply, err = ownedOCRCall(t, a, plugins.OCRRequest{ImageBase64: &b64})
	ownedOCRString(t, reply, err, "")
	mode.Store(4)
	reply, err = ownedOCRCall(t, a, plugins.OCRRequest{ImageBase64: &b64})
	ownedOCRString(t, reply, err, "BOM")
}
func TestSharedOCRSourceURLPreparationFallback(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.URL.Path != "//captcha/base64" {
			t.Errorf("literal trailing slash lost: %s", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		if string(raw) != `{"base64_img": "Qg=="}` {
			t.Errorf("fallback lost: %s", raw)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"result":"FALLBACK"}`)
	}))
	defer server.Close()
	a := newOwnedOCRApp(t, server.URL+"/")
	fallback := "Qg"
	for _, address := range []string{"relative-missing-scheme", "data:image/png;base64,", "data:\u0130mage/png;base64,QQ"} {
		reply, err := ownedOCRCall(t, a, plugins.OCRRequest{ImageURL: &address, ImageBase64: &fallback})
		ownedOCRString(t, reply, err, "FALLBACK")
	}
	if calls.Load() != 3 {
		t.Fatal("invalid URL unexpectedly requested a remote or lost fallback")
	}
	empty := newOwnedOCRApp(t, "")
	reply, err := ownedOCRCall(t, empty, plugins.OCRRequest{ImageBase64: &fallback})
	ownedOCRString(t, reply, err, "")
	if calls.Load() != 3 {
		t.Fatal("explicit empty OCR_HOST fell back to service")
	}
}
