package artifacts

import (
	"context"
	"errors"
	"io"
	"net/http"
)

// downloadPythonSourcePackage reads only a display package selected by
// DiscoverGitHubSourcePackage. MoviePilot reads the complete response text;
// executable Go indexes and archives must keep using bounded download instead.
// Memory grows with the real body, not its advertised Content-Length. Existing
// request context, client timeout, redirect policy and body ownership still apply.
func downloadPythonSourcePackage(ctx context.Context, client *http.Client, raw string) ([]byte, error) {
	if _, err := httpURL(raw); err != nil {
		return nil, err
	}
	clone := *client
	previous := clone.CheckRedirect
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if _, err := httpURL(req.URL.String()); err != nil {
			return err
		}
		if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
			return errors.New("HTTPS downgrade")
		}
		if previous != nil {
			return previous(req, via)
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	resp, err := clone.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("artifact request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &downloadStatusError{Code: resp.StatusCode}
	}
	data, err := io.ReadAll(pythonSourceContextReader{ctx: ctx, reader: resp.Body})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, errors.New("artifact response read failed")
	}
	return data, nil
}

type pythonSourceContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r pythonSourceContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
