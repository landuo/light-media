package app

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func syntheticLoggingApp(count int) *App {
	a := &App{config: Settings{LogLevel: "INFO"}}
	for i := 0; i < count; i++ {
		s := SiteConfig{}
		s.ID = fmt.Sprint(i)
		s.Cookie = strings.Repeat("fixture", 80)
		a.config.Sites = append(a.config.Sites, s)
	}
	return a
}
func TestDisabledDebugLogAvoidsConfigurationCopy(t *testing.T) {
	l := passiveLogs(syntheticLoggingApp(100))
	entry := map[string]any{"level": "DEBUG", "module": "http.status"}
	allocations := testing.AllocsPerRun(100, func() { l.enqueue(entry) })
	if allocations > 2 {
		t.Fatalf("settings copy allocations: %f", allocations)
	}
	if len(l.queue) != 0 {
		t.Fatal("disabled DEBUG queued")
	}
	l.mu.Lock()
	l.overrides["test"] = debugOverride{Scope: "module", ID: "http", ExpiresAt: time.Now().Add(time.Minute)}
	l.mu.Unlock()
	l.enqueue(entry)
	if len(l.queue) != 1 {
		t.Fatal("override ignored")
	}
	l.mu.Lock()
	l.overrides["test"] = debugOverride{Scope: "module", ID: "http", ExpiresAt: time.Now().Add(-time.Minute)}
	l.mu.Unlock()
	l.enqueue(entry)
	if len(l.queue) != 1 {
		t.Fatal("expired override enabled")
	}
}
func BenchmarkDisabledDebugLog(b *testing.B) {
	for _, count := range []int{20, 100} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			l := passiveLogs(syntheticLoggingApp(count))
			entry := map[string]any{"level": "DEBUG", "module": "http.status"}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				l.enqueue(entry)
			}
		})
	}
}
