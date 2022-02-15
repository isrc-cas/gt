package client

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/buger/jsonparser"
	"github.com/isrc-cas/gt/config"
	"github.com/isrc-cas/gt/logger"
	"github.com/isrc-cas/gt/predef"
	"github.com/isrc-cas/gt/util"
	"github.com/rs/zerolog"
)

// Client is a network agent client.
type Client struct {
	config       Config
	logger       zerolog.Logger
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

type dialer struct {
	host      string
	tlsConfig *tls.Config
	dialFn    func() (conn net.Conn, err error)
}

func (d *dialer) init(c *Client, remote string) (err error) {
	var u *url.URL
	u, err = url.Parse(remote)
	if err != nil {
		err = fmt.Errorf("remote url (-remote option) '%s' is invalid, cause %s", remote, err.Error())
		return
	}
	switch u.Scheme {
	case "tls":
		if len(u.Port()) < 1 {
			u.Host = net.JoinHostPort(u.Host, "443")
		}
		tlsConfig := &tls.Config{
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
			tlsConfig.RootCAs = roots
		}
		if c.config.RemoteCertInsecure {
			tlsConfig.InsecureSkipVerify = true
		}
		d.host = u.Host
		d.tlsConfig = tlsConfig
		d.dialFn = d.tlsDial
	case "tcp":
		if len(u.Port()) < 1 {
			u.Host = net.JoinHostPort(u.Host, "80")
		}
		d.host = u.Host
		d.dialFn = d.dial
	default:
		err = fmt.Errorf("remote url (-remote option) '%s' is invalid", remote)
	}
	return
}

func (d *dialer) initWithRemote(c *Client) (err error) {
	return d.init(c, c.config.Remote)
}

func (d *dialer) initWithRemoteAPI(c *Client) (err error) {
	req, err := http.NewRequest("GET", c.config.RemoteAPI, nil)
	if err != nil {
		return
	}
	query := req.URL.Query()
	query.Add("network_client_id", c.config.ID)
	req.URL.RawQuery = query.Encode()
	req.Header.Set("Request-Id", strconv.FormatInt(time.Now().Unix(), 10))
	client := http.Client{
		Timeout: c.config.RemoteTimeout,
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
	addr, err := jsonparser.GetString(r, "serverAddress")
	if err != nil {
		return
	}
	err = d.init(c, addr)
	return
}

func (d *dialer) dial() (conn net.Conn, err error) {
	return net.Dial("tcp", d.host)
}

func (d *dialer) tlsDial() (conn net.Conn, err error) {
	return tls.Dial("tcp", d.host, d.tlsConfig)
}

// Start runs the client agent.
func (c *Client) Start() (err error) {
	c.logger.Info().Interface("config", c.config).Msg(predef.Version)

	if len(c.config.ID) < predef.MinIDSize || len(c.config.ID) > predef.MaxIDSize {
		err = fmt.Errorf("agent id (-id option) '%s' is invalid", c.config.ID)
		return
	}
	if c.config.Secret == "" {
		c.config.Secret = util.RandomString(predef.DefaultSecretSize)
	} else if len(c.config.Secret) < predef.MinSecretSize || len(c.config.Secret) > predef.MaxSecretSize {
		err = fmt.Errorf("agent secret (-secret option) '%s' is invalid", c.config.Secret)
		return
	}

	var dialer dialer
	if len(c.config.Remote) > 0 {
		if !strings.Contains(c.config.Remote, "://") {
			c.config.Remote = "tcp://" + c.config.Remote
		}
		err = dialer.initWithRemote(c)
		if err != nil {
			return
		}
	}
	if len(c.config.RemoteAPI) > 0 {
		if !strings.HasPrefix(c.config.RemoteAPI, "http://") &&
			!strings.HasPrefix(c.config.RemoteAPI, "https://") {
			err = fmt.Errorf("remote api url (-remoteAPI option) '%s' must begin with http:// or https://", c.config.RemoteAPI)
			return
		}
		for len(dialer.host) == 0 {
			if atomic.LoadUint32(&c.closing) == 1 {
				err = errors.New("client is closing")
				return
			}
			err = dialer.initWithRemoteAPI(c)
			if err == nil {
				break
			}
			c.logger.Error().Err(err).Msg("failed to query server address")
			time.Sleep(c.config.ReconnectDelay)
		}
	}
	if len(dialer.host) == 0 {
		err = errors.New("option -remote or -remoteAPI must be specified")
		return
	}

	if !strings.HasPrefix(c.config.Local, "http://") &&
		!strings.HasPrefix(c.config.Local, "https://") {
		err = fmt.Errorf("local url (-local option) '%s' must begin with http:// or https://", c.config.Local)
		return
	}

	if c.config.RemoteConnections < 1 {
		c.config.RemoteConnections = 1
	} else if c.config.RemoteConnections > 3 {
		c.config.RemoteConnections = 3
	}

	for i := uint(0); i < c.config.RemoteConnections; i++ {
		go c.connectLoop(dialer)
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

func (c *Client) initConn(d dialer) (result *conn, err error) {
	c.initConnMtx.Lock()
	defer c.initConnMtx.Unlock()

	conn, err := d.dialFn()
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

func (c *Client) connect(d dialer) (closing bool) {
	c.logger.Info().Msg("trying to connect to remote")

	defer func() {
		if !predef.Debug {
			if e := recover(); e != nil {
				c.logger.Error().Msgf("recovered panic: %#v\n%s", e, debug.Stack())
			}
		}
	}()

	conn, err := c.initConn(d)
	if err == nil {
		conn.readLoop()
	} else {
		c.logger.Error().Err(err).Msg("failed to connect to remote")
	}

	if atomic.LoadUint32(&c.closing) == 1 {
		return true
	}
	time.Sleep(c.config.ReconnectDelay)

	for len(c.config.RemoteAPI) > 0 {
		if atomic.LoadUint32(&c.closing) == 1 {
			return true
		}
		err = d.initWithRemoteAPI(c)
		if err == nil {
			break
		}
		c.logger.Error().Err(err).Msg("failed to query server address")
		time.Sleep(c.config.ReconnectDelay)
	}
	return
}

func (c *Client) connectLoop(d dialer) {
	for atomic.LoadUint32(&c.closing) == 0 {
		if c.connect(d) {
			break
		}
	}
	c.logger.Info().Msg("connect loop exited")
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
