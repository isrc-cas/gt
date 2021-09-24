package client

import (
	"bytes"
	"encoding/binary"
	"errors"
	"github.com/isrc-cas/gt/pool"
	"github.com/isrc-cas/gt/predef"
	"github.com/rs/zerolog"
	"net"
	"sync/atomic"
	"time"
)

var (
	// ErrHostIsTooLong is an error returned when host is too long
	ErrHostIsTooLong = errors.New("host is too long")

	host = []byte("Host:")
)

type httpTask struct {
	conn     net.Conn
	buf      []byte
	tempBuf  *bytes.Buffer
	Logger   zerolog.Logger
	skipping bool
	passing  bool
	closing  uint32
}

func newHTTPTask(c net.Conn) (t *httpTask) {
	t = &httpTask{
		conn: c,
		buf:  pool.BytesPool.Get().([]byte),
	}
	return
}

func (t *httpTask) setHost(host string) (err error) {
	if len(host) > 200 {
		return ErrHostIsTooLong
	}
	n := copy(t.buf, "Host: ")
	n += copy(t.buf[n:], host)
	n += copy(t.buf[n:], "\r\n")
	t.tempBuf = bytes.NewBuffer(t.buf[n:][:0])
	t.buf = t.buf[:n]
	return
}

func (t *httpTask) Write(p []byte) (n int, err error) {
	if t.tempBuf == nil {
		return t.conn.Write(p)
	} else if t.skipping {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			return len(p), nil
		}
		t.skipping = false
		t.tempBuf = nil
		if len(p) <= i+1 {
			return len(p), nil
		}
		if predef.Debug {
			t.Logger.Debug().Bytes("data", p[i+1:]).Msg("write")
		}
		n, err = t.conn.Write(p[i+1:])
		n += i + 1
		return
	} else if t.passing {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			if predef.Debug {
				t.Logger.Debug().Bytes("data", p).Msg("write")
			}
			return t.conn.Write(p)
		}
		t.passing = false
		if predef.Debug {
			t.Logger.Debug().Bytes("data", p[:i+1]).Msg("write")
		}
		n, err = t.conn.Write(p[:i+1])
		if err != nil {
			return
		}
		p = p[i+1:]
		if len(p) <= 0 {
			return
		}
	}

	var nw int
	var updated bool
	if t.tempBuf != nil {
		if len(p)+t.tempBuf.Len() < 5 {
			nw, err = t.tempBuf.Write(p)
			n += nw
			return
		}

		if t.tempBuf.Len() > 0 {
			nw, err = t.tempBuf.Write(p)
			n += nw
			if err != nil {
				return
			}
			p = t.tempBuf.Bytes()
			t.tempBuf.Reset()
			updated = true
		}
	}

	s := 0
	for len(p[s:]) >= 1 {
		switch t.isGoodToWrite(p[s:]) {
		case good:
			i := bytes.IndexByte(p[s:], '\n')
			if i < 0 {
				t.passing = true
				if predef.Debug {
					t.Logger.Debug().Bytes("data", p[s:]).Msg("write")
				}
				nw, err = t.conn.Write(p[s:])
				if !updated {
					n += nw
				}
				return
			}
			if predef.Debug {
				t.Logger.Debug().Bytes("data", p[s:s+i+1]).Msg("write")
			}
			nw, err = t.conn.Write(p[s : s+i+1])
			if !updated {
				n += nw
			}
			if err != nil {
				return
			}
			s += i + 1
		case unsure:
			nw, err = t.tempBuf.Write(p[s:])
			if !updated {
				n += nw
			}
			return
		case replace:
			if predef.Debug {
				t.Logger.Debug().Bytes("data", t.buf).Msg("write")
			}
			_, err = t.conn.Write(t.buf)
			t.buf = t.buf[:cap(t.buf)]
			if err != nil {
				return
			}
			i := bytes.IndexByte(p[s:], '\n')
			if i < 0 {
				t.skipping = true
				nw = len(p[s:])
				if !updated {
					n += nw
				}
				return
			}
			s += i + 1
			if !updated {
				n += i + 1
			}
			t.tempBuf = nil
			if len(p[s:]) > 0 {
				if predef.Debug {
					t.Logger.Debug().Bytes("data", p[s:]).Msg("write")
				}
				nw, err = t.conn.Write(p[s:])
				if !updated {
					n += nw
				}
			}
			return
		}
	}
	return
}

const (
	good = iota
	unsure
	replace
)

func (t *httpTask) isGoodToWrite(p []byte) int {
	l := len(p)
	if l > 5 {
		l = 5
	}
	if bytes.Equal(p[:l], host[:l]) {
		if l < 5 {
			return unsure
		}
		return replace
	}
	return good
}

func (t *httpTask) Close() {
	if !atomic.CompareAndSwapUint32(&t.closing, 0, 1) {
		return
	}
	pool.BytesPool.Put(t.buf[:cap(t.buf)])
	err := t.conn.Close()
	t.Logger.Info().Err(err).Msg("task close")
}

func (t *httpTask) process(id uint32, c *conn) {
	atomic.AddUint32(&c.TasksCount, 1)
	var rerr error
	var werr error
	buf := pool.BytesPool.Get().([]byte)
	defer func() {
		if werr == nil {
			binary.BigEndian.PutUint32(buf[0:], id)
			binary.BigEndian.PutUint16(buf[4:], predef.Close)
			_, werr = c.Write(buf[:6])
		}
		pool.BytesPool.Put(buf)
		if rerr != nil || werr != nil {
			t.Logger.Debug().AnErr("read err", rerr).AnErr("write err", werr).Msg("process err")
		}
		c.tasksRWMtx.Lock()
		delete(c.tasks, id)
		c.tasksRWMtx.Unlock()
		t.Close()
		if atomic.AddUint32(&c.TasksCount, ^uint32(0)) == 0 && c.IsClosing() {
			c.SendCloseSignal()
			c.Close()
		}
	}()
	for {
		binary.BigEndian.PutUint32(buf[0:], id)
		binary.BigEndian.PutUint16(buf[4:], predef.Data)
		if c.client.config.LocalTimeout > 0 {
			dl := time.Now().Add(c.client.config.LocalTimeout)
			rerr = t.conn.SetReadDeadline(dl)
			if rerr != nil {
				return
			}
		}
		var l int
		l, rerr = t.conn.Read(buf[10:])
		if l > 0 {
			binary.BigEndian.PutUint32(buf[6:], uint32(l))
			l += 10

			if predef.Debug {
				c.Logger.Trace().Hex("data", buf[:l]).Msg("write")
			}
			_, werr = c.Write(buf[:l])
			if werr != nil {
				return
			}
		}
		if rerr != nil {
			return
		}
	}
}
