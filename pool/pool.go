package pool

import (
	"github.com/isrc-cas/gt/bufio"
	"io"
	"sync"
)

const (
	// MaxBufferSize max tunnel message size
	MaxBufferSize = 4 * 1024
)

// BytesPool is a pool of []byte that cap and len are MaxBufferSize
var BytesPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, MaxBufferSize)
	},
}

var readersPool = sync.Pool{
	New: func() interface{} {
		return bufio.NewReaderWithBuf(BytesPool.Get().([]byte))
	},
}

// GetReader returns a *bufio.Reader in the pool
func GetReader(reader io.Reader) *bufio.Reader {
	r := readersPool.Get().(*bufio.Reader)
	r.Reset(reader)
	return r
}

// PutReader puts the *bufio.Reader in the pool
func PutReader(r *bufio.Reader) {
	readersPool.Put(r)
}
