package remoteidentifiers

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	sdk "light-media/builtinplugins/internal/sdk"
)

// A large index can exceed one SDK callback's request/byte budget. Keep only
// this task's source cursor between callbacks. The initial private/public
// task waits for the final result, so stop and job success still see one run.
type refreshPass struct {
	mu       sync.Mutex
	id       uint64
	phase    uint64
	ctx      context.Context
	done     chan error
	fileURLs string
	filter   bool
	attempt  int
	collect  collection
}

type refreshContinuation struct {
	Pass  uint64 `json:"pass"`
	Phase uint64 `json:"phase"`
}

func (p *Plugin) task(ctx context.Context, caller sdk.HostCaller) error {
	settings := sdk.SystemSettings{Host: caller}
	// Source reads the setting before remote collection. The existing host
	// adapter merges the latest user prefix atomically when publishing.
	if _, err := settings.Get(ctx, "CustomIdentifiers"); err != nil {
		return err
	}
	fileURLs, filter, err := p.snapshot().Task()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	pass := &refreshPass{ctx: ctx, done: make(chan error, 1), fileURLs: fileURLs, filter: filter, collect: newCollection(fileURLs, filter)}
	p.mu.Lock()
	p.next++
	pass.id = p.next
	p.passes[pass.id] = pass
	p.mu.Unlock()
	defer func() {
		cancel()
		p.mu.Lock()
		delete(p.passes, pass.id)
		p.mu.Unlock()
	}()
	pass.mu.Lock()
	done, err := pass.advance(ctx, caller)
	pass.mu.Unlock()
	if done || err != nil {
		return err
	}
	select {
	case err := <-pass.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Plugin) continueRefresh(ctx context.Context, raw json.RawMessage, caller sdk.HostCaller) error {
	var request refreshContinuation
	if err := json.Unmarshal(raw, &request); err != nil {
		return err
	}
	p.mu.Lock()
	pass := p.passes[request.Pass]
	p.mu.Unlock()
	if pass == nil {
		return nil
	}
	pass.mu.Lock()
	defer pass.mu.Unlock()
	if request.Phase != pass.phase || pass.ctx.Err() != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(pass.ctx, cancel)
	defer func() { stop(); cancel() }()
	done, err := pass.advance(ctx, caller)
	if done || err != nil {
		pass.phase++
		pass.done <- err
	}
	return nil
}

func (p *refreshPass) advance(ctx context.Context, caller sdk.HostCaller) (done bool, err error) {
	logger := sdk.Logger{Host: caller}
	for steps := 0; ; steps++ {
		if err := ctx.Err(); err != nil {
			return true, err
		}
		if refreshPhaseFull(ctx, steps) {
			p.phase++
			return false, (sdk.Manager{Host: caller}).Submit(ctx, "refresh_continue", refreshContinuation{Pass: p.id, Phase: p.phase})
		}
		finished, err := p.collect.step(ctx, caller)
		if err == nil {
			if !finished {
				continue
			}
			_ = logger.Log(ctx, sdk.LogInfo, fmt.Sprintf("获取到远端识别词%d条", len(p.collect.words)-1))
			if _, err = (sdk.SystemSettings{Host: caller}).ReplaceRemoteIdentifiers(ctx, p.collect.words); err != nil {
				return true, err
			}
			_ = logger.Log(ctx, sdk.LogInfo, "远端识别词添加成功")
			return true, nil
		}
		if p.attempt == 2 {
			return true, err
		}
		seconds := 5 << p.attempt
		p.attempt++
		_ = logger.Log(ctx, sdk.LogWarning, fmt.Sprintf("获取远端识别词失败，%d 秒后重试 ...", seconds))
		timer := time.NewTimer(time.Duration(seconds) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return true, ctx.Err()
		case <-timer.C:
		}
		p.collect = newCollection(p.fileURLs, p.filter)
	}
}

func refreshPhaseFull(ctx context.Context, steps int) bool {
	if steps >= 24 {
		return true
	}
	budget, ok := sdk.CurrentHostCallBudget(ctx)
	if !ok {
		return false
	}
	// Reserve for final publication, its response, logs and one handoff.
	return budget.MaxHostCalls-budget.HostCallsCharged < 4 ||
		budget.MaxWorkerToHostWireBytes-budget.WorkerToHostWireBytesCharged < 2*budget.LogicalMessageBytes ||
		budget.MaxHostToWorkerWireBytes-budget.HostToWorkerWireBytesCharged < 2*budget.LogicalMessageBytes ||
		budget.CumulativeChunksPerDirection-budget.WorkerToHostChunksCharged < budget.ChunksPerLogicalMessage+1 ||
		budget.CumulativeChunksPerDirection-budget.HostToWorkerChunksCharged < budget.ChunksPerLogicalMessage+1
}
