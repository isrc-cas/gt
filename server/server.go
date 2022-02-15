package server

import (
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/buger/jsonparser"
	"github.com/isrc-cas/gt/config"
	"github.com/isrc-cas/gt/logger"
	"github.com/isrc-cas/gt/predef"
	"github.com/isrc-cas/gt/server/api"
	"github.com/isrc-cas/gt/server/sync"
	"github.com/pion/logging"
	"github.com/pion/turn"
)

// Server is a network agent server.
type Server struct {
	config       Config
	users        users
	Logger       logger.Logger
	id2Agent     sync.Map
	closing      uint32
	tlsListener  net.Listener
	listener     net.Listener
	sniListener  net.Listener
	accepted     uint64
	served       uint64
	failed       uint64
	tunneling    uint64
	apiServer    *api.Server
	authUser     func(id string, secret string) error
	removeClient func(id string)
	turnServer   *turn.Server
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

	l, err := logger.Init(logger.Options{
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
		Logger: l,
	}
	return
}

func (s *Server) tlsListen() (err error) {
	s.Logger.Info().Str("addr", s.config.TLSAddr).Msg("Listening TLS")
	var tlsConfig *tls.Config
	tlsConfig, err = newTLSConfig(s.config.CertFile, s.config.KeyFile, s.config.TLSMinVersion)
	if err != nil {
		return
	}
	l, err := tls.Listen("tcp", s.config.TLSAddr, tlsConfig)
	if err != nil {
		err = fmt.Errorf("can not listen on addr '%s', cause %s, please check option 'tlsAddr'", s.config.TLSAddr, err.Error())
		return
	}
	s.tlsListener = l
	go s.acceptLoop(l, func(c *conn) {
		c.handle()
	})
	return
}

func (s *Server) listen() (err error) {
	s.Logger.Info().Str("addr", s.config.Addr).Msg("Listening")
	l, err := net.Listen("tcp", s.config.Addr)
	if err != nil {
		err = fmt.Errorf("can not listen on addr '%s', cause %s, please check option 'addr'", s.config.Addr, err.Error())
		return
	}
	s.listener = l
	go s.acceptLoop(l, func(c *conn) {
		c.handle()
	})
	return
}

func (s *Server) sniListen() (err error) {
	s.Logger.Info().Str("sniAddr", s.config.SNIAddr).Msg("Listening")
	l, err := net.Listen("tcp", s.config.SNIAddr)
	if err != nil {
		err = fmt.Errorf("can not listen on addr '%s', cause %s, please check option 'sniAddr'", s.config.SNIAddr, err.Error())
		return
	}
	s.sniListener = l
	go s.acceptLoop(l, func(c *conn) {
		c.handleSNI()
	})
	return
}

func (s *Server) acceptLoop(l net.Listener, handle func(*conn)) {
	var err error
	defer func() {
		if !predef.Debug {
			if e := recover(); e != nil {
				s.Logger.Error().Msgf("recovered panic: %#v\n%s", e, debug.Stack())
			}
		}
		if errors.Is(err, net.ErrClosed) {
			err = nil
		}
		s.Logger.Info().Str("addr", l.Addr().String()).Err(err).Msg("acceptLoop ended")
	}()
	s.Logger.Info().Str("addr", l.Addr().String()).Msg("acceptLoop started")
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
				s.Logger.Error().Err(err).Dur("delay", tempDelay).Msg("Server accept error")
				time.Sleep(tempDelay)
				continue
			}
			return
		}
		atomic.AddUint64(&s.accepted, 1)
		c := newConn(conn, s)
		go handle(c)
	}
}

// Start runs the server.
func (s *Server) Start() (err error) {
	s.Logger.Info().Interface("config", &s.config).Msg(predef.Version)

	err = s.users.mergeUsers(s.config.Users, nil, nil)
	if err != nil {
		return
	}
	users := make(map[string]user)
	err = config.Yaml2Interface(s.config.Options.Users, users)
	if err != nil {
		return
	}
	err = s.users.mergeUsers(users, s.config.ID, s.config.Secret)
	if err != nil {
		return
	}

	if len(s.config.HTTPMUXHeader) <= 0 {
		err = fmt.Errorf("HTTP multiplexing header (-httpMUXHeader option) '%s' is invalid", s.config.HTTPMUXHeader)
		return
	}

	if len(s.config.AuthAPI) > 0 {
		s.authUser = s.authUserWithAPI
		s.removeClient = s.removeClientOnly
	} else if s.users.empty() {
		s.Logger.Warn().Msg("working on -allowAnyClient mode, because no user is configured")
		s.authUser = s.authUserOrCreateUser
		s.removeClient = s.removeClientAndUser
	} else if !s.config.AllowAnyClient {
		s.authUser = s.authUserWithConfig
		s.removeClient = s.removeClientOnly
	} else {
		s.authUser = s.authUserOrCreateUser
		s.removeClient = s.removeClientAndTempUser
	}
	if len(s.config.APIAddr) > 0 {
		if strings.IndexByte(s.config.APIAddr, ':') == -1 {
			s.config.APIAddr = ":" + s.config.APIAddr
		}
		apiServer := api.NewServer(s.config.APIAddr, s.Logger.With().Str("scope", "apiServer").Logger(), s.users.idConflict)
		s.apiServer = apiServer
	}

	var listening bool
	if len(s.config.TLSAddr) > 0 && len(s.config.CertFile) > 0 && len(s.config.KeyFile) > 0 {
		if strings.IndexByte(s.config.TLSAddr, ':') == -1 {
			s.config.TLSAddr = ":" + s.config.TLSAddr
		}
		err = s.tlsListen()
		if err != nil {
			return
		}
		listening = true
	}
	if len(s.config.Addr) > 0 {
		if strings.IndexByte(s.config.Addr, ':') == -1 {
			s.config.Addr = ":" + s.config.Addr
		}
		err = s.listen()
		if err != nil {
			return
		}
		listening = true
	}
	if len(s.config.SNIAddr) > 0 {
		if strings.IndexByte(s.config.SNIAddr, ':') == -1 {
			s.config.SNIAddr = ":" + s.config.SNIAddr
		}
		err = s.sniListen()
		if err != nil {
			return
		}
	}
	if !listening {
		err = errors.New("no services is providing, please check the config")
		return
	}

	if len(s.config.TURNAddr) > 0 {
		err = s.startTURNServer()
		if err != nil {
			return
		}
	}

	if len(s.config.APIAddr) > 0 {
		err = s.startAPIServer()
		if err != nil {
			return
		}
	}
	return
}

func (s *Server) startTURNServer() (err error) {
	if strings.IndexByte(s.config.TURNAddr, ':') == -1 {
		s.config.TURNAddr = ":" + s.config.TURNAddr
	}
	host, port, err := net.SplitHostPort(s.config.TURNAddr)
	if err != nil {
		return
	}
	udpPort, err := strconv.Atoi(port)
	if err != nil {
		return
	}
	factory := logging.NewDefaultLoggerFactory()
	factory.Writer = s.Logger.With().Str("scope", "turnServer").Logger()
	server := turn.NewServer(&turn.ServerConfig{
		Realm:              "gt",
		ChannelBindTimeout: s.config.ChannelBindTimeout,
		ListeningPort:      udpPort,
		LoggerFactory:      factory,
		Software:           predef.Version,
		AuthHandler: func(username string, srcAddr net.Addr) (password string, ok bool) {
			value, ok := s.users.Load(username)
			if ok {
				password = value.(string)
			}
			return
		},
	})
	if len(host) > 0 {
		err = server.AddListeningIPAddr(host)
		if err != nil {
			return
		}
	}

	s.turnServer = server
	return server.Start()
}

func newTLSConfig(cert, key, tlsMinVersion string) (tlsConfig *tls.Config, err error) {
	crt, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		err = fmt.Errorf("invalid cert and key, cause %s", err.Error())
		return
	}
	tlsConfig = &tls.Config{}
	tlsConfig.Certificates = []tls.Certificate{crt}
	switch strings.ToLower(tlsMinVersion) {
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
	return
}

func (s *Server) startAPIServer() (err error) {
	if s.tlsListener != nil {
		s.apiServer.RemoteSchema = "tls://"
		s.apiServer.RemoteAddr = s.tlsListener.Addr().String()
	} else if s.listener != nil {
		s.apiServer.RemoteSchema = "tcp://"
		s.apiServer.RemoteAddr = s.listener.Addr().String()
	}
	var l net.Listener
	if len(s.config.APICertFile) > 0 && len(s.config.APIKeyFile) > 0 {
		var tlsConfig *tls.Config
		tlsConfig, err = newTLSConfig(s.config.APICertFile, s.config.APIKeyFile, s.config.APITLSMinVersion)
		if err != nil {
			return
		}
		l, err = tls.Listen("tcp", s.config.APIAddr, tlsConfig)
		if err != nil {
			return fmt.Errorf("can not listen on addr '%s', cause %s, please check option 'tlsAddr'", s.config.APIAddr, err.Error())
		}
	} else {
		l, err = net.Listen("tcp", s.config.APIAddr)
		if err != nil {
			return fmt.Errorf("can not listen on addr '%s', cause %s, please check option 'apiAddr'", s.config.APIAddr, err.Error())
		}
	}
	go func() {
		err := s.apiServer.Serve(l)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		s.Logger.Info().Err(err).Msg("api server closed")
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
	client := http.Client{
		Timeout: s.config.Timeout,
	}
	resp, err := client.Do(req)
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
	defer s.Logger.Close()
	event := s.Logger.Info()
	if s.apiServer != nil {
		event.AnErr("api", s.apiServer.Close())
	}
	if s.turnServer != nil {
		event.AnErr("turn", s.turnServer.Close())
	}
	if s.listener != nil {
		event.AnErr("listener", s.listener.Close())
	}
	if s.tlsListener != nil {
		event.AnErr("tlsListener", s.tlsListener.Close())
	}
	if s.sniListener != nil {
		event.AnErr("sniListener", s.sniListener.Close())
	}
	s.id2Agent.Range(func(key, value interface{}) bool {
		if c, ok := value.(*client); ok && c != nil {
			c.close()
		}
		return true
	})
	event.Msg("server stopped")
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
	defer s.Logger.Close()
	event := s.Logger.Info()
	if s.apiServer != nil {
		event.AnErr("api", s.apiServer.Close())
	}
	if s.turnServer != nil {
		event.AnErr("turn", s.turnServer.Close())
	}
	if s.listener != nil {
		event.AnErr("listener", s.listener.Close())
	}
	if s.tlsListener != nil {
		event.AnErr("tlsListener", s.tlsListener.Close())
	}
	if s.sniListener != nil {
		event.AnErr("sniListener", s.sniListener.Close())
	}
	for {
		accepted := s.GetAccepted()
		served := s.GetServed()
		failed := s.GetFailed()
		tunneling := s.GetTunneling()
		if accepted == served+failed+tunneling {
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

		s.Logger.Info().
			Uint64("accepted", accepted).
			Uint64("served", served).
			Uint64("failed", failed).
			Uint64("tunneling", tunneling).
			Msg("server shutting down")
		time.Sleep(3 * time.Second)
	}
	s.id2Agent.Range(func(key, value interface{}) bool {
		if c, ok := value.(*client); ok && c != nil {
			c.close()
		}
		return true
	})
	event.Msg("server stopped")
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

// GetAccepted returns value of accepted
func (s *Server) GetAccepted() uint64 {
	return atomic.LoadUint64(&s.accepted)
}

// GetServed returns value of served
func (s *Server) GetServed() uint64 {
	return atomic.LoadUint64(&s.served)
}

// GetFailed returns value of served
func (s *Server) GetFailed() uint64 {
	return atomic.LoadUint64(&s.failed)
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
	ok := s.users.auth(id, secret)
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

func (s *Server) authUserOrCreateUser(id, secret string) (err error) {
	if s.apiServer != nil && s.apiServer.Auth(id, secret) {
		return
	}

	value, loaded := s.users.LoadOrCreate(id, func() interface{} {
		return user{
			Secret: secret,
			temp:   true,
		}
	})
	if loaded && secret != value.(user).Secret {
		err = ErrInvalidUser
	}
	return
}

func (s *Server) removeClientOnly(id string) {
	s.id2Agent.Delete(id)
}

func (s *Server) removeClientAndUser(id string) {
	s.id2Agent.Delete(id)
	s.users.Delete(id)
}

func (s *Server) removeClientAndTempUser(id string) {
	value, loaded := s.id2Agent.LoadAndDelete(id)
	if loaded && value.(user).temp {
		s.users.Delete(id)
	}
}
