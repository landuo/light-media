package app

// Slots are assigned by the collector after filtering. They identify an
// immutable row during one POST, independently of its current display order.
// A whole-batch plugin may return distinct rows with identical public IDs.
func assignKeywordSlots(rows []Candidate, start int) {
	for i := range rows {
		rows[i].keywordSlot = start + i
	}
}

type keywordStreamFrame struct {
	WorkflowID string       `json:"workflow_id"`
	CachedAt   string       `json:"cached_at"`
	Sites      []SiteStatus `json:"sites"`
	Sequence   int          `json:"sequence"`
	Added      []*Candidate `json:"candidates_added,omitempty"`
	Order      []int        `json:"candidate_order,omitempty"`
}

// Only the connection's writer owns this encoder. Coalesced collector events
// never reach frame, so their rows are picked up by the next transmitted
// snapshot. A terminal snapshot goes through the same encoder as progress.
type keywordStreamEncoder struct {
	sequence int
	order    []int
}

func (e *keywordStreamEncoder) frame(out SearchOutput) keywordStreamFrame {
	e.sequence++
	frame := keywordStreamFrame{WorkflowID: out.WorkflowID, CachedAt: out.CachedAt, Sites: out.Sites, Sequence: e.sequence}
	known := len(e.order)
	if len(out.Candidates) > known {
		frame.Added = make([]*Candidate, len(out.Candidates)-known)
	}
	changed := len(out.Candidates) != known
	for i := range out.Candidates {
		candidate := &out.Candidates[i]
		if candidate.keywordSlot >= known {
			frame.Added[candidate.keywordSlot-known] = candidate
		}
		if !changed && candidate.keywordSlot != e.order[i] {
			changed = true
		}
	}
	if changed {
		frame.Order = make([]int, len(out.Candidates))
		for i := range out.Candidates {
			frame.Order[i] = out.Candidates[i].keywordSlot
		}
		e.order = frame.Order
	}
	return frame
}
