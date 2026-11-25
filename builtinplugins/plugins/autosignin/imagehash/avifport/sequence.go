package avif

import (
	"image"
	"math"

	"github.com/gen2brain/gav1d/av1"
)

type track struct {
	id            uint32
	width, height int
	timescale     uint32
	samples       []extent
	timing        []timeRun
	sizes         []uint32
	offsets       []uint64
	chunks        []chunkRun
	sampleCount   int
	sampleSize    uint32
	auxFor        uint32
	premBy        uint32
	av01          bool
	properties    []property

	duration   uint64
	hasEdits   bool
	repeating  bool
	segmentDur uint64
}

const indefiniteDuration = ^uint64(0)

// loopCount is zero for forever, -1 to show each frame once, otherwise the
// number of extra plays. ISO/IEC 23008-12 section 9.6.1.
func (t *track) loopCount() int {
	if !t.hasEdits {
		return 0
	}
	if !t.repeating {
		return -1
	}
	if t.duration == indefiniteDuration || t.duration == 0 || t.segmentDur == 0 {
		return 0
	}

	n := t.duration / t.segmentDur
	if t.duration%t.segmentDur != 0 {
		n++
	}
	if n <= 1 {
		return -1
	}
	if n-1 > math.MaxInt32 {
		return 0
	}

	return int(n - 1)
}

type movie struct {
	tracks []track
}

func parseMoov(b []byte) (*movie, error) {
	m := &movie{}

	err := eachBox(b, func(typ string, b []byte) error {
		if typ != "trak" {
			return nil
		}
		t, err := parseTrak(b)
		if err != nil {
			return err
		}
		m.tracks = append(m.tracks, t)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return m, nil
}

func parseTrak(b []byte) (track, error) {
	var t track

	err := eachBox(b, func(typ string, b []byte) error {
		switch typ {
		case "tkhd":
			r := &reader{b: b}
			v, _ := r.fullBox()
			if v > 1 {
				return ErrInvalid
			}
			if v == 1 {
				r.skip(16)
			} else {
				r.skip(8)
			}
			t.id = r.u32()
			r.skip(4)
			if v == 1 {
				t.duration = r.u64()
			} else {
				t.duration = uint64(r.u32())
				if t.duration == 1<<32-1 {
					t.duration = indefiniteDuration
				}
			}
			r.skip(52)
			t.width, t.height = int(r.u32()>>16), int(r.u32()>>16)
			if r.err {
				return ErrInvalid
			}

		case "edts":
			t.hasEdits = true

			return eachBox(b, func(typ string, b []byte) error {
				if typ != "elst" {
					return nil
				}
				r := &reader{b: b}
				v, flags := r.fullBox()
				if flags&1 == 0 {
					return nil
				}
				t.repeating = true
				if r.u32() != 1 {
					return nil
				}
				if v == 1 {
					t.segmentDur = r.u64()
				} else {
					t.segmentDur = uint64(r.u32())
				}
				if r.err {
					return ErrInvalid
				}

				return nil
			})

		case "tref":
			return eachBox(b, func(typ string, b []byte) error {
				r := &reader{b: b}
				switch typ {
				case "auxl":
					t.auxFor = r.u32()
				case "prem":
					t.premBy = r.u32()
				}
				if r.err {
					return ErrInvalid
				}

				return nil
			})

		case "mdia":
			return eachBox(b, func(typ string, b []byte) error {
				switch typ {
				case "mdhd":
					r := &reader{b: b}
					v, _ := r.fullBox()
					if v == 1 {
						r.skip(16)
					} else {
						r.skip(8)
					}
					t.timescale = r.u32()
					if r.err {
						return ErrInvalid
					}

				case "minf":
					return eachBox(b, func(typ string, b []byte) error {
						if typ != "stbl" {
							return nil
						}

						return t.parseStbl(b)
					})
				}

				return nil
			})
		}

		return nil
	})

	return t, err
}

type chunkRun struct {
	firstChunk, perChunk uint32
}

type timeRun struct {
	count, delta uint32
}

type timeCursor struct {
	runs []timeRun
	used uint32
}

func (c *timeCursor) next() uint32 {
	for len(c.runs) != 0 && c.used == c.runs[0].count {
		c.runs = c.runs[1:]
		c.used = 0
	}
	if len(c.runs) == 0 {
		return 0
	}
	c.used++

	return c.runs[0].delta
}

func (t *track) parseStbl(b []byte) error {
	return eachBox(b, func(typ string, b []byte) error {
		r := &reader{b: b}

		switch typ {
		case "stts":
			r.fullBox()
			n := int(r.u32())
			if r.err || n < 0 || n > r.remaining()/8 {
				return ErrInvalid
			}
			for range n {
				count := r.u32()
				delta := r.u32()
				if r.err {
					return ErrInvalid
				}
				t.timing = append(t.timing, timeRun{count, delta})
			}

		case "stsz":
			r.fullBox()
			t.sampleSize = r.u32()
			n := int(r.u32())
			if r.err || n < 0 || n > 1<<24 {
				return ErrInvalid
			}
			t.sampleCount = n
			if t.sampleSize == 0 {
				if n > r.remaining()/4 {
					return ErrInvalid
				}
				t.sizes = make([]uint32, n)
				for i := range n {
					t.sizes[i] = r.u32()
				}
			}

		case "stsd":
			r.fullBox()
			r.u32()
			if r.err {
				return ErrInvalid
			}

			return eachBox(b[r.i:], func(typ string, b []byte) error {
				if typ != "av01" || t.av01 {
					return nil
				}
				if len(b) < 78 {
					return ErrInvalid
				}
				var props metaBox
				if err := props.parseIpco(b[78:]); err != nil {
					return err
				}
				t.av01, t.properties = true, props.props
				return nil
			})

		case "stsc":
			r.fullBox()
			n := int(r.u32())
			if r.err || n < 0 || n > r.remaining()/12 {
				return ErrInvalid
			}
			for range n {
				first := r.u32()
				per := r.u32()
				r.u32()
				if r.err {
					return ErrInvalid
				}
				if first == 0 || per == 0 ||
					(len(t.chunks) != 0 && first <= t.chunks[len(t.chunks)-1].firstChunk) {
					return ErrInvalid
				}
				t.chunks = append(t.chunks, chunkRun{first, per})
			}

		case "stco", "co64":
			r.fullBox()
			n := int(r.u32())
			entryBytes := 4
			if typ == "co64" {
				entryBytes = 8
			}
			if r.err || n < 0 || n > 1<<24 || n > r.remaining()/entryBytes {
				return ErrInvalid
			}
			t.offsets = make([]uint64, n)
			for i := range n {
				if typ == "stco" {
					t.offsets[i] = uint64(r.u32())
				} else {
					t.offsets[i] = r.u64()
				}
			}
		}

		if r.err {
			return ErrInvalid
		}

		return nil
	})
}

// layout gives every sample of a track being decoded a file offset.
func (t *track) layout(fileSize uint64) error {
	if t.samples != nil {
		return nil
	}
	if t.sampleCount <= 0 || uint64(t.sampleCount) > fileSize ||
		uint64(t.sampleCount)*uint64(t.sampleSize) > fileSize ||
		len(t.offsets) == 0 || len(t.chunks) == 0 || t.chunks[0].firstChunk != 1 {
		return ErrInvalid
	}
	if t.sampleSize == 0 && len(t.sizes) != t.sampleCount {
		return ErrInvalid
	}

	samples := make([]extent, 0, t.sampleCount)
	run := 0
	for c, off := range t.offsets {
		for run+1 < len(t.chunks) && uint64(t.chunks[run+1].firstChunk) <= uint64(c)+1 {
			run++
		}
		for range t.chunks[run].perChunk {
			if len(samples) == t.sampleCount {
				t.samples = samples

				return nil
			}
			n := t.sampleSize
			if n == 0 {
				n = t.sizes[len(samples)]
			}
			if n == 0 || off > fileSize || uint64(n) > fileSize-off {
				return ErrInvalid
			}
			samples = append(samples, extent{off: off, len: uint64(n)})
			off += uint64(n)
		}
	}
	t.samples = samples

	return nil
}

func (m *movie) colorTrack() *track {
	if m == nil {
		return nil
	}
	for i := range m.tracks {
		t := &m.tracks[i]
		if t.hasAV1Samples() && t.auxFor == 0 {
			return t
		}
	}

	return nil
}

func (m *movie) alphaTrack(id uint32) *track {
	for i := range m.tracks {
		t := &m.tracks[i]
		if !t.hasAV1Samples() || t.auxFor != id {
			continue
		}
		if aux := t.property("auxi"); aux == nil || isAlphaURN(aux.auxC) {
			return t
		}
	}

	return nil
}

func (t *track) hasAV1Samples() bool {
	return t.av01 && t.id != 0 && len(t.offsets) > 0 && t.sampleCount > 0
}

func (t *track) property(typ string) *property {
	for i := range t.properties {
		if t.properties[i].typ == typ {
			return &t.properties[i]
		}
	}
	return nil
}

func (t *track) colorInfo(pic *av1.Picture) ColorInfo {
	ci := pictureColor(pic)
	for _, p := range t.properties {
		ci.applyProperty(p.colr)
	}
	return ci
}

func (f *file) usesTracks() bool {
	return f.majorBrand == "avis" || (f.majorBrand != "avif" && f.movie != nil && len(f.movie.tracks) > 0)
}

func (f *file) decodeFirstFrame(o Options) (image.Image, ColorInfo, error) {
	t := f.movie.colorTrack()
	if t == nil || t.width == 0 || t.height == 0 {
		return nil, ColorInfo{}, ErrInvalid
	}
	pic, err := f.firstTrackPicture(t)
	if err != nil {
		return nil, ColorInfo{}, err
	}
	scaled, err := f.normalizePicture(pic, t.width, t.height, false)
	if err != nil {
		pic.Release()
		return nil, ColorInfo{}, err
	}
	pic = scaled
	var alpha *av1.Picture
	aux := f.movie.alphaTrack(t.id)
	if aux != nil {
		if aux.width != t.width || aux.height != t.height {
			pic.Release()
			return nil, ColorInfo{}, ErrInvalid
		}
		alpha, err = f.firstTrackPicture(aux)
		if err != nil {
			pic.Release()
			return nil, ColorInfo{}, err
		}
		if alpha.BitDepth != pic.BitDepth {
			pic.Release()
			alpha.Release()
			return nil, ColorInfo{}, ErrInvalid
		}
		scaled, err := f.normalizePicture(alpha, t.width, t.height, true)
		if err != nil {
			pic.Release()
			alpha.Release()
			return nil, ColorInfo{}, err
		}
		alpha = scaled
	}
	ci := t.colorInfo(pic)
	img, err := convertPictures(o, pic, alpha, ci, aux != nil && t.premBy == aux.id, o.ToYCbCr)
	return img, ci, err
}

// Read the first sample only. The default image does not require decoding
// later frames, which may be inter-predicted or may fail independently.
func (f *file) firstTrackPicture(t *track) (*av1.Picture, error) {
	if err := t.layout(f.src.size); err != nil {
		return nil, err
	}
	if len(t.samples) == 0 {
		return nil, ErrInvalid
	}
	s := t.samples[0]
	b, err := f.src.at(s.off, s.len)
	if err != nil {
		return nil, err
	}
	d := av1.Decoder{FrameSizeLimit: f.frameSizeLimit}
	pics, err := d.DecodeOBUs(b)
	if err != nil {
		return nil, err
	}
	if len(pics) == 0 {
		return nil, ErrInvalid
	}
	for _, pic := range pics[1:] {
		if pic != pics[0] {
			pic.Release()
		}
	}
	return pics[0], nil
}

// sequenceBound refuses a picture or alpha track longer than Options.MaxFrames.
func (f *file) sequenceBound() error {
	n := f.frames()
	if n == 0 {
		return nil
	}

	t := f.movie.colorTrack()
	if t == nil {
		return nil
	}

	if t.sampleCount > n {
		return ErrUnsupported
	}

	if at := f.movie.alphaTrack(t.id); at != nil && at.sampleCount > n {
		return ErrUnsupported
	}

	return nil
}

// decodeSequence decodes an image sequence track.
func (f *file) decodeSequence(o Options) (*AVIF, error) {
	t := f.movie.colorTrack()
	if t == nil || t.timescale == 0 {
		return nil, nil
	}
	alphaTrack := f.movie.alphaTrack(t.id)

	colors, err := f.decodeTrack(t)
	if err != nil {
		return nil, err
	}
	var alphas []*av1.Picture
	if alphaTrack != nil {
		if alphas, err = f.decodeTrack(alphaTrack); err != nil {
			return nil, err
		}
	}

	out := &AVIF{LoopCount: t.loopCount()}
	if len(colors) > 0 {
		out.Color = t.colorInfo(colors[0])
	}
	// A track can show the same picture more than once, so nothing may go back
	// to the pool until every frame referencing it has been converted, and a
	// picture an image still aliases may never go back at all.
	var distinct []*av1.Picture
	seen := map[*av1.Picture]bool{}
	kept := map[*av1.Picture]bool{}
	note := func(p *av1.Picture, alias bool) {
		if p == nil {
			return
		}
		if !seen[p] {
			seen[p] = true
			distinct = append(distinct, p)
		}
		if alias {
			kept[p] = true
		}
	}

	timing := timeCursor{runs: t.timing}
	for i, pic := range colors {
		var alpha *av1.Picture
		if i < len(alphas) {
			alpha = alphas[i]
		}
		img, err := toImageColor(pic, alpha, t.colorInfo(pic), o.ycbcrFor(f, f.primary()))
		if err != nil {
			return nil, err
		}
		alias := aliasesPicture(img)
		note(pic, alias)
		note(alpha, alias)
		if o.AutoRotate {
			if img, err = f.applyTransforms(img, f.primary()); err != nil {
				return nil, err
			}
		}
		out.Image = append(out.Image, img)

		d := float64(timing.next()) / float64(t.timescale)
		out.Delay = append(out.Delay, d)
	}
	for _, p := range distinct {
		if !kept[p] {
			p.Release()
		}
	}

	if len(out.Image) == 0 {
		return nil, nil
	}

	return out, nil
}

func (f *file) decodeTrack(t *track) ([]*av1.Picture, error) {
	if err := t.layout(f.src.size); err != nil {
		return nil, err
	}
	var d av1.Decoder
	d.FrameSizeLimit = f.frameSizeLimit
	var out []*av1.Picture

	for _, s := range t.samples {
		if s.len == 0 {
			return nil, ErrInvalid
		}
		b, err := f.src.at(s.off, s.len)
		if err != nil {
			return nil, err
		}
		pics, err := d.DecodeOBUs(b)
		if err != nil {
			return nil, err
		}
		out = append(out, pics...)
	}

	return out, nil
}
