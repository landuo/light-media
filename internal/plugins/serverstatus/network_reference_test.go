package serverstatus

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestActualRequestUtilsLoopbackTLSReference(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/plugins/server-status-http-original-reference.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Provenance Object
		Cases      []struct {
			Path, Mode   string
			Constructors []Object
			RequestCalls []Object `json:"request_calls"`
			Wire         []struct {
				Path    string
				Headers map[string]string
			}
			Result Object
		}
	}
	if err := decode(raw, &reference); err != nil {
		t.Fatal(err)
	}
	if len(reference.Cases) != 19 || !truth(reference.Provenance["strict_http_rejects_same_certificate"]) {
		t.Fatal("incomplete actual-source TLS reference")
	}
	for _, c := range reference.Cases {
		t.Run(c.Path+c.Mode, func(t *testing.T) {
			for _, call := range c.RequestCalls {
				if call["verify"] != false || call["cookies"] != nil || call["proxies"] != nil || call["stream"] != false || call["allow_redirects"] != true {
					t.Fatal("source per-request policy changed", call)
				}
				headers := call["headers"].(map[string]any)
				if headers["User-Agent"] != nil || headers["Accept"] != nil || headers["referer"] != nil || headers["Content-Type"] != "application/x-www-form-urlencoded; charset=UTF-8" {
					t.Fatal("source header construction changed", headers)
				}
			}
			for _, wire := range c.Wire {
				expectedContentType := "application/x-www-form-urlencoded; charset=UTF-8"
				if c.Path == "/redirect" && wire.Path == "/status" {
					expectedContentType = ""
				} // requests purges entity headers for 302.
				if wire.Headers["User-Agent"] != "python-urllib3/2.7.0" || wire.Headers["Accept-Encoding"] != "gzip, deflate, br, zstd" || wire.Headers["Content-Type"] != expectedContentType {
					t.Fatal("actual installed dependency wire defaults changed", wire.Headers)
				}
				for k := range wire.Headers {
					if strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "Cookie") || strings.EqualFold(k, "Referer") || strings.EqualFold(k, "Accept") {
						t.Fatal("fixture leaked or invented a header", k)
					}
				}
			}
			switch c.Path {
			case "/status":
				if !truth(c.Result["tls_failed_verification"]) || !truth(c.Result["tls_failure_cached"]) || number(c.Result["http_metrics"]) != 12 || truth(c.Result["none"]) {
					t.Fatal("independent HTTP/TLS strategies were conflated", c.Result)
				}
			case "/latin1":
				if c.Result["encoding"] != "ISO-8859-1" || !strings.HasSuffix(c.Result["text"].(string), "é\u0080") {
					t.Fatal("text/plain default is not UTF8/windows1252", c.Result)
				}
			case "/utf8", "/json-default":
				if !strings.HasSuffix(c.Result["text"].(string), "标签") {
					t.Fatal("explicit/JSON UTF8 decoding lost")
				}
			case "/invalid-utf8":
				if !strings.HasSuffix(c.Result["text"].(string), "���") {
					t.Fatal("Python UTF8 replacement boundary changed")
				}
			case "/apparent":
				if c.Result["encoding"] != nil || c.Result["apparent_encoding"] != "utf-8" || !strings.HasSuffix(c.Result["text"].(string), "标签") {
					t.Fatal("installed chardet fallback changed")
				}
			case "/gzip", "/deflate", "/deflate-raw", "/br", "/zstd":
				if !strings.HasPrefix(c.Result["text"].(string), "Active connections: 12\n") || c.Result["content_encoding"] == nil {
					t.Fatal("source automatically decompressed content was lost")
				}
			case "/redirect":
				if c.Result["final_path"] != "/status" || len(c.Wire) != 2 || number(c.Result["status"]) != 200 {
					t.Fatal("source redirect behavior changed")
				}
			case "/503":
				if truth(c.Result["none"]) || truth(c.Result["bool"]) || number(c.Result["status"]) != 503 {
					t.Fatal("response object and truthiness conflated")
				}
			case "/truncated", "/slow":
				if !truth(c.Result["none"]) || c.Result["request_error"] != "网络错误" || len(c.RequestCalls) != 1 {
					t.Fatal("request exception should become None without retry", c.Result)
				}
			case "/trickle":
				if truth(c.Result["none"]) || !truth(c.Result["elapsed_exceeded_read_timeout"]) || number(c.RequestCalls[0]["timeout"]) != .1 {
					t.Fatal("requests timeout was incorrectly treated as a whole-request deadline", c.Result)
				}
			}
			switch c.Mode {
			case "request-exception":
				if !truth(c.Result["none"]) || c.Result["request_error"] != "网络错误" || len(c.RequestCalls) != 1 {
					t.Fatal("normal RequestException handling changed")
				}
			case "unexpected":
				if c.Result["request_error"] != "synthetic unexpected request error" || len(c.RequestCalls) != 1 {
					t.Fatal("unexpected exception must reach plugin catch")
				}
			case "signature":
				if len(c.RequestCalls) != 2 || len(c.Constructors) != 2 || len(c.Constructors[1]) != 0 || number(c.Result["status"]) != 200 {
					t.Fatal("TypeError compatibility retry missing")
				}
			}
		})
	}
}

func TestNetworkReferenceHasMatchingPinnedDependencyVersions(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/plugins/server-status-http-original-reference.json")
	if err != nil {
		t.Fatal(err)
	}
	var ref struct {
		Provenance struct{ Distributions map[string]string }
	}
	if json.Unmarshal(raw, &ref) != nil {
		t.Fatal("reference metadata invalid")
	}
	for name, version := range map[string]string{"requests": "2.34.2", "urllib3": "2.7.0", "certifi": "2026.7.22", "idna": "3.18", "charset-normalizer": "3.4.9", "chardet": "7.4.3", "brotli": "1.2.0", "zstandard": "0.25.0"} {
		if ref.Provenance.Distributions[name] != version {
			t.Fatal("reference runtime drift", name)
		}
	}
}
