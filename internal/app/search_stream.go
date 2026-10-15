package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type searchProgressKey struct{}

func emitSearchProgress(ctx context.Context, out SearchOutput) {
	if callback, ok := ctx.Value(searchProgressKey{}).(func(SearchOutput)); ok {
		// Snapshot the growing slices so consumers can retain earlier frames.
		out.Candidates = append([]Candidate{}, out.Candidates...)
		out.Sites = append([]SiteStatus{}, out.Sites...)
		callback(out)
	}
}
func (a *App) streamSearch(w http.ResponseWriter, r *http.Request) {
	var in SearchRequest
	if e := decodeBody(w, r, &in); e != nil {
		a.writeError(w, r, e)
		return
	}
	_, ok := w.(http.Flusher)
	if !ok {
		a.writeError(w, r, bad("STREAM_UNAVAILABLE", "当前连接不支持渐进响应"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	controller := http.NewResponseController(w)
	send := func(event string, data any) {
		b, _ := json.Marshal(data)
		_ = controller.SetWriteDeadline(time.Now().Add(15 * time.Second))
		defer controller.SetWriteDeadline(time.Time{})
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b); err != nil {
			cancel()
			return
		}
		if err := controller.Flush(); err != nil {
			cancel()
		}
	}
	ctx = context.WithValue(ctx, searchProgressKey{}, func(out SearchOutput) { send("progress", out) })
	var result SearchOutput
	var e error
	if strings.HasSuffix(r.URL.Path, "/search/resources") {
		result, e = a.searchResources(ctx, in)
	} else {
		result, e = a.search(ctx, in, false)
	}
	if e != nil {
		var message string
		code := "SEARCH_FAILED"
		if x, ok := e.(*APIError); ok {
			message = x.Message
			code = x.Code
		} else {
			message = a.clean(e.Error())
		}
		if ctx.Err() != nil {
			code, message = "SEARCH_CANCELLED", "搜索已停止，已完成的结果保留"
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				code, message = "SEARCH_TIMEOUT", "搜索超时，已完成的结果保留"
			}
		}
		send("error", map[string]any{"code": code, "message": message, "request_id": w.Header().Get("X-Request-ID")})
		return
	}
	send("complete", result)
}
