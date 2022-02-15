//go:build release
// +build release

package client

import (
	"sync"

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
}

func (c *conn) onTunnelClose() {
}
