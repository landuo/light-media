package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
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
		// Bound a stalled socket independently of the collection deadline.
		// A frame already on the wire must be allowed to finish.
		_ = controller.SetWriteDeadline(time.Now().Add(interactiveSearchBudget))
		defer controller.SetWriteDeadline(time.Time{})
		if _, err := io.WriteString(w, "event: "+event+"\ndata: "); err != nil {
			cancel()
			return
		}
		if err := json.NewEncoder(w).Encode(data); err != nil {
			cancel()
			return
		}
		if _, err := io.WriteString(w, "\n"); err != nil {
			cancel()
			return
		}
		if err := controller.Flush(); err != nil {
			cancel()
		}
	}
	keyword := strings.HasSuffix(r.URL.Path, "/search/resources")
	compact := keyword && r.Header.Get("X-Light-Media-Search-Stream") == "incremental"
	var encoder keywordStreamEncoder
	sendResult := func(event string, out SearchOutput) {
		if ctx.Err() == context.Canceled {
			return // a disconnect or previous write failed
		}
		if compact {
			send(event, encoder.frame(out))
		} else {
			send(event, out)
		}
	}
	progress := func(out SearchOutput) { sendResult("progress", out) }
	var result SearchOutput
	var e error
	if keyword {
		deadline, ok := ctx.Deadline()
		if !ok {
			deadline = time.Now().Add(interactiveSearchBudget)
		}
		result, e = streamKeywordResults(ctx, deadline, progress, func(searchCtx context.Context) (SearchOutput, error) {
			return a.searchResources(searchCtx, in)
		})
	} else {
		result, e = a.search(context.WithValue(ctx, searchProgressKey{}, progress), in, false)
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
	sendResult("complete", result)
}

// Leave time inside the caller's 15 seconds to encode and flush the final
// cumulative result on a NAS. A slow progress write must never stop collection.
const keywordDeliveryReserve = 500 * time.Millisecond

func streamKeywordResults(ctx context.Context, deadline time.Time, progress func(SearchOutput), search func(context.Context) (SearchOutput, error)) (SearchOutput, error) {
	var latest atomic.Pointer[SearchOutput]
	ready := make(chan struct{}, 1)
	publish := func(out SearchOutput) {
		// The collector publishes immutable snapshots. Superseded snapshots carry
		// no extra results, so retain only the newest while the client is busy.
		latest.Store(&out)
		select {
		case ready <- struct{}{}:
		default:
		}
	}

	searchCtx, cancel := context.WithDeadline(context.WithValue(ctx, searchProgressKey{}, publish), deadline.Add(-keywordDeliveryReserve))
	defer cancel()
	type outcome struct {
		output SearchOutput
		err    error
	}
	finished := make(chan outcome, 1)
	go func() {
		output, err := search(searchCtx)
		finished <- outcome{output, err}
	}()

	// Normally the collector returns as soon as its deadline expires. Keep a
	// bounded handoff even if a plugin or store call is still winding down.
	cutoff := time.NewTimer(time.Until(deadline.Add(-keywordDeliveryReserve / 2)))
	defer cutoff.Stop()
	// Send the first candidates immediately. While a busy cluster of sites is
	// delivering pages, coalesce cumulative frames over a short interval so a
	// slow browser spends its time rendering new results. The terminal frame
	// never waits for this timer.
	pace := time.NewTimer(0)
	pace.Stop()
	defer pace.Stop()
	var resume <-chan time.Time
	var notifications <-chan struct{} = ready
	var displayed *SearchOutput
collect:
	for {
		select {
		case result := <-finished:
			return result.output, result.err
		default:
		}
		select {
		case <-ctx.Done():
			if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return SearchOutput{}, ctx.Err()
			}
			break collect
		case result := <-finished:
			return result.output, result.err
		case <-resume:
			resume, notifications = nil, ready
		case <-notifications:
			if frame := latest.Load(); frame != displayed {
				displayed = frame
				progress(*frame)
				if len(frame.Candidates) > 0 {
					pace.Reset(100 * time.Millisecond)
					resume, notifications = pace.C, nil
				}
			}
		case <-cutoff.C:
			break collect
		}
	}
	select {
	case result := <-finished:
		return result.output, result.err
	default:
		return keywordCutoffResult(latest.Load())
	}
}

func keywordCutoffResult(frame *SearchOutput) (SearchOutput, error) {
	if frame == nil {
		return SearchOutput{}, bad("SEARCH_TIMEOUT", "搜索准备超时，请重试")
	}
	if frame.keywordFilterPending {
		return SearchOutput{}, bad("SEARCH_FILTER_TIMEOUT", "已收到站点结果，搜索筛选未在时限内完成")
	}
	out := *frame
	out.Sites = append([]SiteStatus(nil), frame.Sites...)
	for i, site := range out.Sites {
		if site.Status != "searching" && site.Status != "queued" {
			continue
		}
		out.Sites[i].Status, out.Sites[i].Message = "timeout", "搜索超时，已完成的结果保留"
		if site.Count > 0 {
			out.Sites[i].Status = "partial"
		}
	}
	return out, nil
}
