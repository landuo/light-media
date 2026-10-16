package scrapefileclean

// FallbackBatch is the installed watchfiles change-set translation. Go's host
// watcher supplies one ordered batch; this type neither starts watchers nor
// invents ordering for Python's original unordered set.
type Change struct {
	Kind string
	Path string
}
type ObserverEvent struct {
	Kind, Src, Dest string
	Directory       bool
}
type FallbackBatch struct {
	Known    map[string]Identity
	Machine  *Machine
	Dispatch func(ObserverEvent) error
}

func NewFallbackBatch(m *Machine, path string) *FallbackBatch {
	b := &FallbackBatch{Known: map[string]Identity{}, Machine: m}
	if rows, e := m.Ops.Scan(path); e == nil {
		for _, row := range rows {
			b.Known[row.Path] = row.Identity
		}
	}
	return b
}
func (b *FallbackBatch) Apply(changes []Change) error {
	added := []string{}
	for _, change := range changes {
		if change.Kind == "added" {
			added = append(added, change.Path)
		}
	}
	for _, path := range added {
		if id, e := b.Machine.Ops.Stat(path); e == nil {
			b.Known[path] = id
		} else {
			delete(b.Known, path)
		}
		if e := b.send(ObserverEvent{Kind: "created", Src: path, Directory: b.Machine.Ops.IsDir(path)}); e != nil {
			return e
		}
	}
	for _, change := range changes {
		if change.Kind != "deleted" {
			continue
		}
		path := change.Path
		old, known := b.Known[path]
		delete(b.Known, path)
		moved := false
		if known {
			for _, next := range added {
				identity, exists := b.Known[next]
				if next == path || !exists || !same(identity, old) {
					continue
				}
				if e := b.send(ObserverEvent{Kind: "moved", Src: path, Dest: next, Directory: b.Machine.Ops.IsDir(next)}); e != nil {
					return e
				}
				moved = true
				break
			}
		}
		if !moved {
			if e := b.send(ObserverEvent{Kind: "deleted", Src: path}); e != nil {
				return e
			}
		}
	}
	return nil
}

func (b *FallbackBatch) send(event ObserverEvent) error {
	if b.Dispatch != nil {
		return b.Dispatch(event)
	}
	switch event.Kind {
	case "created":
		return b.Machine.Created(event.Src, event.Directory)
	case "moved":
		return b.Machine.Moved(event.Src, event.Dest, event.Directory)
	default:
		return b.Machine.Deleted(event.Src, event.Directory)
	}
}
