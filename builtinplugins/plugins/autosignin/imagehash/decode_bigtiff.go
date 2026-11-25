package imagehash

import (
	"encoding/binary"
	"math"
)

// BigTIFF changes the IFD container widths, not the strip/tile codecs. Normalize
// the first IFD for the private TIFF decoder, retaining the original strip data
// and offsets. Tjupt never seeks another frame. The host's byte transport limit
// is below Classic TIFF's 4GiB address space.
func classicTIFF(data []byte) ([]byte, error) {
	if len(data) < 16 {
		return nil, ErrInvalid
	}
	var order binary.ByteOrder = binary.LittleEndian
	if string(data[:2]) == "MM" {
		order = binary.BigEndian
	}
	if order.Uint16(data[4:6]) != 8 || order.Uint16(data[6:8]) != 0 {
		return nil, ErrUnsupported
	}
	off := order.Uint64(data[8:16])
	if off > uint64(len(data)-8) {
		return nil, ErrInvalid
	}
	count := order.Uint64(data[off : off+8])
	if count > math.MaxUint16 || count > uint64(len(data)-int(off)-8)/20 || len(data) > math.MaxUint32 {
		return nil, ErrInvalid
	}
	result := append([]byte(nil), data...)
	start := len(result)
	result = append(result, make([]byte, 2+int(count)*12+4)...)
	order.PutUint16(result[2:4], 42)
	order.PutUint32(result[4:8], uint32(start))
	order.PutUint16(result[start:start+2], uint16(count))
	typeSizes := [...]uint64{0, 1, 1, 2, 4, 8, 1, 1, 2, 4, 8, 4, 8, 4, 0, 0, 8, 8, 8}
	for i := uint64(0); i < count; i++ {
		entry := data[off+8+i*20 : off+28+i*20]
		typ := order.Uint16(entry[2:4])
		n := order.Uint64(entry[4:12])
		if int(typ) >= len(typeSizes) || typeSizes[typ] == 0 || n > math.MaxUint32 || n > math.MaxUint64/typeSizes[typ] {
			return nil, ErrUnsupported
		}
		size := n * typeSizes[typ]
		value := entry[12:20]
		if size > 8 {
			ptr := order.Uint64(value)
			if ptr > uint64(len(data)) || size > uint64(len(data))-ptr {
				return nil, ErrInvalid
			}
			value = data[ptr : ptr+size]
		} else {
			value = value[:size]
		}
		if typ >= 16 {
			converted := make([]byte, int(n)*4)
			for k := uint64(0); k < n; k++ {
				v := order.Uint64(value[k*8:])
				if typ == 17 {
					if int64(v) < math.MinInt32 || int64(v) > math.MaxInt32 {
						return nil, ErrUnsupported
					}
				} else if v > math.MaxUint32 {
					return nil, ErrUnsupported
				}
				order.PutUint32(converted[k*4:], uint32(v))
			}
			value = converted
			typ = 4
			if order.Uint16(entry[2:4]) == 17 {
				typ = 9
			}
		}
		at := start + 2 + int(i)*12
		copy(result[at:at+2], entry[:2])
		order.PutUint16(result[at+2:at+4], typ)
		order.PutUint32(result[at+4:at+8], uint32(n))
		if len(value) <= 4 {
			copy(result[at+8:at+12], value)
		} else {
			if uint64(len(result))+uint64(len(value)) > math.MaxUint32 {
				return nil, ErrUnsupported
			}
			order.PutUint32(result[at+8:at+12], uint32(len(result)))
			result = append(result, value...)
		}
	}
	return result, nil
}
