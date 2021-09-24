package server

import (
	"bytes"
	"errors"
)

var (
	// ErrInvalidHost is an error returned when host value is invalid
	ErrInvalidHost = errors.New("invalid host value")
)

func parseIDFromHost(host []byte) (id []byte, err error) {
	i := bytes.Index(host, []byte{'.'})
	if i < 0 {
		err = ErrInvalidHost
		return
	}
	if i+1 >= len(host) {
		err = ErrInvalidHost
		return
	}
	if bytes.Index(host[i+1:], []byte{'.'}) <= 0 {
		err = ErrInvalidHost
		return
	}
	id = host[:i]
	return
}
