package artifacts

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type ownedPythonPackageBody struct {
	reader io.Reader
	reads  int
	closed bool
}

func (b *ownedPythonPackageBody) Read(p []byte) (int, error) {
	b.reads++
	return b.reader.Read(p)
}
func (b *ownedPythonPackageBody) Close() error { b.closed = true; return nil }

func ownedLargePythonPackage() string {
	return `{"OwnedLarge":{"name":"owned metadata","version":"2.0","v2":true,"history":{"2.0":"current"},"padding":"` + strings.Repeat("x", 2<<20) + `"}}`
}

func ownedPythonPackageClient(t *testing.T, status int, length int64, body *ownedPythonPackageBody) *http.Client {
	t.Helper()
	return &http.Client{Transport: repositoryReplayTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != "raw.githubusercontent.com" || (r.URL.Path != "/Owner/Repo/main/package.v2.json" && r.URL.Path != "/Owner/Repo/main/package.json") || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Fatalf("unexpected owned request: %s %s", r.Method, r.URL)
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: body, ContentLength: length, Request: r}, nil
	})}
}

func TestPythonSourcePackageBodyBeyondNativeIndexBudget(t *testing.T) {
	payload := ownedLargePythonPackage()
	if int64(len(payload)) <= MaxIndexBytes {
		t.Fatal("owned counterexample must cross the old index bound")
	}
	for _, version := range []string{"v2", ""} {
		for _, length := range []int64{int64(len(payload)), -1} {
			body := &ownedPythonPackageBody{reader: strings.NewReader(payload)}
			values, err := DiscoverGitHubSourcePackage(context.Background(), "https://github.com/Owner/Repo", version, ownedPythonPackageClient(t, 200, length, body))
			if err != nil || len(values) != 1 {
				t.Fatalf("version=%q length=%d: values=%v err=%v", version, length, values, err)
			}
			value := values[0]
			if value.PluginID != "OwnedLarge" || value.CurrentVersion != "2.0" || value.History["2.0"] != "current" || value.SourcePackageVersion != version || value.SourceDisplay == nil || value.UnavailableReason == "" || value.Current.Manifest.Runtime != "python" {
				t.Fatalf("display-only projection changed: %+v", value)
			}
			if !body.closed || body.reads == 0 {
				t.Fatal("successful body not consumed and closed")
			}
		}
	}
}

type ownedPythonReadFailure struct{}

func (ownedPythonReadFailure) Read([]byte) (int, error) {
	return 0, errors.New("owned-private-URL-cookie-sentinel")
}

type ownedPythonCancelReader struct{ cancel context.CancelFunc }

func (r ownedPythonCancelReader) Read(p []byte) (int, error) {
	r.cancel()
	return copy(p, `{}`), io.EOF
}

func TestPythonSourcePackageReaderFailuresAndCancellation(t *testing.T) {
	for _, status := range []int{404, 503} {
		body := &ownedPythonPackageBody{reader: strings.NewReader("unread error body")}
		values, err := DiscoverGitHubSourcePackage(context.Background(), "https://github.com/Owner/Repo", "v2", ownedPythonPackageClient(t, status, 17, body))
		if status == 404 && (err != nil || len(values) != 0) || status == 503 && err == nil || !body.closed || body.reads != 0 {
			t.Fatalf("status=%d values=%v err=%v reads=%d closed=%v", status, values, err, body.reads, body.closed)
		}
	}
	body := &ownedPythonPackageBody{reader: ownedPythonReadFailure{}}
	_, err := DiscoverGitHubSourcePackage(context.Background(), "https://github.com/Owner/Repo", "v2", ownedPythonPackageClient(t, 200, -1, body))
	if err == nil || err.Error() != "artifact response read failed" || !body.closed {
		t.Fatal("read error must be closed and redacted", err, body.closed)
	}
	body = &ownedPythonPackageBody{reader: strings.NewReader(ownedLargePythonPackage() + `trailing`)}
	if _, err = DiscoverGitHubSourcePackage(context.Background(), "https://github.com/Owner/Repo", "v2", ownedPythonPackageClient(t, 200, -1, body)); err == nil || !body.closed {
		t.Fatal("large malformed document became success", err, body.closed)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body = &ownedPythonPackageBody{reader: ownedPythonCancelReader{cancel: cancel}}
	if _, err = DiscoverGitHubSourcePackage(ctx, "https://github.com/Owner/Repo", "v2", ownedPythonPackageClient(t, 200, -1, body)); !errors.Is(err, context.Canceled) || !body.closed {
		t.Fatal("cancel at final read was lost", err, body.closed)
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	reads := 0
	reader := pythonSourceContextReader{ctx: ctx, reader: repositoryReadFunc(func([]byte) (int, error) { reads++; return 0, io.EOF })}
	if _, err = reader.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) || reads != 0 {
		t.Fatal("canceled body read reached underlying input", err, reads)
	}
}

type repositoryReadFunc func([]byte) (int, error)

func (f repositoryReadFunc) Read(p []byte) (int, error) { return f(p) }

func TestPythonSourcePackageRetainsTransportGuards(t *testing.T) {
	for _, mode := range []string{"downgrade", "redirect-chain", "previous-policy"} {
		t.Run(mode, func(t *testing.T) {
			calls, previousCalls := 0, 0
			client := &http.Client{Transport: repositoryReplayTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				location := "https://raw.githubusercontent.com/Owner/Repo/main/package.v2.json?next=owned"
				if mode == "downgrade" {
					location = "http://raw.githubusercontent.com/Owner/Repo/main/package.v2.json"
				}
				return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{location}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
			})}
			if mode == "previous-policy" {
				client.CheckRedirect = func(*http.Request, []*http.Request) error { previousCalls++; return errors.New("owned policy denial") }
			}
			_, err := DiscoverGitHubSourcePackage(context.Background(), "https://github.com/Owner/Repo", "v2", client)
			wantCalls := 1
			if mode == "redirect-chain" {
				wantCalls = 5
			}
			if err == nil || err.Error() != "artifact request failed" || calls != wantCalls || mode == "previous-policy" && previousCalls != 1 {
				t.Fatal("redirect guard changed", mode, calls, previousCalls, err)
			}
		})
	}
	calls := 0
	client := &http.Client{Transport: repositoryReplayTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("must not run") })}
	if _, err := DiscoverGitHubSourcePackage(context.Background(), "https://user@github.com/Owner/Repo", "v2", client); err == nil || calls != 0 {
		t.Fatal("credential-bearing repository accepted", err, calls)
	}
	if _, err := DiscoverGitHubSourcePackage(context.Background(), "https://github.com/Owner/Repo", "v3", client); err == nil || calls != 0 {
		t.Fatal("new package selector accepted", err, calls)
	}
}

func TestNativeRepositoryAndArtifactBodyBudgetsRemainBounded(t *testing.T) {
	if MaxIndexBytes != 2<<20 || MaxArtifactBytes != 64<<20 || MaxExpandedBytes != 128<<20 || MaxArchiveEntries != 4096 {
		t.Fatal("native artifact limits changed")
	}
	for _, limit := range []int64{MaxIndexBytes, MaxArtifactBytes} {
		body := &ownedPythonPackageBody{reader: strings.NewReader("never read")}
		client := &http.Client{Transport: repositoryReplayTransport(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: limit + 1, Body: body, Request: r}, nil
		})}
		if _, err := download(context.Background(), client, "https://owned.invalid/native-index-or-artifact", limit); err == nil || body.reads != 0 || !body.closed {
			t.Fatal("bounded native Content-Length guard changed", limit, err, body.reads, body.closed)
		}
	}
	body := &ownedPythonPackageBody{reader: strings.NewReader(ownedLargePythonPackage())}
	client := &http.Client{Transport: repositoryReplayTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: -1, Body: body, Request: r}, nil
	})}
	if _, err := download(context.Background(), client, "https://owned.invalid/native-index", MaxIndexBytes); err == nil || !body.closed {
		t.Fatal("bounded native streamed read accepted oversize", err, body.closed)
	}
}
