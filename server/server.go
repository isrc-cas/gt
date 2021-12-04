package server

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/buger/jsonparser"
	"github.com/isrc-cas/gt/config"
	"github.com/isrc-cas/gt/logger"
	"github.com/isrc-cas/gt/predef"
	"github.com/isrc-cas/gt/server/api"
	"github.com/isrc-cas/gt/server/sync"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
)

// Server is a network agent server.
type Server struct {
	config      Config
	logger      zerolog.Logger
	id2Agent    sync.Map
	closing     uint32
	tlsListener net.Listener
	listener    net.Listener
	accepted    uint64
	served      uint64
	tunneling   uint64
	apiServer   *api.Server
	authUser    func(id string, secret string) error
}

// New parses the command line args and creates a Server.
func New(args []string) (s *Server, err error) {
	conf := defaultConfig()
	err = config.ParseFlags(args, &conf, &conf.Options)
	if err != nil {
		return
	}
	if conf.Options.Version {
		fmt.Println(predef.Version)
		os.Exit(0)
	}
	usersYaml := struct{ Users Users }{make(Users)}
	err = config.Yaml2Interface(&conf.Options.Users, &usersYaml)
	if err != nil {
		return
	}
	err = conf.Users.mergeUsers(usersYaml.Users, conf.ID, conf.Secret)
	if err != nil {
		return
	}

	// 裸端口支持
	if !strings.Contains(conf.Addr, ":") {
		conf.Addr = ":" + conf.Addr
	}
	if !strings.Contains(conf.TLSAddr, ":") {
		conf.TLSAddr = ":" + conf.TLSAddr
	}
	if !strings.Contains(conf.APIAddr, ":") {
		conf.APIAddr = ":" + conf.APIAddr
	}

	err = logger.Init(logger.Options{
		FilePath:          conf.LogFile,
		RotationCount:     conf.LogFileMaxCount,
		RotationSize:      conf.LogFileMaxSize,
		Level:             conf.LogLevel,
		SentryDSN:         conf.SentryDSN,
		SentryLevels:      conf.SentryLevel,
		SentrySampleRate:  conf.SentrySampleRate,
		SentryRelease:     conf.SentryRelease,
		SentryEnvironment: conf.SentryEnvironment,
		SentryServerName:  conf.SentryServerName,
		SentryDebug:       conf.SentryDebug,
	})
	if err != nil {
		return
	}

	s = &Server{
		config: conf,
		logger: logger.Logger,
	}
	return
}

func (s *Server) tlsListen() (err error) {
	s.logger.Info().Str("addr", s.config.TLSAddr).Msg("Listening TLS")
	crt, err := tls.LoadX509KeyPair(s.config.CertFile, s.config.KeyFile)
	if err != nil {
		err = fmt.Errorf("invalid cert and key, cause %s", err.Error())
		return
	}
	tlsConfig := &tls.Config{}
	tlsConfig.Certificates = []tls.Certificate{crt}
	switch strings.ToLower(s.config.TLSMinVersion) {
	case "tls1.1":
		tlsConfig.MinVersion = tls.VersionTLS11
	default:
		fallthrough
	case "tls1.2":
		tlsConfig.MinVersion = tls.VersionTLS12
		tlsConfig.CipherSuites = []uint16{
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
		}
	case "tls1.3":
		tlsConfig.MinVersion = tls.VersionTLS13
	}
	l, err := tls.Listen("tcp", s.config.TLSAddr, tlsConfig)
	if err != nil {
		err = fmt.Errorf("can not listen on addr '%s', cause %s, please check option 'tlsAddr'", s.config.TLSAddr, err.Error())
		return
	}
	s.tlsListener = l
	go s.acceptLoop(l)
	return
}

func (s *Server) listen() (err error) {
	s.logger.Info().Str("addr", s.config.Addr).Msg("Listening")
	l, err := net.Listen("tcp", s.config.Addr)
	if err != nil {
		err = fmt.Errorf("can not listen on addr '%s', cause %s, please check option 'addr'", s.config.Addr, err.Error())
		return
	}
	s.listener = l
	go s.acceptLoop(l)
	return
}

func (s *Server) acceptLoop(l net.Listener) {
	var err error
	defer func() {
		if errors.Is(err, net.ErrClosed) {
			err = nil
		}
		s.logger.Info().Err(err).Msg("acceptLoop ended")
	}()
	s.logger.Info().Msg("acceptLoop started")
	var tempDelay time.Duration // how long to sleep on accept failure
	for {
		if atomic.LoadUint32(&s.closing) > 0 {
			return
		}
		var conn net.Conn
		conn, err = l.Accept()
		if err != nil {
			if atomic.LoadUint32(&s.closing) > 0 {
				return
			}
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				if tempDelay == 0 {
					tempDelay = 5 * time.Millisecond
				} else {
					tempDelay *= 2
				}
				if max := 1 * time.Second; tempDelay > max {
					tempDelay = max
				}
				s.logger.Error().Err(err).Dur("delay", tempDelay).Msg("Server accept error")
				time.Sleep(tempDelay)
				continue
			}
			return
		}
		atomic.AddUint64(&s.accepted, 1)
		c := newConn(conn, s)
		go c.handle()
	}
}

// Start runs the server.
func (s *Server) Start() (err error) {
	s.logger.Info().Interface("config", s.config).Msg(predef.Version)

	if len(s.config.AuthAPI) > 0 {
		s.authUser = s.authUserWithAPI
	} else {
		err = s.config.Users.verify()
		if err != nil {
			return
		}
		s.authUser = s.authUserWithConfig
	}
	if len(s.config.APIAddr) > 0 {
		apiServer := api.NewServer(s.config.APIAddr, s.logger.With().Str("src", "apiServer").Logger(), s.config.Users.idConflict)
		s.apiServer = apiServer
	}

	var listening bool
	if len(s.config.TLSAddr) > 0 && len(s.config.CertFile) > 0 && len(s.config.KeyFile) > 0 {
		err = s.tlsListen()
		if err != nil {
			return
		}
		listening = true
	}
	if len(s.config.Addr) > 0 {
		err = s.listen()
		if err != nil {
			return
		}
		listening = true
	}
	if !listening {
		err = errors.New("no services is providing, please check the config")
		return
	}

	if len(s.config.APIAddr) > 0 {
		err = s.startAPIServer()
		if err != nil {
			return
		}
	}
	return
}

func (s *Server) startAPIServer() error {
	ln, err := net.Listen("tcp", s.config.APIAddr)
	if err != nil {
		return fmt.Errorf("can not listen on addr '%s', cause %s, please check option 'apiAddr'", s.config.APIAddr, err.Error())
	}
	if s.tlsListener != nil {
		s.apiServer.RemoteSchema = "tls://"
		s.apiServer.RemoteAddr = s.tlsListener.Addr().String()
		go func() {
			err := s.apiServer.ServeTLS(ln, s.config.CertFile, s.config.KeyFile)
			if errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
			s.logger.Info().Err(err).Msg("api server closed")
		}()
		return nil
	}
	s.apiServer.RemoteSchema = "tcp://"
	s.apiServer.RemoteAddr = s.listener.Addr().String()
	go func() {
		err := s.apiServer.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		s.logger.Info().Err(err).Msg("api server closed")
	}()
	return nil
}

func (s *Server) authWithAPI(id string, secret string) (ok bool, err error) {
	var bs bytes.Buffer
	bs.WriteString(`{"clientId": "`)
	bs.WriteString(id)
	bs.WriteString(`", "secretKey": "`)
	bs.WriteString(secret)
	bs.WriteString(`"}`)
	req, err := http.NewRequest("POST", s.config.AuthAPI, &bs)
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Request-Id", strconv.FormatInt(time.Now().Unix(), 10))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	r, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}
	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("invalid http status code %d, body: %s", resp.StatusCode, string(r))
		return
	}
	ok, err = jsonparser.GetBoolean(r, "result")
	return
}

// Close stops the server.
func (s *Server) Close() {
	if !atomic.CompareAndSwapUint32(&s.closing, 0, 1) {
		return
	}
	var e1, e2, e error
	if s.apiServer != nil {
		e = s.apiServer.Close()
	}
	if s.listener != nil {
		e1 = s.listener.Close()
	}
	if s.tlsListener != nil {
		e2 = s.tlsListener.Close()
	}
	s.id2Agent.Range(func(key, value interface{}) bool {
		if c, ok := value.(*client); ok && c != nil {
			c.close()
		}
		return true
	})
	s.logger.Info().AnErr("listener", e1).AnErr("tlsListener", e2).AnErr("api", e).Msg("server stopped")
}

// IsClosing tells is the server stopping.
func (s *Server) IsClosing() (closing bool) {
	return atomic.LoadUint32(&s.closing) > 0
}

// Shutdown stops the server gracefully.
func (s *Server) Shutdown() {
	if !atomic.CompareAndSwapUint32(&s.closing, 0, 1) {
		return
	}
	var e1, e2, e error
	if s.apiServer != nil {
		e = s.apiServer.Close()
	}
	if s.listener != nil {
		e1 = s.listener.Close()
	}
	if s.tlsListener != nil {
		e2 = s.tlsListener.Close()
	}
	for {
		accepted := s.GetAccepted()
		served := s.GetServed()
		tunneling := s.GetTunneling()
		if accepted == served+tunneling {
			break
		}

		i := 0
		s.id2Agent.Range(func(key, value interface{}) bool {
			i++
			if c, ok := value.(*client); ok && c != nil {
				c.shutdown()
			}
			return true
		})
		if i == 0 {
			break
		}

		s.logger.Info().
			Uint64("accepted", s.GetAccepted()).
			Uint64("served", s.GetServed()).
			Uint64("tunneling", s.GetTunneling()).
			Msg("server shutting down")
		time.Sleep(3 * time.Second)
	}
	s.id2Agent.Range(func(key, value interface{}) bool {
		if c, ok := value.(*client); ok && c != nil {
			c.close()
		}
		return true
	})
	s.logger.Info().AnErr("listener", e1).AnErr("tlsListener", e2).AnErr("api", e).Msg("server stopped")
	return
}

func (s *Server) getOrCreateClient(id string, fn func() interface{}) (result *client, exists bool) {
	value, exists := s.id2Agent.LoadOrCreate(id, fn)
	result = value.(*client)
	return
}

func (s *Server) getClient(id string) (c *client, ok bool) {
	value, ok := s.id2Agent.Load(id)
	if ok {
		if c, ok = value.(*client); ok {
			ok = c != nil
			return
		}
	}
	return
}

func (s *Server) removeClient(id string) {
	s.id2Agent.Delete(id)
}

// GetAccepted returns value of accepted
func (s *Server) GetAccepted() uint64 {
	return atomic.LoadUint64(&s.accepted)
}

// GetServed returns value of served
func (s *Server) GetServed() uint64 {
	return atomic.LoadUint64(&s.served)
}

// GetTunneling returns value of tunneling
func (s *Server) GetTunneling() uint64 {
	return atomic.LoadUint64(&s.tunneling)
}

// ErrInvalidUser is returned if id and secret are invalid
var ErrInvalidUser = errors.New("invalid user")

func (s *Server) authUserWithConfig(id string, secret string) (err error) {
	if len(id) < 1 || len(secret) < 1 {
		err = ErrInvalidUser
		return
	}
	ok := s.config.Users.auth(id, secret)
	if !ok {
		if s.apiServer != nil && !s.apiServer.Auth(id, secret) {
			err = ErrInvalidUser
		}
	}
	return
}

func (s *Server) authUserWithAPI(id string, secret string) (err error) {
	if len(id) < 1 || len(secret) < 1 {
		err = ErrInvalidUser
		return
	}
	ok, err := s.authWithAPI(id, secret)
	if err != nil {
		return
	}
	if !ok {
		if s.apiServer != nil && !s.apiServer.Auth(id, secret) {
			err = ErrInvalidUser
		}
	}
	return
}
