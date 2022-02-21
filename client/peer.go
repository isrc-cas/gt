package client

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"github.com/isrc-cas/gt/client/internal"
	"io"
	"strconv"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/isrc-cas/gt/bufio"
	"github.com/isrc-cas/gt/pool"
	"github.com/isrc-cas/gt/predef"
	"github.com/pion/webrtc/v3"
	"github.com/rs/zerolog"
)

type peerTask struct {
	Logger           zerolog.Logger
	Reader           internal.ChunkedReader
	closing          uint32
	initDone         bool
	state            uint8
	dataLen          [2]byte
	n                int
	data             []byte
	conn             *webrtc.PeerConnection
	ctx              context.Context
	ctxDone          context.CancelFunc
	candidateOutChan chan webrtc.ICECandidateInit
}

func (t *peerTask) initSDP(c *conn, done context.CancelFunc) (sdp []byte, err error) {
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: c.stuns,
			},
		},
	}

	t.conn, err = webrtc.NewPeerConnection(config)
	if err != nil {
		return
	}
	pConnLogger := t.Logger.With().
		Str("peerConn", strconv.FormatUint(uint64(uintptr(unsafe.Pointer(t.conn))), 16)).
		Logger()

	t.conn.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		pConnLogger.Info().Str("state", s.String()).Msg("p2p conn state changed")
	})
	t.conn.OnICEConnectionStateChange(func(s webrtc.ICEConnectionState) {
		pConnLogger.Info().Str("state", s.String()).Msg("p2p conn ICE state changed")
	})

	t.conn.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			done()
			close(t.candidateOutChan)
		} else {
			t.candidateOutChan <- candidate.ToJSON()
		}
	})

	t.conn.OnDataChannel(func(d *webrtc.DataChannel) {
		pConnLogger.Info().Str("label", d.Label()).Uint16("id", *d.ID()).Msg("new data channel")
		d.OnOpen(func() {
			pConnLogger.Info().Str("label", d.Label()).Uint16("id", *d.ID()).Msg("data channel on open")
		})

		d.OnMessage(func(msg webrtc.DataChannelMessage) {
			pConnLogger.Info().
				Str("label", d.Label()).Uint16("id", *d.ID()).
				Bool("string", msg.IsString).
				Hex("data", msg.Data).Str("text", string(msg.Data)).
				Msg("data channel on msg")
			err := d.Send(msg.Data)
			if err != nil {
				pConnLogger.Error().Err(err).Msg("failed to send")
			}
		})
	})

	offer := webrtc.SessionDescription{}
	err = json.Unmarshal(t.data[:t.n], &offer)
	if err != nil {
		return
	}

	err = t.conn.SetRemoteDescription(offer)
	if err != nil {
		return
	}

	answer, err := t.conn.CreateAnswer(nil)
	if err != nil {
		return
	}

	err = t.conn.SetLocalDescription(answer)
	if err != nil {
		return
	}
	sdp, err = json.Marshal(*t.conn.LocalDescription())
	return
}

func (t *peerTask) skipHTTPHeader(r *bufio.LimitedReader) (ok bool, err error) {
	defer func() {
		t.Logger.Info().Err(err).Msg("skipHTTPHeader done")
	}()
	for {
		var line []byte
		line, err = r.ReadSlice('\n')
		if err != nil {
			if err == bufio.ErrBufferFull {
				err = errors.New("http line is too long")
			}
			return
		}

		// 跳过http headers
		if t.state == skipHTTPHeader {
			if len(line) == 2 && line[0] == '\r' && line[1] == '\n' {
				ok = true
				return
			}
		}
	}
}

func (t *peerTask) Close() {
	if !atomic.CompareAndSwapUint32(&t.closing, 0, 1) {
		return
	}
	t.Logger.Info().Msg("p2p task closed")
}

func resp(id uint32, c *conn, data [][]byte) {
	var wErr error
	buf := pool.BytesPool.Get().([]byte)
	defer func() {
		pool.BytesPool.Put(buf)
		if wErr != nil {
			c.Logger.Debug().AnErr("write err", wErr).Uint32("peerTask", id).Msg("resp err")
		}
		if wErr != nil {
			c.Close()
		}
	}()
	binary.BigEndian.PutUint32(buf[0:], id)
	binary.BigEndian.PutUint16(buf[4:], predef.Data)
	l := 0
	s := 0
	for _, d := range data {
		s += len(d)
	}
	if s > len(buf) {
		buf = make([]byte, s)
	}
	dataBuf := buf[10:]
	for _, d := range data {
		l += copy(dataBuf[l:], d)
	}
	if l > 0 {
		binary.BigEndian.PutUint32(buf[6:], uint32(l))
		l += 10

		if predef.Debug {
			c.Logger.Trace().Hex("data", buf[:l]).Msg("write")
		}
		_, wErr = c.Write(buf[:l])
		if wErr != nil {
			return
		}
		if c.client.config.RemoteTimeout > 0 {
			dl := time.Now().Add(c.client.config.RemoteTimeout)
			wErr = c.Conn.SetReadDeadline(dl)
			if wErr != nil {
				return
			}
		}
	}
}

func respAndClose(id uint32, c *conn, data [][]byte) {
	var wErr error
	buf := pool.BytesPool.Get().([]byte)
	defer func() {
		if wErr == nil {
			binary.BigEndian.PutUint32(buf[0:], id)
			binary.BigEndian.PutUint16(buf[4:], predef.Close)
			_, wErr = c.Write(buf[:6])
		}
		pool.BytesPool.Put(buf)
		if wErr != nil {
			c.Logger.Debug().AnErr("write err", wErr).Uint32("peerTask", id).Msg("respAndClose err")
		}
		c.peerTasksRWMtx.Lock()
		delete(c.peerTasks, id)
		c.peerTasksRWMtx.Unlock()
		if wErr != nil {
			c.Close()
		}
	}()
	binary.BigEndian.PutUint32(buf[0:], id)
	binary.BigEndian.PutUint16(buf[4:], predef.Data)
	l := 0
	s := 0
	for _, d := range data {
		s += len(d)
	}
	if s > len(buf) {
		buf = make([]byte, s)
	}
	dataBuf := buf[10:]
	for _, d := range data {
		l += copy(dataBuf[l:], d)
	}
	if l > 0 {
		binary.BigEndian.PutUint32(buf[6:], uint32(l))
		l += 10

		if predef.Debug {
			c.Logger.Trace().Hex("data", buf[:l]).Msg("write")
		}
		_, wErr = c.Write(buf[:l])
		if wErr != nil {
			return
		}
		if c.client.config.RemoteTimeout > 0 {
			dl := time.Now().Add(c.client.config.RemoteTimeout)
			wErr = c.Conn.SetReadDeadline(dl)
			if wErr != nil {
				return
			}
		}
	}
}

const (
	skipHTTPHeader = iota
	dataLength
	dataBody
	processData
)

func (t *peerTask) process(id uint32, c *conn, r *bufio.LimitedReader) {
	var err error
	defer func() {
		if err != nil {
			respAndClose(id, c, [][]byte{
				[]byte("HTTP/1.1 400 Bad Request\r\nConnection: Closed\r\n\r\n"),
			})
		}
		t.Logger.Info().Err(err).Msg("process done")
	}()
	t.Reader.SetReader(r)
	for {
		switch t.state {
		case skipHTTPHeader:
			var ok bool
			ok, err = t.skipHTTPHeader(r)
			if !ok || err != nil {
				return
			}
			t.state = dataLength
			fallthrough
		case dataLength:
			var n int
			n, err = t.Reader.Read(t.dataLen[t.n:2])
			if n > 0 {
				t.n += n
			}
			if t.n != 2 {
				if err != nil {
					if err == io.EOF || err == io.ErrUnexpectedEOF {
						err = nil
						return
					}
					t.Logger.Error().Err(err).Hex("data", t.dataLen[:t.n]).Int("read n", n).Msg("failed to read data")
					return
				}
				continue
			}
			en := uint16(t.dataLen[0])<<8 | uint16(t.dataLen[1])
			if en > pool.MaxBufferSize {
				err = errors.New("dataLen is too long")
				return
			}
			t.n = 0
			t.state = dataBody
			t.Logger.Debug().Int("read n", n).Uint16("len", en).Msg("read data length")
			fallthrough
		case dataBody:
			var n int
			en := uint16(t.dataLen[0])<<8 | uint16(t.dataLen[1])
			n, err = t.Reader.Read(t.data[t.n:en])
			if n > 0 {
				t.n += n
			}
			if t.n != int(en) {
				if err != nil {
					if err == io.EOF || err == io.ErrUnexpectedEOF {
						err = nil
						return
					}
					t.Logger.Error().Err(err).Hex("data", t.data[:t.n]).Int("read n", n).Uint16("len", en).Msg("failed to read data")
					return
				}
				continue
			}
			t.state = processData
			fallthrough
		case processData:
			if !t.initDone {
				t.initDone = true
				sdp, err := t.initSDP(c, t.ctxDone)
				if err != nil {
					t.Logger.Error().Err(err).Msg("failed to initSDP")
					return
				}
				go t.processSDP(id, c, sdp)
			} else {
				candidate := webrtc.ICECandidateInit{}
				err := json.Unmarshal(t.data[:t.n], &candidate)
				if err != nil {
					t.Logger.Error().Err(err).Msg("failed to Unmarshal candidate")
					return
				}
				err = t.conn.AddICECandidate(candidate)
				if err != nil {
					t.Logger.Error().Err(err).Msg("failed to AddICECandidate")
					return
				}
			}

			t.n = 0
			t.state = dataLength
			if err != nil {
				if err == io.EOF || err == io.ErrUnexpectedEOF {
					err = nil
					return
				}
				t.Logger.Error().Err(err).Hex("data", t.data[:t.n]).Msg("err after processData")
				return
			}
			continue
		}
	}
}

func (t *peerTask) processSDP(id uint32, c *conn, sdp []byte) {
	var err error
	defer func() {
		if err != nil {
			t.Logger.Error().Err(err).Msg("failed to processSDP")
			respAndClose(id, c, [][]byte{
				[]byte("HTTP/1.1 400 Bad Request\r\nConnection: Closed\r\n\r\n"),
			})
		}
	}()

	n := uint16(len(sdp))
	resp(id, c, [][]byte{
		[]byte("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nConnection: Closed\r\n\r\n"),
		[]byte(strconv.FormatInt(int64(n+2), 16)),
		[]byte("\r\n"),
		{byte(n >> 8), byte(n)},
		sdp,
		[]byte("\r\n"),
	})

FOR:
	for {
		select {
		case candidate, ok := <-t.candidateOutChan:
			if !ok {
				break FOR
			}
			var cj []byte
			cj, err = json.Marshal(candidate)
			if err != nil {
				return
			}
			n := uint16(len(cj))
			resp(id, c, [][]byte{
				[]byte(strconv.FormatInt(int64(n+2), 16)),
				[]byte("\r\n"),
				{byte(n >> 8), byte(n)},
				cj,
				[]byte("\r\n"),
			})
		case <-t.ctx.Done():
			err = t.ctx.Err()
			if err != nil {
				if err != context.Canceled {
					return
				}
				err = nil
			}
		}
	}

	respAndClose(id, c, [][]byte{
		[]byte("0\r\n\r\n"),
	})
}
