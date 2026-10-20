package download

import (
	"context"
	"sync/atomic"

	"light-media/internal/acquisitionbatch"
)

// sourceAddReceipt is minted only by a successful Client.Add. Copying an
// AddResult shares the same one-use receipt; JSON cannot recreate it. The host
// owns durable recovery across processes and must persist every returned effect
// before considering the enclosing operation complete.
type sourceAddReceipt struct {
	client    *Client
	hash      string
	duplicate bool
	paused    bool
	consumed  atomic.Bool
}

func (c *Client) sourceAddResult(result AddResult, paused bool, err error) (AddResult, error) {
	if err == nil {
		result.sourceReceipt = &sourceAddReceipt{client: c, hash: result.Hash, duplicate: result.Duplicate, paused: paused}
	}
	return result, err
}

// SourcePostAddEffect keeps the native acknowledgment separate from the
// original Python wrapper's return. The qB force-start wrapper returns None
// even on success; ordinary start/setters return False after exceptions.
// OriginalReturn is empty for reads, whose business result is in Selection.
// Unknown means an issued mutation lacks an acknowledgment or fixed rejection.
// Failure never contains a remote body, URL, header or credential.
type SourcePostAddEffect struct {
	Operation      string
	Acknowledged   bool
	Failure        error
	NotStarted     bool
	Rejected       bool
	Unknown        bool
	OriginalReturn string
}

type SourcePostAddResult struct {
	Hash      string
	Duplicate bool
	Selection acquisitionbatch.FileSelectionResult
	Effects   []SourcePostAddEffect
}

func sourcePostAddEffect(operation string, acknowledged bool, err error) SourcePostAddEffect {
	effect := SourcePostAddEffect{Operation: operation, Acknowledged: acknowledged, Failure: err}
	if operation == "files" {
		return effect // a failed read cannot create an unknown daemon mutation.
	}
	effect.OriginalReturn = "false"
	if acknowledged {
		effect.OriginalReturn = "true"
	}
	if operation == "force_start" {
		effect.OriginalReturn = "none"
	}
	effect.NotStarted = MutationNotStarted(err)
	effect.Rejected = MutationRejected(err)
	effect.Unknown = err != nil && !effect.NotStarted && !effect.Rejected
	return effect
}

// SourcePostAdd performs the source modules' post-Add branch exactly once for
// a real Add on this Client. A duplicate never changes file priorities or start
// state. Newly added qB tasks use Config.ForceResume both with and without an
// episode selection. Transmission ignores that setting and starts only after
// selecting files. Setter/start failures remain effects, matching the source's
// accepted hash return; local guard/parser failures return a separate error.
// There is no retry of an uncertain mutation, including through copied results.
func (c *Client) SourcePostAdd(ctx context.Context, added AddResult, episodes []int, parse func(string) ([]int, error)) (SourcePostAddResult, error) {
	out := SourcePostAddResult{Hash: added.Hash, Duplicate: added.Duplicate, Selection: acquisitionbatch.FileSelectionResult{SelectedEpisodes: []int{}}, Effects: []SourcePostAddEffect{}}
	receipt := added.sourceReceipt
	if receipt == nil || receipt.client != c || receipt.hash != added.Hash || receipt.duplicate != added.Duplicate {
		return out, beforeMutation(failure("source_post_add", "invalid_add_receipt"))
	}
	if !added.Duplicate && receipt.paused != (len(episodes) > 0) {
		return out, beforeMutation(failure("source_post_add", "paused_selection_mismatch"))
	}
	if !added.Duplicate && len(episodes) > 0 && parse == nil {
		return out, beforeMutation(failure("source_post_add", "missing_episode_parser"))
	}
	if !receipt.consumed.CompareAndSwap(false, true) {
		return out, beforeMutation(failure("source_post_add", "add_receipt_consumed"))
	}
	if added.Duplicate {
		return out, nil
	}
	if len(episodes) > 0 {
		selected, err := c.SourceSelectEpisodeDownload(ctx, added.Hash, episodes, c.config.ForceResume, parse)
		out.Selection = selected.Selection
		for _, effect := range selected.Effects {
			operation := effect.Operation
			if operation == "start" && c.config.Kind == "qbittorrent" && c.config.ForceResume {
				operation = "force_start"
			}
			out.Effects = append(out.Effects, sourcePostAddEffect(operation, effect.Acknowledged, effect.Failure))
		}
		return out, err
	}
	if c.config.Kind == "qbittorrent" && c.config.ForceResume {
		ok, err := c.SourceStartEpisodeDownload(ctx, added.Hash, true)
		out.Effects = append(out.Effects, sourcePostAddEffect("force_start", ok, err))
	}
	return out, nil
}
