package bufio

import (
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
