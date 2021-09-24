package server

import (
	"encoding/binary"
	"errors"
	"github.com/isrc-cas/gt/bufio"
	connection "github.com/isrc-cas/gt/conn"
	"github.com/isrc-cas/gt/pool"
	"github.com/isrc-cas/gt/predef"
	"io"
	"io/ioutil"
	"net"
	"strconv"
	"sync/atomic"
	"time"
	"unsafe"
)

var (
	// ErrInvalidID is an error returned when id is invalid
	ErrInvalidID = errors.New("invalid id")
	// ErrIDNotFound is an error returned when id is not in the url
	ErrIDNotFound = errors.New("id not found")
)

type conn struct {
	connection.Connection
	server *Server
}

func newConn(c net.Conn, s *Server) *conn {
	nc := &conn{
		Connection: connection.Connection{
			Conn:         c,
			WriteTimeout: s.config.Timeout,
		},
		server: s,
	}
	nc.Logger = s.logger.With().
		Str("serverConn", strconv.FormatUint(uint64(uintptr(unsafe.Pointer(nc))), 16)).
		Str("ip", c.RemoteAddr().String()).
		Logger()
	nc.Logger.Info().Msg("accepted")
	return nc
}

func (c *conn) handle() {
	startTime := time.Now()
	reader := pool.GetReader(c.Conn)
	c.Reader = reader
	defer func() {
		c.Close()
		pool.PutReader(reader)
		endTime := time.Now()
		if !predef.Debug {
			if e := recover(); e != nil {
				c.Logger.Warn().Msgf("recovered panic: %#v", e)
			}
		}
		c.Logger.Info().Dur("cost", endTime.Sub(startTime)).Msg("closed")
	}()
	if c.server.config.Timeout > 0 {
		dl := startTime.Add(c.server.config.Timeout)
		err := c.SetReadDeadline(dl)
		if err != nil {
			c.Logger.Debug().Err(err).Msg("handle set deadline failed")
			return
		}
	}

	version, err := reader.Peek(2)
	if err != nil {
		if !errors.Is(err, io.EOF) {
			c.Logger.Warn().Err(err).Msg("failed to peek version field")
		}
		return
	}
	if version[0] == predef.VersionFirst {
		switch version[1] {
		case 0x01:
			_, err = reader.Discard(2)
			if err != nil {
				c.Logger.Warn().Err(err).Msg("failed to discard version field")
				return
			}
			c.handleTunnel()
			return
		}
	}
	c.handleHTTP()
	return
}

func (c *conn) handleHTTP() {
	var err error
	var host []byte
	defer func() {
		if err != nil {
			c.Logger.Error().Bytes("host", host).Err(err).Msg("handleHTTP")
		}
		atomic.AddUint64(&c.server.served, 1)
	}()
	r := newPeekHostReader(c.Reader)
	host, err = r.PeekHost()
	if err != nil {
		return
	}
	if len(host) < 1 {
		err = ErrInvalidHTTPProtocol
		return
	}
	id, err := parseIDFromHost(host)
	if err != nil {
		return
	}
	if len(id) < predef.MinIDSize {
		err = ErrInvalidID
		return
	}
	client, ok := c.server.getClient(string(id))
	if ok {
		client.process(c)
	} else {
		err = ErrIDNotFound
	}
}

func (c *conn) handleTunnel() {
	reader := c.Reader

	// 读取 id 相关
	idLen, err := reader.ReadByte()
	if err != nil {
		c.Logger.Error().Err(err).Msg("failed to read id len")
		return
	}
	if idLen < predef.MinIDSize && idLen > predef.MaxIDSize {
		c.Logger.Error().Err(err).Msg("invalid id len")
		return
	}
	id, err := reader.Peek(int(idLen))
	if err != nil {
		c.Logger.Error().Err(err).Msg("failed to read id")
		return
	}
	idStr := string(id)
	_, err = reader.Discard(int(idLen))
	if err != nil {
		c.Logger.Error().Err(err).Msg("failed to discard id")
		return
	}

	// 读取 secret 相关
	secretLen, err := reader.ReadByte()
	if err != nil {
		c.Logger.Error().Err(err).Msg("failed to read secret len")
		return
	}
	if secretLen < predef.MinSecretSize && secretLen > predef.MaxSecretSize {
		c.Logger.Error().Err(err).Msg("invalid secret len")
		return
	}
	secret, err := reader.Peek(int(secretLen))
	if err != nil {
		c.Logger.Error().Err(err).Msg("failed to read secret")
		return
	}
	secretStr := string(secret)
	_, err = reader.Discard(int(secretLen))
	if err != nil {
		c.Logger.Error().Err(err).Msg("failed to discard secret")
		return
	}

	// 验证 id secret
	verifyOk := false
	ud, err := c.server.config.Users.get(idStr)
	if err == nil && ud.Secret == secretStr {
		verifyOk = true
	}
	// 如果 Users 里面没有就从 apiServer 里面找
	if !verifyOk && idStr == c.server.apiServer.ID && secretStr == c.server.apiServer.Secret {
		verifyOk = true
	}
	if !verifyOk {
		c.Logger.Error().Err(err).Msg("failed to verify id and secret")
		// TODO 返回错误信息给客户端
		return
	}

	optionByte, err := reader.ReadByte()
	if err != nil {
		c.Logger.Error().Err(err).Msg("failed to read optionByte")
		return
	}
	_ = optionByte

	var cli *client
	var ok bool

	for i := 0; i < 5; i++ {
		var exists bool
		cli, exists = c.server.getOrCreateClient(idStr, newClient)
		if !exists {
			cli.init(idStr)
		}

		ok = cli.addTunnel(c)
		if ok {
			break
		}
	}
	if !ok || cli == nil {
		c.Logger.Error().Msg("failed to create client")
		return
	}
	defer cli.removeTunnel(c)
	atomic.AddUint64(&c.server.tunneling, 1)
	c.readLoop(cli)
}

func (c *conn) GetTasksCount() uint32 {
	return atomic.LoadUint32(&c.TasksCount)
}

func (c *conn) readLoop(cli *client) {
	var err error
	defer func() {
		if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
			err = nil
		}
		c.Logger.Debug().Err(err).Msg("readLoop ended")
	}()
	err = c.SendReadySignal()
	if err != nil {
		return
	}
	r := &bufio.LimitedReader{}
	for {
		if c.server.config.Timeout > 0 {
			dl := time.Now().Add(c.server.config.Timeout)
			err = c.SetReadDeadline(dl)
			if err != nil {
				return
			}
		}
		var peekBytes []byte
		peekBytes, err = c.Reader.Peek(4)
		if err != nil {
			return
		}
		id := uint32(peekBytes[3]) | uint32(peekBytes[2])<<8 | uint32(peekBytes[1])<<16 | uint32(peekBytes[0])<<24
		_, err = c.Reader.Discard(4)
		if err != nil {
			return
		}
		switch id {
		case connection.PingSignal:
			if predef.Debug {
				c.Logger.Trace().Msg("readLoop read ping signal")
			}
			err = c.SendPingSignal()
			if err != nil {
				c.Logger.Debug().Err(err).Msg("readLoop resp ping signal failed")
				return
			}
			continue
		case connection.CloseSignal:
			if predef.Debug {
				c.Logger.Trace().Msg("readLoop read close signal")
			}
			return
		}
		if predef.Debug {
			c.Logger.Trace().Uint32("id", id).Msg("readLoop read id")
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
		task, ok := cli.getTask(id)
		switch op {
		case predef.Data:
			if predef.Debug {
				c.Logger.Trace().Uint32("id", id).Msg("read data op")
			}
			peekBytes, err = c.Reader.Peek(4)
			if err != nil {
				return
			}
			l := uint32(peekBytes[3]) | uint32(peekBytes[2])<<8 | uint32(peekBytes[1])<<16 | uint32(peekBytes[0])<<24
			_, err = c.Reader.Discard(4)
			if err != nil {
				return
			}
			if predef.Debug {
				c.Logger.Trace().Uint32("len", l).Msg("readLoop read len")
			}
			r.Reader = c.Reader
			r.N = int64(l)
			if ok {
				if !predef.Debug {
					_, err = r.WriteTo(task)
				} else {
					err = func() error {
						buf := pool.BytesPool.Get().([]byte)
						defer pool.BytesPool.Put(buf)
						for {
							n, re := r.Read(buf)
							if n > 0 {
								task.Logger.Trace().Hex("data", buf[:n]).Msg("resp")
								wn, we := task.Write(buf[:n])
								if we != nil {
									return we
								}
								if wn != n {
									return connection.ErrInvalidWrite
								}
							}
							if re != nil {
								if re == io.EOF {
									return nil
								}
								return re
							}
							if r.N <= 0 {
								return nil
							}
						}
					}()
				}
				if err != nil {
					c.Logger.Debug().Err(err).Msg("remote req resp writer closed")
					task.Close()
				}
				continue
			}
			bs, err := ioutil.ReadAll(r)
			c.Logger.Trace().Uint16("op", op).Hex("content", bs).Err(err).Uint32("id", id).Msg("orphan resp")
		case predef.Close:
			if predef.Debug {
				c.Logger.Trace().Uint32("id", id).Msg("read close op")
			}
			if ok {
				task.Close()
			}
		}
	}
}

func (c *conn) process(id uint32, task *conn) {
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
			c.Logger.Debug().AnErr("read err", rerr).AnErr("write err", werr).Msg("process err")
		}
		if atomic.AddUint32(&c.TasksCount, ^uint32(0)) == 0 && c.IsClosing() {
			c.SendCloseSignal()
			c.Close()
		}
	}()
	for {
		binary.BigEndian.PutUint32(buf[0:], id)
		binary.BigEndian.PutUint16(buf[4:], predef.Data)
		if c.server.config.Timeout > 0 {
			dl := time.Now().Add(c.server.config.Timeout)
			rerr = task.SetReadDeadline(dl)
			if rerr != nil {
				return
			}
		}
		var l int
		l, rerr = task.Reader.Read(buf[10:])
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
