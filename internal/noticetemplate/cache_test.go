package noticetemplate

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestContextCacheTTLAndLRU(t *testing.T) {
	now := time.Unix(0, 0)
	c := NewContextCache(func() time.Time { return now })
	for i := 0; i < 100; i++ {
		c.Set(fmt.Sprint(i), Object{"i": i})
	}
	if c.Get("0") == nil {
		t.Fatal("initial entry absent")
	}
	c.Set("100", Object{})
	if c.Get("0") == nil || c.Get("1") != nil || c.Get("100") == nil {
		t.Fatal("LRU capacity or touch differs")
	}
	now = now.Add(599 * time.Second)
	value := c.Get("0")
	if value == nil {
		t.Fatal("entry expired early")
	}
	value["reference"] = true
	c.Close()
	if c.Get("0")["reference"] != true {
		t.Fatal("memory backend must retain source reference and close must be a no-op")
	}
	c.Set("100", Object{"updated": true})
	now = now.Add(time.Second)
	if c.Get("0") != nil || c.Get("100") == nil {
		t.Fatal("get must not refresh TTL; overwrite must reset expiry")
	}
	now = now.Add(599 * time.Second)
	if c.Get("100") != nil {
		t.Fatal("expiry boundary must be exclusive")
	}
}
func TestHelperConcurrentIndependentContexts(t *testing.T) {
	h := NewHelper(nil)
	var group sync.WaitGroup
	for i := 0; i < 32; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			title := fmt.Sprint(i)
			out, err := h.Render(t.Context(), `{'title':'{{ title }}','text':'{{ text }}'}`, "literal", ContextInput{Extra: Object{"title": title, "text": "owned"}})
			if err != nil {
				t.Error(err)
				return
			}
			cached, err := h.GetCacheContext(out)
			if err != nil || cached["title"] != title {
				t.Error("context mixed across concurrent renders", err)
			}
		}(i)
	}
	group.Wait()
}
