package app

import (
	"bytes"
	"encoding/json"
	"sync"
)

// Only PluginFolders has source synchronous cache-before-SQL semantics here.
// Order and the host CAS revision remain database facts. operation is acquired
// before BeginTx by public folder writers/readers; state never performs SQL.
// This avoids a cache/SQLite-single-connection lock inversion.
type originalPluginFolderCache struct {
	operation sync.Mutex
	state     sync.Mutex
	loaded    bool
	folders   map[string]json.RawMessage
	keys      []string
	disk      json.RawMessage
	revision  int64
}

func cloneOriginalFolders(in map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(in))
	for k, v := range in {
		out[k] = append(json.RawMessage(nil), v...)
	}
	return out
}

// Observe committed native folder changes, including existing private Tx
// helpers, without mistaking an order-only revision for a folder replacement.
// The separate disk snapshot must not erase a failed write's published value.
func (p *PluginStore) withOriginalFolders(in PluginOrganization) PluginOrganization {
	c := &p.folders
	c.state.Lock()
	defer c.state.Unlock()
	disk := originalFolderJSON(in)
	if !c.loaded || in.Revision >= c.revision && !bytes.Equal(disk, c.disk) {
		c.loaded = true
		c.folders = cloneOriginalFolders(in.Folders)
		c.keys = append([]string(nil), in.folderKeys...)
		c.disk = append(json.RawMessage(nil), disk...)
	}
	if in.Revision > c.revision {
		c.revision = in.Revision
	}
	in.Folders = cloneOriginalFolders(c.folders)
	in.folderKeys = append([]string(nil), c.keys...)
	return in
}

func (p *PluginStore) publishOriginalFolders(in PluginOrganization, committed bool) {
	c := &p.folders
	c.state.Lock()
	defer c.state.Unlock()
	c.loaded = true
	c.folders = cloneOriginalFolders(in.Folders)
	c.keys = append([]string(nil), in.folderKeys...)
	if committed {
		c.disk = originalFolderJSON(in)
		c.revision = in.Revision
	}
}
