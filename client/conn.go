package client

import (
	"context"
	"errors"
	"github.com/pion/webrtc/v3"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/isrc-cas/gt/bufio"
	connection "github.com/isrc-cas/gt/conn"
	"github.com/isrc-cas/gt/pool"
	"github.com/isrc-cas/gt/predef"
)

type conn struct {
	connection.Connection
	client         *Client
	tasks          map[uint32]*httpTask
	tasksRWMtx     sync.RWMutex
	peerTasks      map[uint32]*peerTask
	peerTasksRWMtx sync.RWMutex
	stuns          []string
}

func newConn(c net.Conn, client *Client) *conn {
	nc := &conn{
		Connection: connection.Connection{
			Conn:         c,
			Reader:       pool.GetReader(c),
			WriteTimeout: client.config.RemoteTimeout,
		},
		client:    client,
		tasks:     make(map[uint32]*httpTask, 100),
		peerTasks: make(map[uint32]*peerTask),
	}
	nc.Logger = client.Logger.With().
		Str("clientConn", strconv.FormatUint(uint64(uintptr(unsafe.Pointer(nc))), 16)).
		Logger()
	return nc
}

func (c *conn) init() (err error) {
	buf := c.Connection.Reader.GetBuf()
	bufIndex := 0

	buf[bufIndex] = predef.VersionFirst
	bufIndex++
	buf[bufIndex] = 0x01
	bufIndex++

	id := c.client.config.ID
	buf[bufIndex] = byte(len(id))
	bufIndex++
	idLen := copy(buf[bufIndex:], id)
	bufIndex += idLen

	secret := c.client.config.Secret
	buf[bufIndex] = byte(len(secret))
	bufIndex++
	secretLen := copy(buf[bufIndex:], secret)
	bufIndex += secretLen

	// option
	buf[bufIndex] = 0x00
	bufIndex++

	_, err = c.Conn.Write(buf[:bufIndex])

	return
}

func (c *conn) IsTimeout(e error) (result bool) {
	if ne, ok := e.(*net.OpError); ok && ne.Timeout() {
		err := c.Connection.SendPingSignal()
		if err == nil {
			result = true
			return
		}
		c.Logger.Debug().Err(err).Msg("failed to send ping signal")
	}
	return
}

func (c *conn) Close() {
	if !atomic.CompareAndSwapUint32(&c.Closing, 0, 1) {
		return
	}
	c.tasksRWMtx.RLock()
	for _, task := range c.tasks {
		task.Close()
	}
	c.tasksRWMtx.RUnlock()
	c.Connection.CloseOnce()
	pool.PutReader(c.Reader)
}

func (c *conn) readLoop() {
	var err error
	var pings int
	defer func() {
		c.client.removeTunnel(c)
		if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
			err = nil
		}
		c.Close()
		c.Logger.Info().Err(err).Int("pings", pings).Msg("tunnel closed")
		c.onTunnelClose()
	}()

	r := &bufio.LimitedReader{}
	r.Reader = c.Reader
	for pings <= 1 {
		if c.client.config.RemoteTimeout > 0 {
			dl := time.Now().Add(c.client.config.RemoteTimeout)
			err = c.Conn.SetReadDeadline(dl)
			if err != nil {
				return
			}
		}
		var peekBytes []byte
		peekBytes, err = c.Reader.Peek(4)
		if err != nil {
			if c.IsTimeout(err) {
				pings++
				if predef.Debug {
					c.Logger.Trace().Int("pings", pings).Msg("sent ping")
				}
				continue
			}
			return
		}
		id := uint32(peekBytes[3]) | uint32(peekBytes[2])<<8 | uint32(peekBytes[1])<<16 | uint32(peekBytes[0])<<24
		_, err = c.Reader.Discard(4)
		if err != nil {
			return
		}
		switch id {
		case connection.PingSignal:
			pings--
			continue
		case connection.CloseSignal:
			c.Logger.Debug().Msg("read close signal")
			return
		case connection.ReadySignal:
			c.client.addTunnel(c)
			c.Logger.Info().Msg("tunnel started")
			continue
		case connection.ErrorSignal:
			peekBytes, err = c.Reader.Peek(2)
			if err != nil {
				return
			}
			errCode := uint16(peekBytes[1]) | uint16(peekBytes[0])<<8
			c.Logger.Info().Err(connection.Error(errCode)).Msg("read error signal")
			return
		}
		peekBytes, err = c.Reader.Peek(2)
		if err != nil {
			return
		}
		op := uint16(peekBytes[1]) | uint16(peekBytes[0])<<8
		_, err = c.Reader.Discard(2)
		if err != nil {
			return
		}
		switch op {
		case predef.Data:
			peekBytes, err = c.Reader.Peek(4)
			if err != nil {
				return
			}
			l := uint32(peekBytes[3]) | uint32(peekBytes[2])<<8 | uint32(peekBytes[1])<<16 | uint32(peekBytes[0])<<24
			_, err = c.Reader.Discard(4)
			if err != nil {
				return
			}
			r.N = int64(l)
			rErr, wErr := c.processData(id, r)
			if rErr != nil {
				err = wErr
				if !errors.Is(err, net.ErrClosed) {
					c.Logger.Warn().Err(err).Msg("failed to read data in processData")
				}
				return
			}
			if r.N > 0 {
				if !predef.Debug {
					_, err = r.Discard(int(r.N))
					if err != nil {
						return
					}
				} else {
					event := c.Logger.Debug().Int("N", int(r.N))
					all, e := io.ReadAll(r)
					event.Hex("remaining", all).Err(e).Msg("discarding")
				}
			}
			if wErr != nil {
				if !errors.Is(wErr, net.ErrClosed) {
					c.Logger.Warn().Err(wErr).Msg("failed to write data in processData")
				}
				continue
			}
		case predef.Close:
			c.tasksRWMtx.RLock()
			t, ok := c.tasks[id]
			c.tasksRWMtx.RUnlock()
			if ok {
				t.Close()
			}
		}

	}
}

func (c *conn) dial() (task *httpTask, err error) {
	u, err := url.Parse(c.client.config.Local)
	if err != nil {
		return
	}
	addr := u.Host
	switch u.Scheme {
	case "https":
		if strings.Index(addr, ":") < 0 {
			addr = addr + ":443"
		}
	case "http":
		if strings.Index(addr, ":") < 0 {
			addr = addr + ":80"
		}
	}
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return
	}
	task = newHTTPTask(conn)
	if c.client.config.UseLocalAsHTTPHost {
		err = task.setHost(u.Host)
	}
	return
}

func (c *conn) processData(id uint32, r *bufio.LimitedReader) (readErr, writeErr error) {
	peekBytes, readErr := r.Peek(2)
	if readErr != nil {
		return
	}
	// first 2 bytes of p2p sdp request is "X1"(0x5831)
	isP2P := (uint16(peekBytes[1]) | uint16(peekBytes[0])<<8) == 0x5831
	c.peerTasksRWMtx.RLock()
	p2pTask, ok := c.peerTasks[id]
	c.peerTasksRWMtx.RUnlock()
	if isP2P || ok {
		if len(c.stuns) < 1 {
			respAndClose(id, c, [][]byte{
				[]byte("HTTP/1.1 403 Forbidden\r\nConnection: Closed\r\n\r\n"),
			})
			return
		}
		c.processP2P(id, r, p2pTask, ok)
		return
	}

	c.tasksRWMtx.RLock()
	t, ok := c.tasks[id]
	c.tasksRWMtx.RUnlock()
	if !ok {
		c.tasksRWMtx.Lock()
		t, ok = c.tasks[id]
		if !ok {
			t, writeErr = c.dial()
			if writeErr != nil {
				c.tasksRWMtx.Unlock()
				return
			}
			c.tasks[id] = t
			c.tasksRWMtx.Unlock()
			t.Logger = c.Logger.With().
				Uint32("task", id).
				Logger()
			t.Logger.Info().Msg("task started")
			go t.process(id, c)
		} else {
			c.tasksRWMtx.Unlock()
		}
	}
	_, err := r.WriteTo(t)
	if err != nil {
		if oe, ok := err.(*net.OpError); ok {
			switch oe.Op {
			case "write":
				writeErr = err
			default:
				readErr = err
			}
		} else {
			readErr = err
		}
	}
	if c.client.config.LocalTimeout > 0 {
		dl := time.Now().Add(c.client.config.LocalTimeout)
		writeErr = t.conn.SetReadDeadline(dl)
		if writeErr != nil {
			return
		}
	}
	return
}

func (c *conn) processP2P(id uint32, r *bufio.LimitedReader, t *peerTask, ok bool) {
	if !ok {
		c.peerTasksRWMtx.Lock()
		t, ok = c.peerTasks[id]
		if !ok {
			t = &peerTask{}
			c.peerTasks[id] = t
			c.peerTasksRWMtx.Unlock()
			t.data = pool.BytesPool.Get().([]byte)
			t.ctx, t.ctxDone = context.WithTimeout(context.Background(), 60*time.Second)
			t.candidateOutChan = make(chan webrtc.ICECandidateInit)
			t.Logger = c.Logger.With().
				Uint32("peerTask", id).
				Logger()
			t.Logger.Info().Msg("peer task started")
		} else {
			c.peerTasksRWMtx.Unlock()
		}
	}
	t.process(id, c, r)
}
