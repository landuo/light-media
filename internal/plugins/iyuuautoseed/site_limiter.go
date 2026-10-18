package iyuuautoseed

import (
	"fmt"
	"math"
	"sync"
	"time"
)

type RateState struct {
	Count         int     `json:"count"`
	LastVisitTime float64 `json:"last_visit_time"`
}

// SiteRateLimiter reproduces the installed compiled helper's consumed sliding
// last-visit algorithm. This is not a fixed calendar bucket: each allowed visit
// moves the interval origin. The mutex belongs to the new host concurrency
// model; the source outputs and state transitions are compared to actual .so IO.
type SiteRateLimiter struct {
	mu                       sync.Mutex
	interval, count, seconds int
	now                      func() float64
	location                 *time.Location
	state                    RateState
}

func NewSiteRateLimiter(interval, count, seconds int, now func() float64, location *time.Location) *SiteRateLimiter {
	if now == nil {
		now = func() float64 { return float64(time.Now().UnixNano()) / 1e9 }
	}
	if location == nil {
		location = time.Local
	}
	return &SiteRateLimiter{interval: interval, count: count, seconds: seconds, now: now, location: location}
}
func (l *SiteRateLimiter) Snapshot() RateState { l.mu.Lock(); defer l.mu.Unlock(); return l.state }
func (l *SiteRateLimiter) Check() (bool, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	last := l.state.LastVisitTime
	stamp := func() string {
		return time.Unix(int64(math.Floor(last)), 0).In(l.location).Format("2006-01-02 15:04:05")
	}
	if l.seconds != 0 && now-last < float64(l.seconds) {
		return true, fmt.Sprintf("触发流控规则，访问间隔不得小于 %d 秒，上次访问时间：%s", l.seconds, stamp())
	}
	if l.interval != 0 && l.count != 0 {
		if now-last > float64(l.interval) {
			l.state.Count = 0
		}
		if l.state.Count >= l.count {
			return true, fmt.Sprintf("触发流控规则，%d 秒内访问次数不得超过 %d 次，上次访问时间：%s", l.interval, l.count, stamp())
		}
		l.state.Count++
	}
	l.state.LastVisitTime = now
	return false, ""
}
