//go:build !release
// +build !release

package client

import (
	"sync"
	"sync/atomic"

	"github.com/isrc-cas/gt/logger"
)

// Client is a network agent client.
type Client struct {
	config       Config
	Logger       logger.Logger
	initConnMtx  sync.Mutex
	closing      uint32
	tunnels      map[*conn]struct{}
	tunnelsRWMtx sync.RWMutex
	tunnelsCond  *sync.Cond

	// test purpose only
	OnTunnelClose atomic.Value
}

func (c *conn) onTunnelClose() {
	cb := c.client.OnTunnelClose.Load()
	if cb != nil {
		if cb, ok := cb.(func()); ok {
			cb()
		}
	}
}
