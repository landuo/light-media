package tiff

import "encoding/binary"

// TIFF 6 stores these tables as raw bytes at per-component offsets. libtiff
// builds a baseline frame and scan header when the JPEG SOF is absent.
func (d *decoder) oldJPEGTableHeader(components, horizontal, vertical int, restart []byte) ([]byte, error) {
	prefix := []byte{0xff, 0xd8}
	var selectors [3][3]byte
	for i, tag := range [...]int{tJPEGQTables, tJPEGDCTables, tJPEGACTables} {
		tables, ids, err := d.oldJPEGTableMarkers(tag, components)
		if err != nil {
			return nil, err
		}
		prefix = append(prefix, tables...)
		selectors[i] = ids
	}
	frame := binary.BigEndian.AppendUint16([]byte{8}, uint16(d.config.Height))
	frame = binary.BigEndian.AppendUint16(frame, uint16(d.config.Width))
	frame = append(frame, byte(components))
	scan := []byte{byte(components)}
	for i := 0; i < components; i++ {
		sampling := byte(0x11)
		if i == 0 {
			sampling = byte(horizontal<<4 | vertical)
		}
		frame = append(frame, byte(i), sampling, selectors[0][i])
		scan = append(scan, byte(i), selectors[1][i]<<4|selectors[2][i])
	}
	prefix = appendJPEGMarker(prefix, 0xc0, frame)
	return appendJPEGMarker(appendJPEGRestart(prefix, restart), 0xda, append(scan, 0, 63, 0)), nil
}

func (d *decoder) oldJPEGTableMarkers(tag, components int) ([]byte, [3]byte, error) {
	var selectors [3]byte
	p := d.ifd[tag]
	count := d.byteOrder.Uint32(p[4:8])
	if d.byteOrder.Uint16(p[2:4]) != dtLong || count == 0 || count > uint32(components) {
		return nil, selectors, FormatError("invalid old JPEG table offsets")
	}
	values, err := d.ifdUint(p[:], components)
	if err != nil {
		return nil, selectors, err
	}
	if values[0] == 0 {
		return nil, selectors, FormatError("missing first old JPEG table offset")
	}
	var offsets [3]uint
	copy(offsets[:], values)
	var markers []byte
	for i, offset := range offsets[:components] {
		if i > 0 && (offset == 0 || offset == offsets[i-1]) {
			selectors[i] = selectors[i-1]
			continue
		}
		for j := 0; j < i; j++ {
			if offset == offsets[j] {
				return nil, selectors, FormatError("non-consecutive shared old JPEG table offset")
			}
		}
		marker, id, size := byte(0xdb), byte(i), uint64(64)
		if tag != tJPEGQTables {
			marker, size = 0xc4, 16
			if tag == tJPEGACTables {
				id |= 0x10
			}
			counts, err := safeReadAt(d.r, size, int64(offset))
			if err != nil {
				return nil, selectors, err
			}
			for _, n := range counts {
				size += uint64(n)
			}
			if size > 16+256 {
				return nil, selectors, FormatError("invalid old JPEG Huffman table length")
			}
		}
		table, err := safeReadAt(d.r, size, int64(offset))
		if err != nil {
			return nil, selectors, err
		}
		markers = appendJPEGMarker(markers, marker, append([]byte{id}, table...))
		selectors[i] = byte(i)
	}
	return markers, selectors, nil
}
