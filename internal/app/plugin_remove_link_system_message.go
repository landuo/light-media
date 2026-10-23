package app

import (
	"context"
	"time"
)

// publishInitSystemMessage is called only for a newly inserted diagnostic after
// its SQL transaction is known committed, including this invocation's exact
// lost-ACK read-back. Existing receipts and cold recovery never call it.
//
// The configure Guard and effect lease remain held. Reuse MessageHelper's shared
// ephemeral FIFO: no separate instance queue, external sender or durable replay.
// A process exit between SQL commit and this put may lose the live popup; the
// sealed local diagnostic remains. This is not an exactly-once durable outbox.
func (o *pluginRemoveLinkOwner) publishInitSystemMessage(ctx context.Context, notice Notification) {
	m := &o.a.systemMessages
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx.Err() != nil {
		return
	}
	// Original RemoveLink 2.17 omits role in systemmessage.put, whose default
	// is plugin, irrespective of the legacy local record's MType="系统".
	m.putLocked("plugin", &notice.Title, notice.Message, nil, time.Now())
}
