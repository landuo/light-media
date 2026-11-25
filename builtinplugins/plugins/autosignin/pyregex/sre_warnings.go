package pyregex

import (
	"container/list"
	"fmt"
	"io"
	"os"
	"sync"

	"light-media/builtinplugins/plugins/autosignin/regexengine"
)

// Origins are the two captured Python regex callsites, not a general Python
// stack emulator. Their warnings use the original source line and default
// FutureWarning registry. Each isolated Go worker owns its runtime caches.
type SourceOrigin struct {
	Filename, Module, LineText string
	Line                       int
}

var RetryOrigin = SourceOrigin{"/app/app/plugins/autosignin/__init__.py", "app.plugins.autosignin", "match = re.search(self._retry_keyword, s[1])", 1701}
var SignInResultOrigin = SourceOrigin{"/app/app/plugins/autosignin/sites/__init__.py", "app.plugins.autosignin.sites", "if re.search(str(regex), target):", 111}

type compileKey struct {
	pattern string
	flags   int
}
type compileEntry struct {
	key compileKey
	re  *regexp2.Regexp
}
type warningKey struct {
	module, category, message string
	line                      int
}
type sourceCompileRuntime struct {
	mu                  sync.Mutex
	lru, fifo           *list.List
	lruIndex, fifoIndex map[compileKey]*list.Element
	registry            map[warningKey]bool
	sink                io.Writer
}

func newSourceCompileRuntime(sink io.Writer) *sourceCompileRuntime {
	return &sourceCompileRuntime{lru: list.New(), fifo: list.New(), lruIndex: map[compileKey]*list.Element{}, fifoIndex: map[compileKey]*list.Element{}, registry: map[warningKey]bool{}, sink: sink}
}

var sourceCompiles = newSourceCompileRuntime(nil)

func (r *sourceCompileRuntime) emit(origin SourceOrigin, w Warning) {
	// Default Python filters ignore DeprecationWarning outside __main__. Only
	// FutureWarning is raised by the supported string-pattern compiler here.
	if w.Category != "FutureWarning" || origin.Filename == "" {
		return
	}
	key := warningKey{origin.Module, w.Category, w.Message, origin.Line}
	if r.registry[key] {
		return
	}
	r.registry[key] = true
	sink := r.sink
	if sink == nil {
		sink = os.Stderr
	}
	// Python's default stderr warning printer ignores OSError from its sink.
	_, _ = fmt.Fprintf(sink, "%s:%d: %s: %s\n  %s\n", origin.Filename, origin.Line, w.Category, w.Message, origin.LineText)
}
func (r *sourceCompileRuntime) compile(pattern string, flags int, origin SourceOrigin) (*regexp2.Regexp, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := compileKey{pattern, flags}
	if e := r.fifoIndex[key]; e != nil {
		return e.Value.(compileEntry).re, nil
	}
	var compiled *regexp2.Regexp
	if e := r.lruIndex[key]; e != nil {
		compiled = e.Value.(compileEntry).re
		r.lru.Remove(e)
		delete(r.lruIndex, key)
	} else {
		p, err := parseSourceWithWarnings(pattern, flags, func(w Warning) { r.emit(origin, w) })
		if err != nil {
			return nil, err
		}
		if err = CheckCompile(p); err != nil {
			return nil, err
		}
		compiled, err = regexp2.CompileSourceTree(pattern, lowerTree(p))
		if err != nil {
			return nil, err
		}
	}
	if len(r.lruIndex) >= 512 {
		e := r.lru.Front()
		delete(r.lruIndex, e.Value.(compileEntry).key)
		r.lru.Remove(e)
	}
	r.lruIndex[key] = r.lru.PushBack(compileEntry{key, compiled})
	if len(r.fifoIndex) >= 256 {
		e := r.fifo.Front()
		delete(r.fifoIndex, e.Value.(compileEntry).key)
		r.fifo.Remove(e)
	}
	r.fifoIndex[key] = r.fifo.PushBack(compileEntry{key, compiled})
	return compiled, nil
}
