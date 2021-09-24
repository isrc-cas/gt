package server

import (
	"bytes"
	"errors"
	"github.com/isrc-cas/gt/bufio"
	"github.com/isrc-cas/gt/predef"
)

var (
	// ErrInvalidHostLength is an error returned when invalid host length was received
	ErrInvalidHostLength = errors.New("invalid host length of http protocol")
	// ErrInvalidHTTPProtocol is an error returned when invalid http protocol was received
	ErrInvalidHTTPProtocol = errors.New("invalid http protocol")
)

type peekHostReader struct {
	reader *bufio.Reader
}

func newPeekHostReader(reader *bufio.Reader) *peekHostReader {
	return &peekHostReader{reader: reader}
}

func (r *peekHostReader) PeekHost() (host []byte, err error) {
	for {
		n := r.reader.Buffered()
		var headers []byte
		headers, err = r.reader.Peek(n)
		if err != nil {
			return nil, err
		}
		s := 0
		for i, b := range headers {
			if b == '\n' {
				if i-s >= 6 {
					if bytes.Equal(headers[s:s+6], []byte("Host: ")) {
						line := bytes.TrimSpace(headers[s:i])
						hl := len(line) - 6
						if hl < 1 || hl > 512 {
							return nil, ErrInvalidHostLength
						}
						host = make([]byte, hl)
						copy(host, line[6:])
						return host, nil
					}
				}
				if len(headers) > i {
					s = i + 1
				}
			}
		}
		if n > predef.MaxEndingOfHostInHTTPHeaders {
			return nil, ErrInvalidHTTPProtocol
		}
		_, err = r.reader.Peek(n + 1)
		if err != nil {
			return
		}
	}
}
