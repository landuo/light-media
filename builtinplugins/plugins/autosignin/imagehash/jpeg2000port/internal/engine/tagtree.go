package engine

// TagTree is a helper for JPEG 2000 tag-tree coded values.
// It supports both a simple flat-mode (for tests) and a hierarchical
// progressive decoding used by true packet header parsing.

// Internal level representation for a hierarchical tag tree.
type tagTreeLevel struct {
	w, h int
	// current minimum values known for nodes at this level (per J2K "m" state)
	m []int
	// whether node value is already known to be > current threshold
	known []bool
}

// TagTree holds a hierarchy of levels from leaves (level 0) up to a single root.
type TagTree struct {
	w, h int
	// canonical leaf values (used for flat mode and as targets in tests)
	val []int
	// hierarchical levels for progressive decoding (root is last)
	levels []tagTreeLevel
}

// NewTagTree constructs a TagTree with given dimensions; all values start at 0.
func NewTagTree(w, h int) *TagTree {
	t := &TagTree{w: w, h: h, val: make([]int, w*h)}
	// Build levels up to 1x1
	lw, lh := w, h
	for {
		n := lw * lh
		lvl := tagTreeLevel{
			w: lw, h: lh,
			m:     make([]int, n),
			known: make([]bool, n),
		}
		t.levels = append(t.levels, lvl)
		if lw == 1 && lh == 1 {
			break
		}
		if lw > 1 {
			lw = (lw + 1) / 2
		}
		if lh > 1 {
			lh = (lh + 1) / 2
		}
	}
	return t
}

// ResetKnown clears progressive decode state (m and known) across all levels.
func (t *TagTree) ResetKnown() {
	for i := range t.levels {
		for j := range t.levels[i].m {
			t.levels[i].m[j] = 0
		}
		for j := range t.levels[i].known {
			t.levels[i].known[j] = false
		}
	}
}

// Set sets the leaf value at (x,y) for flat/test usage.
func (t *TagTree) Set(x, y, v int) {
	if x < 0 || y < 0 || x >= t.w || y >= t.h {
		return
	}
	t.val[y*t.w+x] = v
}

// Get returns the leaf value at (x,y) (flat/test usage).
func (t *TagTree) Get(x, y int) int {
	if x < 0 || y < 0 || x >= t.w || y >= t.h {
		return 0
	}
	return t.val[y*t.w+x]
}

// DecodeFlat reads values for all leaves directly from a BitReader using a
// fixed number of bits per value. This is not the full tag‑tree decoding but
// provides a convenient way to exercise header propagation in tests.
func (t *TagTree) DecodeFlat(br *BitReader, bitsPerValue int) bool {
	if bitsPerValue <= 0 {
		return true
	}
	for y := 0; y < t.h; y++ {
		for x := 0; x < t.w; x++ {
			v, ok := br.ReadBits(bitsPerValue)
			if !ok {
				return false
			}
			t.Set(x, y, int(v))
		}
	}
	return true
}

// DecodeFlatBits convenience: decode from a raw byte slice using BitReader.
func (t *TagTree) DecodeFlatBits(buf []byte, bitsPerValue int) bool {
	br := NewBitReader(buf)
	return t.DecodeFlat(br, bitsPerValue)
}

// ProgressiveDecodeBool decodes a single leaf's inclusion decision at a given
// threshold using hierarchical tag-tree logic per ISO 15444-1 B.10.2.
// Bit convention matches OpenJPEG: bit=1 means "value IS the current m" (known),
// bit=0 means "value > current m" (increment m).
// Loop condition: while m < threshold (strictly less-than per spec).
// Returns (included, ok) where included means the leaf value < threshold.
func (t *TagTree) ProgressiveDecodeBool(br *BitReader, x, y, threshold int) (bool, bool) {
	if threshold < 0 {
		threshold = 0
	}
	li := y*t.w + x
	// Compute node index at each level from leaf (level 0) to root (last)
	idxs := make([]int, len(t.levels))
	lw, lh := t.w, t.h
	cx, cy := x, y
	for lvl := 0; lvl < len(t.levels); lvl++ {
		idxs[lvl] = cy*lw + cx
		if lw == 1 && lh == 1 {
			break
		}
		cx, cy = cx/2, cy/2
		if lw > 1 {
			lw = (lw + 1) / 2
		}
		if lh > 1 {
			lh = (lh + 1) / 2
		}
	}
	top := len(t.levels) - 1
	for top > 0 && t.levels[top].w*t.levels[top].h == 0 {
		top--
	}
	// Walk from root down to leaf
	for lvl := top; lvl >= 0; lvl-- {
		level := &t.levels[lvl]
		idx := idxs[lvl]
		// Spec: while m < threshold and not known, read bit.
		// bit=1: value IS m → mark known
		// bit=0: value > m → m++
		for level.m[idx] < threshold && !level.known[idx] {
			b, ok := br.ReadBit()
			if !ok {
				return false, false
			}
			if b == 1 {
				level.known[idx] = true
			} else {
				level.m[idx]++
			}
		}
		// Propagate m downward
		if lvl > 0 {
			child := &t.levels[lvl-1]
			cidx := idxs[lvl-1]
			if child.m[cidx] < level.m[idx] {
				child.m[cidx] = level.m[idx]
			}
		}
	}
	leaf := &t.levels[0]
	m := leaf.m[li]
	return m < threshold, true
}

// ProgressiveDecodeValue decodes the integer value for a leaf by querying
// ProgressiveDecodeBool at increasing thresholds. Returns the leaf's value
// (the first threshold where the leaf IS included, meaning value < threshold,
// i.e., value = threshold - 1... actually we return the leaf's m once known).
func (t *TagTree) ProgressiveDecodeValue(br *BitReader, x, y, maxBits int) (int, bool) {
	li := y*t.w + x
	v := 0
	for {
		_, ok := t.ProgressiveDecodeBool(br, x, y, v+1)
		if !ok {
			return 0, false
		}
		leaf := &t.levels[0]
		if leaf.known[li] {
			return leaf.m[li], true
		}
		v++
		if maxBits > 0 && v >= (1<<uint(maxBits)) {
			return leaf.m[li], true
		}
	}
}
