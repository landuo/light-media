package app

import (
	"context"
	bf "light-media/internal/plugins/brushflow"
	"sync"
)

// Actual MoviePilot core/config.py CONF: normal scheduler=50, threadpool=50;
// BIG_MEMORY_MODE (default false) selects 100/100. Brush get_service uses the
// scheduler pool; init/API cleanup/run/check submit to ThreadHelper's other pool.
// Pending work stays in SQL instead of one goroutine per queued source function.
const brushFlowNormalPoolSize = 50
const brushFlowLargePoolSize = 100

type brushFlowSourcePools struct{ tasks, services chan struct{} }

func newBrushFlowSourcePools(large bool) *brushFlowSourcePools {
	n := brushFlowNormalPoolSize
	if large {
		n = brushFlowLargePoolSize
	}
	return &brushFlowSourcePools{make(chan struct{}, n), make(chan struct{}, n)}
}
func (a *App) brushFlowPools() *brushFlowSourcePools {
	r := &a.pluginBrushFlow
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pools == nil {
		r.pools = newBrushFlowSourcePools(false)
	}
	return r.pools
}

type brushFlowClaimBudgetKey struct{}
type brushFlowClaimBudget struct {
	pools   *brushFlowSourcePools
	release func()
}

func brushFlowClaimBlocked(ctx context.Context) (bool, bool) {
	v, _ := ctx.Value(brushFlowClaimBudgetKey{}).(*brushFlowClaimBudget)
	if v == nil || v.pools == nil {
		return false, false
	}
	return len(v.pools.tasks) == cap(v.pools.tasks), len(v.pools.services) == cap(v.pools.services)
}
func brushFlowReserveClaim(ctx context.Context, plugin string, run PluginRun) bool {
	v, _ := ctx.Value(brushFlowClaimBudgetKey{}).(*brushFlowClaimBudget)
	if v == nil || v.pools == nil || plugin != bf.PluginID || run.Version != bf.Version {
		return true
	}
	var pool chan struct{}
	switch run.Action {
	case "task":
		pool = v.pools.tasks
	case "service":
		pool = v.pools.services
	default:
		return true
	}
	select {
	case pool <- struct{}{}:
		var once sync.Once
		v.release = func() { once.Do(func() { <-pool }) }
		return true
	default:
		return false
	}
}
