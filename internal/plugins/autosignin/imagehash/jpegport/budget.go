package jpeg

// ResourceLimitError is a local allocation policy, separate from JPEG syntax
// or Pillow compatibility. No large raster/coefficient slices exist at SOF.
type ResourceLimitError string

func (e ResourceLimitError) Error() string { return "JPEG resource limit: " + string(e) }

// Bound the major live pixel allocations, including the caller's later raw
// hashing copy. Encoded input (separately <=8MiB) and Go/runtime overhead are
// outside this estimate, so it is not a process RSS guarantee.
const maxWorkingBytes int64 = 64 << 20

func (d *decoder) checkMemoryBudget() error {
	channels := int64(d.nComp)
	bytes := int64(d.width)*int64(d.height)*channels*2 + 64<<10
	mxx := (int64(d.width) + 8*int64(d.maxH) - 1) / (8 * int64(d.maxH))
	myy := (int64(d.height) + 8*int64(d.maxV) - 1) / (8 * int64(d.maxV))
	for _, c := range d.comp[:d.nComp] {
		blocks := mxx * myy * int64(c.h) * int64(c.v)
		bytes += blocks * 64
		if d.progressive {
			bytes += blocks * 256
		}
	}
	if bytes > maxWorkingBytes {
		return ResourceLimitError("decoded working buffers exceed 64 MiB")
	}
	return nil
}
