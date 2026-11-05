package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/releasemeta"
)

func TestOriginalHostMediaAniListFixedSourceModels(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-host-business-20261031/anilist-source/oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		SourceCommit string `json:"source_commit"`
		Cases        []struct {
			Case            string
			Input, Expected json.RawMessage
			Error           string
		} `json:"model_cases"`
	}
	if json.Unmarshal(raw, &corpus) != nil || corpus.SourceCommit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(corpus.Cases) != 35 {
		t.Fatal("source models missing")
	}
	parser, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: releasemeta.Options{SourceMode: "legacy"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range corpus.Cases {
		t.Run(row.Case, func(t *testing.T) {
			value, err := decodeSourceJSON(row.Input)
			if err != nil {
				t.Fatal(err)
			}
			fields, err := originalHostAniListFields(sourceMap(value), parser)
			if row.Error != "" {
				if err == nil {
					t.Fatal("source error became successful model", row.Error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			actual, err := originalHostMediaSourceSerialize(fields)
			if err != nil {
				t.Fatal(err)
			}
			decoded, _ := decodeSourceJSON(actual)
			got := sourceMap(decoded)
			delete(got, "recognize_cache_hit")
			wanted, _ := decodeSourceJSON(row.Expected)
			if !originalBaseValueEqual(got, wanted) {
				for key, want := range sourceMap(wanted) {
					if !originalBaseValueEqual(got[key], want) {
						t.Errorf("%s target=%#v source=%#v", key, got[key], want)
					}
				}
			}
		})
	}
}

type originalHostAniListReply struct {
	Host, Method, Path string
	Status             int
	Body               json.RawMessage `json:"body"`
	RequestBody        json.RawMessage `json:"request_body"`
}

// RequestBody has its own JSON name; keep decoding separate from response Body.
func (r *originalHostAniListReply) UnmarshalJSON(raw []byte) error {
	var value struct {
		Host, Method, Path string
		Status             int
		Body               json.RawMessage
		RequestBody        json.RawMessage `json:"request_body"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	r.Host, r.Method, r.Path, r.Status, r.Body, r.RequestBody = value.Host, value.Method, value.Path, value.Status, value.Body, value.RequestBody
	return nil
}

func installOriginalHostAniListTLS(t *testing.T, replies []originalHostAniListReply) func() int {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"trace.moe", "graphql.anilist.co", "raw.githubusercontent.com"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true, IsCA: true}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	var mu sync.Mutex
	count := 0
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if count >= len(replies) {
			t.Errorf("unexpected owned AniList request %s %s", r.Host, r.URL.Path)
			http.Error(w, "unexpected", 500)
			return
		}
		reply := replies[count]
		count++
		if r.Host != reply.Host || r.Method != reply.Method || r.URL.Path != reply.Path {
			t.Errorf("source request order differs got %s %s %s expected %#v", r.Host, r.Method, r.URL.Path, reply)
		}
		if len(reply.RequestBody) > 0 {
			body, _ := io.ReadAll(r.Body)
			if !originalHostSubscriptionJSONEqual(body, reply.RequestBody) {
				t.Errorf("source GraphQL complete fields or variables changed got=%s want=%s", body, reply.RequestBody)
			}
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("public provider received credentials")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(reply.Status)
		_, _ = w.Write(reply.Body)
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	server.StartTLS()
	t.Cleanup(server.Close)
	old := http.DefaultTransport
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: pool}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || port != "443" || host != "trace.moe" && host != "graphql.anilist.co" && host != "raw.githubusercontent.com" {
			return nil, fmt.Errorf("owned AniList fixture denied unexpected address")
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}}
	http.DefaultTransport = transport
	t.Cleanup(func() { transport.CloseIdleConnections(); http.DefaultTransport = old })
	return func() int { mu.Lock(); defer mu.Unlock(); return count }
}

func TestOriginalHostMediaAniListActualSDKFixedSourceNativeCases(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-host-business-20261031/anilist-source/native-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	type nativeCase struct {
		Case         string
		Request      plugins.MediaRecognizeRequest
		Replies      []originalHostAniListReply
		Expected     json.RawMessage
		FreshProcess bool `json:"fresh_process"`
	}
	var corpus struct {
		Cases []nativeCase `json:"cases"`
	}
	if json.Unmarshal(raw, &corpus) != nil || len(corpus.Cases) != 18 {
		t.Fatal("source native cases missing")
	}
	// Source client state deliberately outlives individual cache=false calls.
	for index := 0; index < len(corpus.Cases); {
		rows := []nativeCase{corpus.Cases[index]}
		index++
		for index < len(corpus.Cases) && !corpus.Cases[index].FreshProcess {
			rows = append(rows, corpus.Cases[index])
			index++
		}
		t.Run(rows[0].Case, func(t *testing.T) {
			a, _, worker := ownedHostServicesFixture(t)
			replies := []originalHostAniListReply{}
			for _, row := range rows {
				replies = append(replies, row.Replies...)
			}
			calls := installOriginalHostAniListTLS(t, replies)
			config := a.Settings()
			off := false
			config.MediaRecognizeShare = &off
			ownedDownloaderSave(t, a, config)
			for _, row := range rows {
				result, err := ownedHostServicesCall(t, worker, plugins.MediaRecognizeCapability, row.Request)
				if err != nil {
					t.Fatal(err)
				}
				value, err := decodeSourceJSON(result)
				if err != nil {
					t.Fatal(err)
				}
				if m := sourceMap(value); m != nil {
					delete(m, "recognize_cache_hit")
				}
				want, _ := decodeSourceJSON(row.Expected)
				if !originalBaseValueEqual(value, want) {
					for key, w := range sourceMap(want) {
						if !originalBaseValueEqual(sourceMap(value)[key], w) {
							t.Errorf("%s target=%#v source=%#v", key, sourceMap(value)[key], w)
						}
					}
					if want == nil && value != nil {
						t.Fatal("source None became data")
					}
				}
			}
			if calls() != len(replies) {
				t.Fatal("native HTTP chain did not execute source calls", calls(), len(replies))
			}
		})
	}
}

func TestOriginalHostMediaAniListActualSDKDefaultSourceAndSourceFailure(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-host-business-20261031/anilist-source/oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Cases []struct {
			Case     string
			Request  plugins.MediaRecognizeRequest
			Replies  []originalHostAniListReply
			Expected json.RawMessage
			Error    string
		} `json:"recognition_cases"`
	}
	if err = json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	for _, row := range corpus.Cases {
		if row.Case != "search" && row.Case != "null_people_error" {
			continue
		}
		t.Run(row.Case, func(t *testing.T) {
			a, _, worker := ownedHostServicesFixture(t)
			calls := installOriginalHostAniListTLS(t, row.Replies)
			config := a.Settings()
			off := false
			config.MediaRecognizeShare = &off
			ownedDownloaderSave(t, a, config)
			fresh := false
			row.Request.Cache = &fresh
			if row.Case == "search" {
				// Same immutable startup setting consumed by normal host dispatch. The
				// fixed source module oracle has settings.RECOGNIZE_SOURCE='anilist'.
				a.originalHostMediaRecognition.Source = "anilist"
				row.Request.Source = ""
			}
			a.systemMessages.get("system")
			result, err := ownedHostServicesCall(t, worker, plugins.MediaRecognizeCapability, row.Request)
			if row.Error != "" {
				// ChainBase.__execute_system_modules catches this source exception,
				// publishes its system error, and preserves the None accumulator.
				value, decodeErr := decodeSourceJSON(result)
				if err != nil || decodeErr != nil || value != nil {
					t.Fatal("source Chain must fold module failure to None", string(result), err)
				}
				a.wg.Wait()
				messages := a.systemMessages.get("system")
				if !strings.Contains(string(messages), "AniList") {
					t.Fatal("source module failure diagnostic missing", string(messages))
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				value, _ := decodeSourceJSON(result)
				delete(sourceMap(value), "recognize_cache_hit")
				expected, _ := decodeSourceJSON(row.Expected)
				if !originalBaseValueEqual(value, expected) {
					t.Fatal("default source AniList model differs")
				}
			}
			if calls() != len(row.Replies) {
				t.Fatal("source request chain differs", calls(), len(row.Replies))
			}
		})
	}
}

func TestOriginalHostMediaAniListFixedChainSourceSelection(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-host-business-20261031/anilist-source/dispatch-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeSourceJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	rows := sourceRows(sourceMap(decoded)["cases"])
	if len(rows) != 8 {
		t.Fatal("fixed chain source cases missing")
	}
	for _, value := range rows {
		row := sourceMap(value)
		t.Run(sourceText(row["case"]), func(t *testing.T) {
			kwargs, err := originalCatalogRecognitionCallKwargs(nil, sourceMap(row["request"]))
			if err != nil {
				t.Fatal(err)
			}
			result := []any{kwargs["source"], kwargs["tmdbid"], kwargs["doubanid"], kwargs["bangumiid"], kwargs["anilistid"]}
			if !originalBaseValueEqual(result, row["expected"]) {
				t.Fatal("fixed Chain ID precedence differs", result, row["expected"])
			}
		})
	}
}
