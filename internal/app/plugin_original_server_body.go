package app

import (
	"bytes"
	"context"
	"io"
)

// readOriginalPluginCenterBody is private to the source statistics/rating
// response and rating-admission paths. Those original consumers read the whole
// body; this does not alter generic HTTP, executable-artifact or IPC budgets.
// The caller retains its existing request deadline and owns closing the body.
// A chunk size bounds each read, not the total memory required by the source.
func readOriginalPluginCenterBody(ctx context.Context, body io.Reader) ([]byte, error) {
	var result bytes.Buffer
	chunk := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, readErr := body.Read(chunk)
		if n > 0 {
			_, _ = result.Write(chunk[:n])
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if readErr == io.EOF {
			return result.Bytes(), nil
		}
		if readErr != nil {
			return nil, readErr
		}
	}
}
