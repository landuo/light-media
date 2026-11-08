package app

import (
	"container/heap"
	"context"
	"encoding/json"
	"sync"

	"github.com/google/uuid"
	"light-media/internal/plugins"
)

type originalQueuedBroadcast struct {
	ctx      context.Context
	event    plugins.MutableEvent
	sequence uint64
	// A native producer may already have performed its concrete core consumer.
	// Keep this queue fact outside callback contexts and SDK-visible event data.
	coreConsumed bool
	refresh      *originalFNOSRefreshEvent
}

type originalBroadcastHeap []originalQueuedBroadcast

func (h originalBroadcastHeap) Len() int { return len(h) }
func (h originalBroadcastHeap) Less(i, j int) bool {
	if *h[i].event.Priority == *h[j].event.Priority {
		return h[i].sequence < h[j].sequence
	}
	return *h[i].event.Priority < *h[j].event.Priority
}
func (h originalBroadcastHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *originalBroadcastHeap) Push(value any) {
	*h = append(*h, value.(originalQueuedBroadcast))
}
func (h *originalBroadcastHeap) Pop() any {
	last := len(*h) - 1
	value := (*h)[last]
	(*h)[last] = originalQueuedBroadcast{}
	*h = (*h)[:last]
	return value
}

// This queue is only EventManager's in-memory priority ordering. Each entry
// owns one background admission; there is no durable job, receipt or replay.
// Priorities order dequeue/submission, never concurrent callback completion.
type originalBroadcastRuntime struct {
	mu       sync.Mutex
	pending  originalBroadcastHeap
	running  bool
	sequence uint64
	// These registration maps use pluginOriginalEventsMu, never the queue mutex.
	handlers                   map[string][]originalBroadcastHandler
	disabled                   map[originalEventHandlerIdentity]bool
	broadcastOrder, chainOrder []string
}

func originalNewEvent(kind string, data json.RawMessage, priority *int) (plugins.MutableEvent, error) {
	normalized, err := originalNewEventData(data)
	if err != nil {
		return plugins.MutableEvent{}, err
	}
	order := 10
	if priority != nil {
		order = *priority
	}
	return plugins.MutableEvent{Type: kind, Data: normalized, EventID: uuid.NewString(), Priority: &order}, nil
}

func (a *App) enqueueOriginalBroadcast(ctx context.Context, event plugins.MutableEvent) bool {
	return a.enqueueOriginalBroadcastCoreState(ctx, event, false)
}

func (a *App) enqueueOriginalBroadcastCoreState(ctx context.Context, event plugins.MutableEvent, consumed bool) bool {
	return a.enqueueOriginalBroadcastWithRefresh(ctx, event, consumed, nil)
}

func (a *App) enqueueOriginalBroadcastWithRefresh(ctx context.Context, event plugins.MutableEvent, consumed bool, refresh *originalFNOSRefreshEvent) bool {
	if !a.admitOriginalBackground() {
		return false
	}
	state := &a.pluginOriginalBroadcast
	state.mu.Lock()
	state.sequence++
	heap.Push(&state.pending, originalQueuedBroadcast{ctx: context.WithoutCancel(ctx), event: event, sequence: state.sequence, coreConsumed: consumed, refresh: refresh})
	start := !state.running
	state.running = true
	state.mu.Unlock()
	if start {
		go a.drainOriginalBroadcasts()
	}
	return true
}

func (a *App) drainOriginalBroadcasts() {
	state := &a.pluginOriginalBroadcast
	for {
		state.mu.Lock()
		if len(state.pending) == 0 {
			state.running = false
			state.mu.Unlock()
			return
		}
		next := heap.Pop(&state.pending).(originalQueuedBroadcast)
		state.mu.Unlock()
		a.dispatchOriginalBroadcast(next.ctx, next.event, next.coreConsumed, next.refresh)
		// Release the last entry only after publishing the idle state. A caller
		// waiting for all admitted work must not race an untracked drain loop.
		state.mu.Lock()
		empty := len(state.pending) == 0
		if empty {
			state.running = false
		}
		state.mu.Unlock()
		a.wg.Done()
		if empty {
			return
		}
	}
}
