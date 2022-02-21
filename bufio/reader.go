package bufio

import (
	"bytes"
	"io"
)

// LimitedReader implements a limited io.WriterTo.
type LimitedReader struct {
	*Reader
	N int64
}

// Read reads data into p.
// It returns the number of bytes read into p.
// The bytes are taken from at most one Read on the underlying Reader,
// hence n may be less than len(p).
// To read exactly len(p) bytes, use io.ReadFull(b, p).
// At EOF, the count will be zero and err will be io.EOF.
func (b *LimitedReader) Read(p []byte) (n int, err error) {
	if b.N <= 0 {
		err = io.EOF
		return
	}
	if int64(len(p)) > b.N {
		p = p[:b.N]
	}
	n, err = b.Reader.Read(p)
	b.N -= int64(n)
	return
}

// WriteTo implements io.WriterTo.
// This may make multiple calls to the Read method of the underlying Reader.
// If the underlying reader supports the WriteTo method,
// this calls the underlying WriteTo without buffering.
func (b *LimitedReader) WriteTo(w io.Writer) (n int64, err error) {
	if b.N <= 0 {
		return
	}
	n, err = b.writeBuf(w)
	if b.N <= 0 || err != nil {
		return
	}

	if b.w-b.r < len(b.buf) {
		b.fill() // buffer not full
	}

	for b.r < b.w {
		// b.r < b.w => buffer is not empty
		m, err := b.writeBuf(w)
		n += m
		if b.N <= 0 || err != nil {
			return n, err
		}
		b.fill() // buffer is empty
	}

	if b.err == io.EOF {
		b.err = nil
	}

	return n, b.readErr()
}

// writeBuf writes the Reader's buffer to the writer.
func (b *LimitedReader) writeBuf(w io.Writer) (n int64, err error) {
	l := int64(b.w)
	if int64(b.w-b.r) > b.N {
		l = b.N + int64(b.r)
	}
	nw, err := w.Write(b.buf[b.r:l])
	if nw < 0 {
		panic(errNegativeWrite)
	}
	b.r += nw
	n = int64(nw)
	b.N -= n
	return
}

// NewReaderWithBuf returns a new Reader using the specified buffer.
func NewReaderWithBuf(buf []byte) *Reader {
	r := new(Reader)
	r.reset(buf, nil)
	return r
}

// GetBuf returns the underlying buffer.
func (b *Reader) GetBuf() []byte {
	return b.buf
}

// ReadSlice reads until the first occurrence of delim in the input,
// returning a slice pointing at the bytes in the buffer.
// The bytes stop being valid at the next read.
// If ReadSlice encounters an error before finding a delimiter,
// it returns all the data in the buffer and the error itself (often io.EOF).
// ReadSlice fails with error ErrBufferFull if the buffer fills without a delim.
// Because the data returned from ReadSlice will be overwritten
// by the next I/O operation, most clients should use
// ReadBytes or ReadString instead.
// ReadSlice returns err != nil if and only if line does not end in delim.
func (b *LimitedReader) ReadSlice(delim byte) (line []byte, err error) {
	s := 0 // search start index
	for {
		l := int64(b.w)
		if int64(b.w-b.r) > b.N {
			l = b.N + int64(b.r)
		}
		// Search buffer.
		if i := bytes.IndexByte(b.buf[b.r+s:l], delim); i >= 0 {
			i += s
			line = b.buf[b.r : b.r+i+1]
			b.r += i + 1
			break
		}

		// Pending error?
		if b.err != nil {
			line = b.buf[b.r:l]
			b.r = int(l)
			err = b.readErr()
			break
		}

		s = int(l) - b.r // do not rescan area we scanned before
		if int64(s) >= b.N {
			line = b.buf[b.r:l]
			b.r = int(l)
			err = io.EOF
			break
		}

		// Buffer full?
		if b.Buffered() >= len(b.buf) {
			b.r = b.w
			line = b.buf
			err = ErrBufferFull
			break
		}

		b.fill() // buffer is not full
	}

	// Handle last byte, if any.
	if i := len(line) - 1; i >= 0 {
		b.lastByte = int(line[i])
		b.lastRuneSize = -1
	}

	b.N -= int64(len(line))

	return
}
