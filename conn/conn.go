package conn

import (
	"errors"
	"github.com/isrc-cas/gt/bufio"
	"github.com/isrc-cas/gt/predef"
	"github.com/rs/zerolog"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// ErrInvalidWrite is an error of write operation, number of wrote data is less than passed
var ErrInvalidWrite = errors.New("invalid write")

// Connection is an extended net.Conn
type Connection struct {
	net.Conn
	writeMtx     sync.Mutex
	Logger       zerolog.Logger
	Reader       *bufio.Reader
	WriteTimeout time.Duration
	TasksCount   uint32
	Closing      uint32
}

func (c *Connection) Write(b []byte) (n int, err error) {
	l := len(b)
	c.writeMtx.Lock()
	if c.WriteTimeout > 0 {
		dl := time.Now().Add(c.WriteTimeout)
		err = c.Conn.SetWriteDeadline(dl)
		if err != nil {
			c.writeMtx.Unlock()
			return
		}
	}
	n, err = c.Conn.Write(b)
	c.writeMtx.Unlock()
	if l != n && err == nil {
		err = ErrInvalidWrite
	}
	return
}

// Close closes Connection
func (c *Connection) Close() {
	if atomic.CompareAndSwapUint32(&c.Closing, 0, 1) {
		c.CloseOnce()
	}
}

// CloseOnce closes Connection
func (c *Connection) CloseOnce() {
	err := c.Conn.Close()
	c.Logger.Debug().Err(err).Msg("conn close")
}

// IsClosing tells is the server stopping.
func (c *Connection) IsClosing() (closing bool) {
	return atomic.LoadUint32(&c.Closing) > 0
}

// Shutdown closes Connection gracefully
func (c *Connection) Shutdown() {
	atomic.StoreUint32(&c.Closing, 1)
}

// AddTaskCount increases TaskCount
func (c *Connection) AddTaskCount() uint32 {
	return atomic.AddUint32(&c.TasksCount, 1)
}

// SubTaskCount decreases TaskCount
func (c *Connection) SubTaskCount() uint32 {
	return atomic.AddUint32(&c.TasksCount, ^uint32(0))
}

// GetTaskCount returns TaskCount
func (c *Connection) GetTaskCount() uint32 {
	return atomic.LoadUint32(&c.TasksCount)
}

// Signal is alias type of uint32 for specify signal in the protocol
type Signal = uint32

const (
	// PingSignal is a signal used for ping
	PingSignal Signal = math.MaxUint32
	// CloseSignal is a signal used for close
	CloseSignal Signal = math.MaxUint32 - 1
	// ReadySignal is a signal used for ready
	ReadySignal Signal = math.MaxUint32 - 2
	// PreservedSignal is a signal used for preserved signals
	PreservedSignal Signal = math.MaxUint32 - 3000
)

var (
	pingBytes  = []byte{0xFF, 0xFF, 0xFF, 0xFF}
	closeBytes = []byte{0xFF, 0xFF, 0xFF, 0xFE}
	readyBytes = []byte{0xFF, 0xFF, 0xFF, 0xFD}
)

// SendPingSignal sends ping signal to the other side
func (c *Connection) SendPingSignal() (err error) {
	_, err = c.Write(pingBytes)
	return
}

// SendCloseSignal sends close signal to the other side
func (c *Connection) SendCloseSignal() {
	_, err := c.Write(closeBytes)
	if predef.Debug {
		if err != nil {
			c.Logger.Trace().Err(err).Msg("failed to send close signal")
		}
	}
}

// SendReadySignal sends ready signal to the other side
func (c *Connection) SendReadySignal() (err error) {
	_, err = c.Write(readyBytes)
	return
}
