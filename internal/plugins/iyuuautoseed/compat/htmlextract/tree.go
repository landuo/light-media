package htmlextract

// Consumed tree construction from htmlAutoClose, htmlCheckImplied,
// htmlAutoCloseOnClose and htmlParseStartTag in the pinned libxml2 source.
type treeState struct {
	stack                       []string
	inserted, ignoredStructural int
	stopped                     bool
}

func (t *treeState) push(name string) {
	t.stack = append(t.stack, name)
	if name == "head" && t.inserted < 3 {
		t.inserted = 3
	}
	if name == "body" && t.inserted < 10 {
		t.inserted = 10
	}
	if len(t.stack) > 256 {
		t.stopped = true
	}
}
func (t *treeState) auto(name string) {
	for len(t.stack) > 0 && autoClose[t.stack[len(t.stack)-1]+"\x00"+name] {
		t.stack = t.stack[:len(t.stack)-1]
	}
}
func (t *treeState) implied(name string) {
	if name == "html" {
		return
	}
	if len(t.stack) == 0 {
		t.push("html")
	}
	if name == "body" || name == "head" {
		return
	}
	head := name == "script" || name == "style" || name == "meta" || name == "link" || name == "title" || name == "base"
	if len(t.stack) <= 1 && head {
		if t.inserted >= 3 {
			return
		}
		t.push("head")
	} else if name != "noframes" && name != "frame" && name != "frameset" {
		if t.inserted >= 10 {
			return
		}
		for _, tag := range t.stack {
			if tag == "body" || tag == "head" {
				return
			}
		}
		t.push("body")
	}
}
func (t *treeState) text() {
	if len(t.stack) > 0 && t.stack[len(t.stack)-1] == "head" {
		t.auto("p")
	}
	t.implied("p")
}
func (t *treeState) start(name string) bool {
	t.auto(name)
	t.implied(name)
	discard := name == "html" && len(t.stack) > 0 || name == "head" && len(t.stack) != 1
	if name == "body" {
		for _, tag := range t.stack {
			if tag == "body" {
				discard = true
			}
		}
	}
	if discard {
		t.ignoredStructural++
		return false
	}
	t.push(name)
	return !t.stopped
}
func endPriority(name string) int {
	switch name {
	case "div":
		return 150
	case "td", "th":
		return 160
	case "tr":
		return 170
	case "thead", "tbody", "tfoot":
		return 180
	case "table":
		return 190
	case "head", "body":
		return 200
	case "html":
		return 220
	}
	return 100
}
func (t *treeState) end(name string) {
	if t.ignoredStructural > 0 && (name == "html" || name == "head" || name == "body") {
		t.ignoredStructural--
		return
	}
	priority := endPriority(name)
	for i := len(t.stack) - 1; i >= 0; i-- {
		if t.stack[i] == name {
			t.stack = t.stack[:i]
			return
		}
		if endPriority(t.stack[i]) > priority {
			return
		}
	}
}
