package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type searchProgressKey struct{}

func emitSearchProgress(ctx context.Context, out SearchOutput) {
	if callback, ok := ctx.Value(searchProgressKey{}).(func(SearchOutput)); ok {
		callback(out)
	}
}
func (a *App) streamSearch(w http.ResponseWriter, r *http.Request) {
	var in SearchRequest
	if e := decodeBody(w, r, &in); e != nil {
		a.writeError(w, r, e)
		return
	}
	f, ok := w.(http.Flusher)
	if !ok {
		a.writeError(w, r, bad("STREAM_UNAVAILABLE", "当前连接不支持渐进响应"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	send := func(event string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		f.Flush()
	}
	ctx := context.WithValue(r.Context(), searchProgressKey{}, func(out SearchOutput) { send("progress", out) })
	result, e := a.search(ctx, in, false)
	if e != nil {
		var message string
		code := "SEARCH_FAILED"
		if x, ok := e.(*APIError); ok {
			message = x.Message
			code = x.Code
		} else {
			message = a.clean(e.Error())
		}
		send("error", map[string]any{"code": code, "message": message, "request_id": w.Header().Get("X-Request-ID")})
		return
	}
	send("complete", result)
}
