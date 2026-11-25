package tiff

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"image"
	"io"

	jpegport "light-media/builtinplugins/plugins/autosignin/imagehash/jpegport"
)

type oldJPEGPlanarPart struct {
	fileOffset, start, end int64
	interchange            bool
}

type oldJPEGPlanarData struct {
	r      io.ReaderAt
	parts  []oldJPEGPlanarPart
	length int64
}

func (s *oldJPEGPlanarData) add(offset, length int64, interchange bool) {
	if offset != 0 && length != 0 {
		s.parts = append(s.parts, oldJPEGPlanarPart{offset, s.length, s.length + length, interchange})
		s.length += length
	}
}

// The old JPEG reader treats the interchange bytes and the striles as one
// stream. It inserts restart markers only at strile boundaries when decoding.
func (s *oldJPEGPlanarData) readers(start, end int64, restarts bool) []io.Reader {
	var readers []io.Reader
	insertRestart := false
	restartIndex := byte(0)
	for _, part := range s.parts {
		a, b := max(start, part.start), min(end, part.end)
		if b <= a {
			continue
		}
		if restarts && insertRestart {
			readers = append(readers, bytes.NewReader([]byte{0xff, 0xd0 + restartIndex}))
			restartIndex = (restartIndex + 1) & 7
		}
		readers = append(readers, io.NewSectionReader(s.r, part.fileOffset+a-part.start, b-a))
		insertRestart = !part.interchange
	}
	return readers
}

type oldJPEGPlanarOffset struct {
	r io.Reader
	n int64
}

func (r *oldJPEGPlanarOffset) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.n += int64(n)
	return n, err
}

type oldJPEGPlanarHeader struct {
	tables    []byte
	frame     []byte
	sof       byte
	restart   []byte
	firstScan [2]byte
}

// libtiff's OJPEGReadSecondarySos finds the next single-component SOS by
// walking the entropy bytes. Stuffed FF bytes and restart markers aren't SOS.
func findOldJPEGPlanarScan(r *bufio.Reader, offset *oldJPEGPlanarOffset) (int64, [2]byte, error) {
	for {
		start := offset.n - int64(r.Buffered())
		b, err := r.ReadByte()
		if err != nil {
			return 0, [2]byte{}, err
		}
		if b != 0xff {
			continue
		}
		for b == 0xff {
			b, err = r.ReadByte()
			if err != nil {
				return 0, [2]byte{}, err
			}
		}
		if b == 0xda {
			_, data, err := readJPEGMarker(io.MultiReader(bytes.NewReader([]byte{0xff, b}), r))
			if err != nil {
				return 0, [2]byte{}, err
			}
			scan, err := oldJPEGPlanarScanFields(data)
			return start, scan, err
		}
	}
}

func oldJPEGPlanarScanFields(data []byte) ([2]byte, error) {
	if len(data) != 6 || data[0] != 1 {
		return [2]byte{}, FormatError("invalid old JPEG planar SOS")
	}
	return [2]byte{data[1], data[2]}, nil
}

func (d *decoder) readOldJPEGPlanarHeader(r *bufio.Reader, h oldJPEGPlanarHeader, width, height, across int) (oldJPEGPlanarHeader, error) {
	var tables jpegTableSet
	for {
		first, err := r.Peek(1)
		if err != nil {
			return h, err
		}
		if first[0] != 0xff {
			if h.frame == nil {
				if across != 1 {
					// libtiff synthesizes just the padded image height,
					// insufficient for a stack across several columns.
					return h, UnsupportedError("old JPEG planar tiles in several columns require a frame header")
				}
				return d.oldJPEGPlanarTableHeader(h, width, height)
			}
			h.tables = tables.bytes()
			return h, nil
		}
		marker, data, err := readJPEGMarker(r)
		if err != nil {
			return h, err
		}
		switch {
		case marker == 0xd8 || jpegIgnoredMarker(marker):
		case marker == 0xdb || marker == 0xc4:
			if err := tables.add(marker, data); err != nil {
				return h, err
			}
		case marker == 0xc0 || marker == 0xc1:
			if h.frame != nil || len(data) != 15 || data[0] != 8 || data[5] != 3 {
				return h, FormatError("invalid old JPEG planar SOF")
			}
			if int(binary.BigEndian.Uint16(data[1:3])) < height ||
				int(binary.BigEndian.Uint16(data[3:5])) != width {
				return h, FormatError("old JPEG planar dimensions do not match TIFF")
			}
			if data[7] != 0x11 || data[10] != 0x11 || data[13] != 0x11 {
				return h, UnsupportedError("old JPEG planar subsampling other than 1:1")
			}
			// The RGBA reader stops after the TIFF strips or tiles, even
			// when the JPEG frame advertises extra rows.
			binary.BigEndian.PutUint16(data[1:3], uint16(height))
			h.frame, h.sof = data, marker
		case marker == 0xdd:
			if len(data) != 2 {
				return h, FormatError("invalid old JPEG planar DRI")
			}
			h.restart = data
		case marker == 0xda:
			if h.frame == nil {
				return h, FormatError("missing old JPEG planar SOF")
			}
			h.tables = tables.bytes()
			h.firstScan, err = oldJPEGPlanarScanFields(data)
			return h, err
		default:
			return h, UnsupportedError("old JPEG planar interchange marker")
		}
	}
}

func (d *decoder) oldJPEGPlanarTableHeader(h oldJPEGPlanarHeader, width, height int) (oldJPEGPlanarHeader, error) {
	if d.ycbcr.horizontal != 1 || d.ycbcr.vertical != 1 {
		return h, UnsupportedError("old JPEG planar subsampling other than 1:1")
	}
	var selectors [3][3]byte
	for i, tag := range [...]int{tJPEGQTables, tJPEGDCTables, tJPEGACTables} {
		markers, ids, err := d.oldJPEGTableMarkers(tag, 3)
		if err != nil {
			return h, err
		}
		h.tables = append(h.tables, markers...)
		selectors[i] = ids
	}
	h.frame = binary.BigEndian.AppendUint16([]byte{8}, uint16(height))
	h.frame = binary.BigEndian.AppendUint16(h.frame, uint16(width))
	h.frame = append(h.frame, 3)
	for i, selector := range selectors[0] {
		h.frame = append(h.frame, byte(i), 0x11, selector)
	}
	h.sof = 0xc0
	h.firstScan[1] = selectors[1][0]<<4 | selectors[2][0]
	return h, nil
}

func (h oldJPEGPlanarHeader) prefix(plane int, scan [2]byte) ([]byte, error) {
	component := h.frame[6+3*plane : 9+3*plane]
	if scan[0] != component[0] {
		return nil, FormatError("old JPEG planar scan component does not match frame")
	}
	frame := append(append([]byte(nil), h.frame[:5]...), 1)
	frame = append(frame, component...)
	prefix := append([]byte{0xff, 0xd8}, h.tables...)
	prefix = appendJPEGMarker(prefix, h.sof, frame)
	return appendJPEGMarker(appendJPEGRestart(prefix, h.restart), 0xda, []byte{1, scan[0], scan[1], 0, 63, 0}), nil
}

func (d *decoder) decodeOldJPEGPlanarYCbCr(l ycbcrLayout) (image.Image, error) {
	width, height := d.config.Width, d.config.Height
	if l.tiled {
		if l.across > 65535/l.height/l.down {
			return nil, FormatError("old JPEG planar tile stack exceeds JPEG frame size")
		}
		width, height = l.width, l.height*l.across*l.down
		if _, ok := checkedMul3(width, height, maxBytesPerPixel); !ok {
			return nil, FormatError("old JPEG planar tile stack is too large")
		}
	}
	if width > 65535 || height > 65535 {
		return nil, FormatError("old JPEG planar dimensions exceed JPEG frame size")
	}
	stream := oldJPEGPlanarData{r: d.r}
	if interchange := d.oldJPEGInterchange(); interchange != nil {
		_, offset, length := interchange.Outer()
		stream.add(offset, length, true)
	}
	for i := 0; i < l.offsets.count; i++ {
		offset, err := l.offsets.value(i)
		if err != nil {
			return nil, err
		}
		size, err := l.counts.value(i)
		if err != nil {
			return nil, err
		}
		stream.add(int64(offset), int64(size), false)
	}
	header := oldJPEGPlanarHeader{restart: d.oldJPEGRestart()}
	if l.height < d.config.Height {
		interval := ((width + 7) / 8) * (l.height / 8)
		if interval > 65535 {
			return nil, FormatError("old JPEG planar restart interval exceeds 65535 MCUs")
		}
		header.restart = binary.BigEndian.AppendUint16(nil, uint16(interval))
	}
	offset := &oldJPEGPlanarOffset{r: io.MultiReader(stream.readers(0, stream.length, false)...)}
	input := bufio.NewReader(offset)
	header, err := d.readOldJPEGPlanarHeader(input, header, width, height, l.across)
	if err != nil {
		return nil, err
	}
	planes, err := readOldJPEGPlanarPlanes(&stream, input, offset, header, width, height)
	if err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, d.config.Width, d.config.Height))
	if l.tiled {
		// Each planar JPEG is a vertical stack in tile order. Copy the
		// corresponding samples into the TIFF grid and clip edge tiles.
		for tile := range l.across * l.down {
			x, y := tile%l.across*l.width, tile/l.across*l.height
			rect := image.Rect(x, y, x+l.width, y+l.height).Intersect(img.Bounds())
			drawOldJPEGPlanarColor(img, rect, planes, width, tile*l.height)
		}
	} else {
		drawOldJPEGPlanarColor(img, img.Bounds(), planes, width, 0)
	}
	return img, nil
}

func drawOldJPEGPlanarColor(dst *image.RGBA, rect image.Rectangle, planes [3][]byte, width, firstRow int) {
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		row := (firstRow + y - rect.Min.Y) * width
		for x := rect.Min.X; x < rect.Max.X; x++ {
			i := row + x - rect.Min.X
			cb, cr := byte(0), byte(0)
			if planes[1] != nil {
				cb = planes[1][i]
			}
			if planes[2] != nil {
				cr = planes[2][i]
			}
			dst.SetRGBA(x, y, oldJPEGColor(planes[0][i], cb, cr))
		}
	}
}

func readOldJPEGPlanarPlanes(stream *oldJPEGPlanarData, input *bufio.Reader, offset *oldJPEGPlanarOffset, h oldJPEGPlanarHeader, width, height int) ([3][]byte, error) {
	var planes [3][]byte
	scan := h.firstScan
	start := offset.n - int64(input.Buffered())
	for plane := range planes {
		end := stream.length
		var nextScan [2]byte
		var nextErr error
		if plane < len(planes)-1 {
			end, nextScan, nextErr = findOldJPEGPlanarScan(input, offset)
			if nextErr != nil {
				if nextErr != io.EOF {
					return planes, nextErr
				}
				end = stream.length
			}
		}
		prefix, err := h.prefix(plane, scan)
		if err != nil {
			return planes, err
		}
		readers := []io.Reader{bytes.NewReader(prefix)}
		readers = append(readers, stream.readers(start, end, true)...)
		readers = append(readers, bytes.NewReader([]byte{0xff, 0xd9}))
		pixels, err := jpegport.DecodeRasterChecked(io.MultiReader(readers...), func(w, h int) error {
			if w != width || h != height {
				return FormatError("old JPEG planar dimensions do not match TIFF")
			}
			return nil
		})
		if err != nil {
			return planes, err
		}
		planes[plane] = pixels.Pixels
		if nextErr == io.EOF {
			// Pillow calls TIFFRGBAImageBegin with stoponerr disabled.
			// A missing secondary SOS leaves the chroma buffers zeroed.
			break
		}
		scan, start = nextScan, offset.n-int64(input.Buffered())
	}
	return planes, nil
}
