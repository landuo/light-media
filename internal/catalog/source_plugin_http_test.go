package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
)

type sourceProviderBrokenBody struct{}

func (sourceProviderBrokenBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (sourceProviderBrokenBody) Close() error             { return nil }

func TestSourcePluginHTTPFixedResponseAndRetry(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-host-integration-20261031/source-models/http-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Commit    string
		Responses []struct {
			Source   string
			Status   int
			Body     string
			Expected any
			Error    *string
		}
		Retries []struct {
			Failure  string
			Recovery bool
			Calls    int
			Returned bool
		}
	}
	if json.Unmarshal(raw, &oracle) != nil || oracle.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(oracle.Responses) != 16 || len(oracle.Retries) != 8 {
		t.Fatal("fixed HTTP oracle missing")
	}
	for _, row := range oracle.Responses {
		t.Run(row.Source+"/"+http.StatusText(row.Status)+"/"+row.Body, func(t *testing.T) {
			calls := 0
			e := NewExplorer(nil, roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: row.Status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(row.Body))}, nil
			}))
			defer e.CloseIdleConnections()
			var actual json.RawMessage
			var err error
			if row.Source == "douban" {
				actual, err = e.SourceDoubanInformation(context.Background(), "movie", "901", false)
			} else {
				actual, err = e.SourceBangumiInformation(context.Background(), "902", "20261031", false)
			}
			if row.Source == "douban" && (err != nil) != (row.Error != nil) {
				t.Fatal("source Douban exception differs", err, row.Error)
			}
			var value any
			if err == nil {
				if json.Unmarshal(actual, &value) != nil {
					t.Fatal("invalid raw projection")
				}
			}
			if !reflect.DeepEqual(value, row.Expected) || calls != 1 {
				t.Fatal("response semantics differ", value, row.Expected, calls)
			}
		})
	}
	for _, row := range oracle.Retries {
		for _, source := range []string{"douban", "bangumi"} {
			t.Run(source+"/"+row.Failure+map[bool]string{true: "/recover", false: "/fail"}[row.Recovery], func(t *testing.T) {
				calls := 0
				e := NewExplorer(nil, roundTripFunc(func(*http.Request) (*http.Response, error) {
					calls++
					if calls == 1 || !row.Recovery {
						switch row.Failure {
						case "ConnectionError":
							return nil, &net.OpError{Op: "read", Net: "tcp", Err: errors.New("owned connection lost")}
						case "ReadTimeout":
							return nil, &net.DNSError{Err: "owned timeout", IsTimeout: true}
						case "ChunkedEncodingError":
							return &http.Response{StatusCode: 200, Header: http.Header{}, Body: sourceProviderBrokenBody{}}, nil
						default:
							return nil, errors.New("owned generic request failure")
						}
					}
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
				}))
				defer e.CloseIdleConnections()
				var actual json.RawMessage
				var err error
				if source == "douban" {
					actual, err = e.SourceDoubanInformation(context.Background(), "movie", "901", false)
				} else {
					actual, err = e.SourceBangumiInformation(context.Background(), "902", "20261031", false)
				}
				if calls != row.Calls || (err == nil && len(actual) > 0) != row.Returned {
					t.Fatal("source retry differs", calls, row.Calls, err, row.Returned)
				}
			})
		}
	}
}
