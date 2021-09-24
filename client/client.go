package client

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"github.com/isrc-cas/gt/config"
	"github.com/isrc-cas/gt/logger"
	"github.com/isrc-cas/gt/predef"
	"io/ioutil"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
)

// Client is a network agent client.
type Client struct {
	config       Config
	logger       zerolog.Logger
	dialFn       func() (net.Conn, error)
	initConnMtx  sync.Mutex
	closing      uint32
	tunnels      map[*conn]struct{}
	tunnelsRWMtx sync.RWMutex
	tunnelsCond  *sync.Cond
}

// New parses the command line args and creates a Client.
func New(args []string) (c *Client, err error) {
	conf := defaultConfig()
	err = config.ParseFlags(args, &conf, &conf.Options)
	if err != nil {
		return
	}
	if conf.Options.Version {
		fmt.Println(predef.Version)
		os.Exit(0)
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

	c = &Client{
		config:  conf,
		logger:  logger.Logger,
		tunnels: make(map[*conn]struct{}),
	}
	c.tunnelsCond = sync.NewCond(c.tunnelsRWMtx.RLocker())
	return
}

// Start runs the client agent.
func (c *Client) Start() (err error) {
	c.logger.Info().Interface("config", c.config).Msg(predef.Version)

	// TODO: check ID config
	if len(c.config.ID) < predef.MinIDSize || len(c.config.ID) > predef.MaxIDSize {
		err = fmt.Errorf("agent id (-id option) '%s' is invalid", c.config.ID)
		return
	}
	if len(c.config.Secret) < predef.MinSecretSize || len(c.config.Secret) > predef.MaxSecretSize {
		err = fmt.Errorf("agent secret (-secret option) '%s' is invalid", c.config.Secret)
		return
	}

	// 默认 tcp
	if !strings.Contains(c.config.Remote, "://") {
		c.config.Remote = "tcp://" + c.config.Remote
	}

	if !strings.HasPrefix(c.config.Remote, "tls://") &&
		!strings.HasPrefix(c.config.Remote, "tcp://") {
		err = fmt.Errorf("remote url (-remote option) '%s' must begin with tcp:// or tls://", c.config.Remote)
		return
	}
	c.config.Remote = strings.TrimSuffix(c.config.Remote, "/")

	if !strings.HasPrefix(c.config.Local, "http://") &&
		!strings.HasPrefix(c.config.Local, "https://") {
		err = fmt.Errorf("local url (-local option) '%s' must begin with http:// or https://", c.config.Local)
		return
	}
	c.config.Local = strings.TrimSuffix(c.config.Local, "/")

	config := &tls.Config{
		PreferServerCipherSuites: true,
	}
	if len(c.config.RemoteCert) > 0 {
		var cf []byte
		cf, err = ioutil.ReadFile(c.config.RemoteCert)
		if err != nil {
			err = fmt.Errorf("failed to read remote cert file (-remoteCert option) '%s', cause %s", c.config.RemoteCert, err.Error())
			return
		}
		roots := x509.NewCertPool()
		ok := roots.AppendCertsFromPEM(cf)
		if !ok {
			err = fmt.Errorf("failed to parse remote cert file (-remoteCert option) '%s'", c.config.RemoteCert)
			return
		}
		config.RootCAs = roots
	}
	if c.config.RemoteCertInsecure {
		config.InsecureSkipVerify = true
	}

	u, err := url.Parse(c.config.Remote)
	if err != nil {
		err = fmt.Errorf("remote url (-remote option) '%s' is invalid, cause %s", c.config.Remote, err.Error())
		return
	}
	switch u.Scheme {
	case "tls":
		if len(u.Port()) < 1 {
			u.Host = net.JoinHostPort(u.Host, "443")
		}
		c.dialFn = func() (net.Conn, error) {
			return tls.Dial("tcp", u.Host, config)
		}
	case "tcp":
		if len(u.Port()) < 1 {
			u.Host = net.JoinHostPort(u.Host, "80")
		}
		c.dialFn = func() (net.Conn, error) {
			return net.Dial(u.Scheme, u.Host)
		}
	default:
		err = fmt.Errorf("remote url (-remote option) '%s' is invalid", c.config.Remote)
		return
	}

	if c.config.RemoteConnections < 1 {
		c.config.RemoteConnections = 1
	} else if c.config.RemoteConnections > 3 {
		c.config.RemoteConnections = 3
	}

	for i := uint(0); i < c.config.RemoteConnections; i++ {
		go c.connect()
	}
	return
}

// Close stops the client agent.
func (c *Client) Close() {
	if !atomic.CompareAndSwapUint32(&c.closing, 0, 1) {
		return
	}
	c.tunnelsRWMtx.Lock()
	for t := range c.tunnels {
		t.SendCloseSignal()
		t.Close()
	}
	c.tunnelsRWMtx.Unlock()
}

func (c *Client) initConn() (result *conn, err error) {
	c.initConnMtx.Lock()
	defer c.initConnMtx.Unlock()

	conn, err := c.dialFn()
	if err != nil {
		return
	}
	result = newConn(conn, c)
	err = result.init()
	if err != nil {
		result.Close()
	}
	return
}

func (c *Client) connect() {
	for {
		c.logger.Info().Msg("trying to connect to remote")
		conn, err := c.initConn()
		if err == nil {
			conn.readLoop()
		} else {
			c.logger.Error().Err(err).Msg("failed to connect to remote")
		}
		if atomic.LoadUint32(&c.closing) == 1 {
			break
		}
		time.Sleep(c.config.ReconnectDelay)
	}
}

func (c *Client) addTunnel(conn *conn) {
	c.tunnelsRWMtx.Lock()
	c.tunnels[conn] = struct{}{}
	c.tunnelsRWMtx.Unlock()
	c.tunnelsCond.Broadcast()
}

func (c *Client) removeTunnel(conn *conn) {
	c.tunnelsRWMtx.Lock()
	delete(c.tunnels, conn)
	c.tunnelsRWMtx.Unlock()
}

var errTimeout = errors.New("timeout")

// WaitUntilReady waits until the client connected to server
func (c *Client) WaitUntilReady(timeout time.Duration) (err error) {
	c.tunnelsRWMtx.RLock()
	defer c.tunnelsRWMtx.RUnlock()
	for len(c.tunnels) < 1 {
		var e atomic.Value
		func() {
			timer := time.AfterFunc(timeout, func() {
				e.Store(errTimeout)
				c.tunnelsCond.Broadcast()
			})
			defer timer.Stop()
			c.tunnelsCond.Wait()
		}()
		v := e.Load()
		if v == nil {
			return
		}
		err = v.(error)
		if err != nil {
			return
		}
	}
	return
}
