package app

import "sync/atomic"

type organizeProgressKey struct{}
type organizeCopyCallbackKey struct{}
type organizeCopyProgress struct {
	Source string
	Bytes  *atomic.Int64
}

func (a *App) organizeFileProgress(file PlanFile) (float64, bool) {
	if file.Status == "imported" || file.Status == "already_imported" {
		return 100, true
	}
	value, ok := a.organizeProgress.Load(file.ID)
	if !ok || file.Size <= 0 {
		return 0, false
	}
	return min(99, float64(value.(*atomic.Int64).Load())*100/float64(file.Size)), true
}
