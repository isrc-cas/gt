package api

import (
	"bytes"
	"context"
	"fmt"
	"github.com/isrc-cas/gt/client"
	"github.com/isrc-cas/gt/predef"
	"io"
	"math/rand"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
)

// Server provides internal api service.
type Server struct {
	http.Server
	logger         zerolog.Logger
	checkTunnelMtx sync.Mutex
	RemoteAddr     string
	RemoteSchema   string

	// status response cache
	statusRespCache     http.Response
	statusRespCacheTime time.Time
	statusRespCacheBody *bytes.Reader

	id         atomic.Value
	secret     atomic.Value
	idConflict func(id string) bool
}

// ID 返回 api server 生成的 id
func (s *Server) ID() string {
	idValue := s.id.Load()
	if idValue == nil {
		return ""
	}
	return idValue.(string)
}

// Secret 返回 api server 生成的 secret
func (s *Server) Secret() string {
	secretValue := s.secret.Load()
	if secretValue == nil {
		return ""
	}
	return secretValue.(string)
}

// NewServer returns an api server instance.
func NewServer(addr string, logger zerolog.Logger, idConflict func(id string) bool) *Server {
	mux := http.NewServeMux()
	s := &Server{
		Server: http.Server{
			Addr:    addr,
			Handler: mux,
		},
		logger:     logger,
		idConflict: idConflict,
	}
	mux.HandleFunc("/status", s.status)
	mux.HandleFunc("/statusResp", s.statusResp)
	return s
}

func (s *Server) status(writer http.ResponseWriter, _ *http.Request) {
	err := s.check(writer)
	if err != nil {
		s.logger.Warn().Err(err).Msg("failed to check status")
		writer.WriteHeader(http.StatusServiceUnavailable)
		r := `{"status": "failed", "version":"` + predef.Version + `"}`
		_, err = writer.Write([]byte(r))
		if err != nil {
			s.logger.Warn().Err(err).Msg("failed to resp failed status")
		}
	}
}

func (s *Server) statusResp(writer http.ResponseWriter, _ *http.Request) {
	r := `{"status": "ok", "version":"` + predef.Version + `"}`
	_, err := writer.Write([]byte(r))
	if err != nil {
		s.logger.Warn().Err(err).Msg("failed to responses to statusResp request")
	}
}

func randomString(n int) string {
	letters := []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
	r := rand.New(rand.NewSource(time.Now().Unix()))

	s := make([]rune, n)
	for i := range s {
		s[i] = letters[r.Intn(len(letters))]
	}
	return string(s)
}

func (s *Server) randomIDSecret() error {
	retries := 10
	for i := 0; i < retries; i++ {
		id := randomString(64)
		if s.idConflict(id) {
			continue
		}
		s.id.Store(id)
		s.secret.Store(randomString(64))
		return nil
	}
	return fmt.Errorf("random id and secret still conflict after %v retries", retries)
}

// Auth 验证是不是 api server 生成的 id 和 secret
func (s *Server) Auth(id string, secret string) (ok bool) {
	ok = id == s.ID() && secret == s.Secret()
	return
}

func (s *Server) check(writer http.ResponseWriter) (err error) {
	s.checkTunnelMtx.Lock()
	defer s.checkTunnelMtx.Unlock()

	err = s.randomIDSecret()
	if err != nil {
		return
	}
	defer func() {
		s.id.Store("")
		s.secret.Store("")
	}()

	if !s.statusRespCacheTime.IsZero() && time.Now().Sub(s.statusRespCacheTime) <= 3*time.Minute {
		_, err = s.statusRespCacheBody.Seek(0, io.SeekStart)
		if err != nil {
			return
		}
		s.statusRespCache.Body = io.NopCloser(s.statusRespCacheBody)
		err = s.statusRespCache.Write(writer)
		return
	}

	cArgs := []string{
		"client",
		"-id", s.ID(),
		"-secret", s.Secret(),
		"-local", "http://" + s.Addr,
		"-remote", s.RemoteSchema + s.RemoteAddr,
		"-logLevel", "info",
	}
	c, err := client.New(cArgs)
	if err != nil {
		return
	}
	err = c.Start()
	if err != nil {
		return
	}
	defer c.Close()
	dialFn := func(ctx context.Context, network string, address string) (net.Conn, error) {
		return (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext(ctx, network, s.RemoteAddr)
	}
	httpClient := &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dialFn,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			DisableKeepAlives:     true,
		},
	}
	err = c.WaitUntilReady(30 * time.Second)
	if err != nil {
		return
	}
	var url string
	switch s.RemoteSchema {
	case "tcp://":
		url = fmt.Sprintf("http://%v.example.com/statusResp", s.ID())
	case "tls://":
		url = fmt.Sprintf("https://%v.example.com/statusResp", s.ID())
	}
	resp, err := httpClient.Get(url)
	if err != nil {
		return
	}
	bs, err := io.ReadAll(resp.Body)
	if err != nil {
		_ = resp.Body.Close()
		return
	}
	err = resp.Body.Close()
	if err != nil {
		return
	}
	reader := bytes.NewReader(bs)
	resp.Body = io.NopCloser(reader)
	s.statusRespCache = *resp
	s.statusRespCacheBody = reader
	s.statusRespCacheTime = time.Now()
	err = resp.Write(writer)
	return
}
