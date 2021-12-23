package server

import (
	"bytes"
	"encoding/binary"
	"errors"
	"github.com/isrc-cas/gt/bufio"
	"github.com/isrc-cas/gt/predef"
	"io"
)

var (
	// ErrInvalidHeaderLength is an error returned when invalid header length was received
	ErrInvalidHeaderLength = errors.New("invalid header length of http protocol")
	// ErrInvalidHTTPProtocol is an error returned when invalid http protocol was received
	ErrInvalidHTTPProtocol = errors.New("invalid http protocol")
	// ErrInvalidHost is an error returned when host value is invalid
	ErrInvalidHost = errors.New("invalid host value")
)

const (
	headerHostPrefix = "Host: "
	endOfHeaders     = 0x0D0A0D0A
)

func peekHost(reader *bufio.Reader) (value []byte, err error) {
	return peekHeader(reader, headerHostPrefix)
}

func peekHeader(reader *bufio.Reader, target string) (value []byte, err error) {
	for {
		n := reader.Buffered()
		var headers []byte
		headers, err = reader.Peek(n)
		if err != nil {
			return nil, err
		}
		s := 0
		targetLen := len(target)
		for i, b := range headers {
			if b == '\n' {
				if i-s >= targetLen {
					if bytes.Equal(headers[s:s+targetLen], []byte(target)) {
						line := bytes.TrimSpace(headers[s+targetLen : i])
						hl := len(line)
						if hl < 1 || hl > 512 {
							return nil, ErrInvalidHeaderLength
						}
						value = make([]byte, hl)
						copy(value, line)
						return value, nil
					}
				}
				if i >= 3 && binary.BigEndian.Uint32(headers[i-3:i+1]) == endOfHeaders {
					return nil, io.EOF
				}
				if len(headers) > i {
					s = i + 1
				}
			}
		}
		if n > predef.MaxHTTPHeaderSize {
			return nil, ErrInvalidHTTPProtocol
		}
		_, err = reader.Peek(n + 1)
		if err != nil {
			return
		}
	}
}

func parseIDFromHost(host []byte) (id []byte, err error) {
	i := bytes.IndexByte(host, '.')
	if i < 0 {
		err = ErrInvalidHost
		return
	}
	if i+1 >= len(host) {
		err = ErrInvalidHost
		return
	}
	if bytes.IndexByte(host[i+1:], '.') <= 0 {
		err = ErrInvalidHost
		return
	}
	id = host[:i]
	return
}
