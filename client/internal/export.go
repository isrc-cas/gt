package internal

import (
	"github.com/isrc-cas/gt/bufio"
)

type ChunkedReader = chunkedReader

func (cr *chunkedReader) SetReader(r *bufio.LimitedReader) {
	cr.r = r
	cr.err = nil
}
