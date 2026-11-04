package mediaservervendors

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"io"
)

// Only errors from these concrete owned codec readers become source errors.
// Do not unwrap an unknown/current error merely because it has a codec cause.
func requestsContentCodecError(err error) bool {
	switch err {
	case gzip.ErrHeader, gzip.ErrChecksum, zlib.ErrHeader, zlib.ErrChecksum, zlib.ErrDictionary:
		return true
	}
	_, ok := err.(flate.CorruptInputError)
	return ok
}
func requestsContentTruncated(err error) bool { return err == io.EOF || err == io.ErrUnexpectedEOF }

// zlib validates the two-byte magic and then the two-byte method/flags word
// as those fields become available. Go's gzip reader first requests ten bytes,
// so its UnexpectedEOF alone cannot distinguish these errors from a valid
// incomplete header. A lone byte remains incomplete, even if it is not 0x1f.
func requestsContentGzipPrefix(raw []byte) error {
	if len(raw) >= 2 && (raw[0] != 0x1f || raw[1] != 0x8b) {
		return gzip.ErrHeader
	}
	if len(raw) >= 4 && (raw[2] != 8 || raw[3]&0xe0 != 0) {
		return gzip.ErrHeader
	}
	return nil
}

// A later gzip member's corrupt call loses only that member's not-yet-returned
// bytes. Previous complete members in the same call and earlier 10240-byte
// calls survive. This differs from retaining n blindly on Go Read's error.
func requestsContentGzip(raw []byte, check func() error) ([]byte, error) {
	input := requestsContentInput(raw, check)
	var reader *gzip.Reader
	var out bytes.Buffer
	member := 0
	done := false
	closeReader := func() {
		if reader != nil {
			_ = reader.Close()
			reader = nil
		}
	}
	defer closeReader()
	for !done {
		if e := check(); e != nil {
			return nil, e
		}
		call := make([]byte, 0, requestsContentChunk)
		for len(call) < requestsContentChunk && !done {
			if reader == nil {
				if input.source.Len() == 0 {
					done = true
					break
				}
				var next *gzip.Reader
				e := requestsContentGzipPrefix(raw[len(raw)-input.source.Len():])
				if e == nil {
					next, e = gzip.NewReader(input)
				}
				if g := check(); g != nil {
					return nil, g
				}
				if e != nil {
					if requestsContentTruncated(e) {
						done = true
						break
					}
					if !requestsContentCodecError(e) {
						return nil, e
					}
					if member == 0 {
						return nil, requestsContentError(e)
					}
					done = true
					break
				}
				reader = next
				reader.Multistream(false)
				member++
			}
			part := make([]byte, 0, requestsContentChunk-len(call))
			ended := false
			for len(call)+len(part) < requestsContentChunk {
				if e := check(); e != nil {
					return nil, e
				}
				buf := make([]byte, requestsContentChunk-len(call)-len(part))
				n, e := reader.Read(buf)
				if g := check(); g != nil {
					return nil, g
				}
				if e != nil && !requestsContentTruncated(e) {
					if !requestsContentCodecError(e) {
						return nil, e
					}
					if member == 1 {
						return nil, requestsContentError(e)
					}
					part = nil
					done = true
					break
				}
				part = append(part, buf[:n]...)
				if e == io.EOF {
					ended = true
					break
				}
				if e == io.ErrUnexpectedEOF {
					done = true
					break
				}
				if n == 0 {
					return nil, io.ErrNoProgress
				}
			}
			call = append(call, part...)
			if ended {
				closeReader()
			}
		}
		_, _ = out.Write(call)
	}
	if e := check(); e != nil {
		return nil, e
	}
	return out.Bytes(), nil
}
func requestsContentDeflate(raw []byte, check func() error) ([]byte, error) {
	return requestsContentInflate(raw, false, check)
}
func requestsContentInflate(raw []byte, rawMode bool, check func() error) ([]byte, error) {
	input := requestsContentInput(raw, check)
	var reader io.ReadCloser
	var err error
	if rawMode {
		reader = flate.NewReader(input)
	} else {
		reader, err = zlib.NewReader(input)
	}
	if g := check(); g != nil {
		if reader != nil {
			_ = reader.Close()
		}
		return nil, g
	}
	if err != nil {
		if requestsContentTruncated(err) {
			return []byte{}, nil
		}
		if !requestsContentCodecError(err) {
			return nil, err
		}
		if !rawMode {
			return requestsContentInflate(raw, true, check)
		}
		return nil, requestsContentError(err)
	}
	defer reader.Close()
	var out bytes.Buffer
	firstOutput := false
	for {
		if e := check(); e != nil {
			return nil, e
		}
		call := make([]byte, 0, requestsContentChunk)
		finished := false
		for len(call) < requestsContentChunk {
			buf := make([]byte, requestsContentChunk-len(call))
			n, e := reader.Read(buf)
			if g := check(); g != nil {
				return nil, g
			}
			if e != nil && !requestsContentTruncated(e) {
				if !requestsContentCodecError(e) {
					return nil, e
				}
				if !rawMode && !firstOutput {
					return requestsContentInflate(raw, true, check)
				}
				return nil, requestsContentError(e)
			}
			call = append(call, buf[:n]...)
			if requestsContentTruncated(e) {
				finished = true
				break
			}
			if n == 0 {
				return nil, io.ErrNoProgress
			}
		}
		if len(call) > 0 {
			firstOutput = true
			_, _ = out.Write(call)
		}
		if finished {
			break
		}
	}
	if e := check(); e != nil {
		return nil, e
	}
	return out.Bytes(), nil
}
