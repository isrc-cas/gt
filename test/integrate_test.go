package test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/isrc-cas/gt/client"
	"github.com/isrc-cas/gt/server"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func setupServerAndClient(t *testing.T, local string) (*server.Server, *client.Client, string) {
	if local == "" {
		local = "http://www.baidu.com"
	}
	n := rand.Int31n(10000)
	n += 10000
	port := strconv.FormatInt(int64(n), 10)
	sArgs := []string{
		"server",
		"-addr", port,
		"-id", "05797ac9-86ae-40b0-b767-7a41e03a5486",
		"-secret", "eec1eabf-2c59-4e19-bf10-34707c17ed89",
		"-timeout", "10s",
	}
	s, err := server.New(sArgs)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Start()
	if err != nil {
		t.Fatal(err)
	}
	cArgs := []string{
		"client",
		"-id", "05797ac9-86ae-40b0-b767-7a41e03a5486",
		"-secret", "eec1eabf-2c59-4e19-bf10-34707c17ed89",
		"-local", local,
		"-remote", fmt.Sprintf("tcp://localhost:%s", port),
		"-remoteTimeout", "5s",
		"-useLocalAsHTTPHost",
	}
	c, err := client.New(cArgs)
	if err != nil {
		t.Fatal(err)
	}
	err = c.Start()
	if err != nil {
		t.Fatal(err)
	}
	err = c.WaitUntilReady(30 * time.Second)
	if err != nil {
		t.Fatal(err)
	}

	return s, c, port
}

func setupHTTPClient(addr string) *http.Client {
	dialFn := func(ctx context.Context, network string, address string) (net.Conn, error) {
		return (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext(ctx, network, addr)
	}
	httpClient := &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dialFn,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       5 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
	return httpClient
}

func TestServerAndClient(t *testing.T) {
	s, c, port := setupServerAndClient(t, "")
	defer func() {
		c.Close()
		s.Close()
	}()
	httpClient := setupHTTPClient(net.JoinHostPort("localhost", port))
	resp, err := httpClient.Get("http://05797ac9-86ae-40b0-b767-7a41e03a5486.example.com")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatal("invalid status code")
	}
	all, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s", all)
}

func TestClientAndServerWithLocalServer(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/test", func(writer http.ResponseWriter, request *http.Request) {
		err := request.ParseForm()
		if err != nil {
			panic(err)
		}
		if request.FormValue("hello") != "world" {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		_, err = writer.Write([]byte("ok"))
		if err != nil {
			panic(err)
		}
	})
	hs := &http.Server{Handler: mux}

	n := rand.Int31n(10000)
	n += 10000
	port := strconv.FormatInt(int64(n), 10)
	hsu := net.JoinHostPort("localhost", port)
	l, err := net.Listen("tcp", hsu)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := hs.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()
	go func() {
		err := hs.Serve(l)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			panic(err)
		}
	}()
	s, c, port := setupServerAndClient(t, fmt.Sprintf("http://%s", hsu))
	defer func() {
		c.Close()
		s.Close()
	}()
	httpClient := setupHTTPClient(net.JoinHostPort("localhost", port))
	resp, err := httpClient.Get("http://05797ac9-86ae-40b0-b767-7a41e03a5486.example.com/test?hello=world")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatal("invalid status code")
	}
	all, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(all) != "ok" {
		t.Fatal("invalid resp")
	}
	t.Logf("%s", all)
	s.Shutdown()
}

func TestClientAndServerWithLocalWebsocket(t *testing.T) {
	var upgrader = websocket.Upgrader{} // use default options

	echo := func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			panic(err)
		}
		defer func(c *websocket.Conn) {
			err := c.Close()
			if err != nil {
				panic(err)
			}
		}(c)
		for {
			mt, message, err := c.ReadMessage()
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseNoStatusReceived) {
					return
				}
				panic(err)
			}
			log.Printf("recv: %s", message)
			err = c.WriteMessage(mt, message)
			if err != nil {
				panic(err)
			}
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/test", echo)
	hs := &http.Server{Handler: mux}

	n := rand.Int31n(10000)
	n += 10000
	port := strconv.FormatInt(int64(n), 10)
	hsu := net.JoinHostPort("localhost", port)
	l, err := net.Listen("tcp", hsu)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := hs.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()
	go func() {
		err := hs.Serve(l)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			panic(err)
		}
	}()
	s, c, port := setupServerAndClient(t, fmt.Sprintf("http://%s", hsu))
	defer func() {
		c.Close()
		s.Close()
	}()

	dialFn := func(ctx context.Context, network string, address string) (net.Conn, error) {
		address = net.JoinHostPort("localhost", port)
		return (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext(ctx, network, address)
	}
	dialer := &websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 45 * time.Second,
		NetDialContext:   dialFn,
	}
	ws, _, err := dialer.Dial("ws://05797ac9-86ae-40b0-b767-7a41e03a5486.example.com/test", nil)
	if err != nil {
		t.Fatal("dial:", err)
	}

	done := make(chan struct{})
	msg := make(chan string, 1)

	go func() {
		defer close(done)
		for i := 0; i < 3; i++ {
			_, message, err := ws.ReadMessage()
			if err != nil {
				panic(err)
			}
			m := <-msg
			if m != string(message) {
				panic("not equal")
			}
			log.Printf("client recv: %s", message)
		}
	}()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

FOR:
	for {
		select {
		case <-done:
			err := ws.WriteControl(websocket.CloseMessage, nil, time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			err = ws.Close()
			if err != nil {
				t.Fatal(err)
			}
			break FOR
		case tick := <-ticker.C:
			ts := tick.String()
			err := ws.WriteMessage(websocket.TextMessage, []byte(ts))
			if err != nil {
				t.Fatal(err)
			}
			msg <- ts
		}
	}

	s.Shutdown()
}

func TestPing(t *testing.T) {
	s, c, _ := setupServerAndClient(t, "")
	defer func() {
		c.Close()
		s.Close()
	}()
	time.Sleep(20 * time.Second)
	if s.GetTunneling() != 1 {
		t.Fatal("zero tunneling?!")
	}
	s.Shutdown()
}

func TestAPIStatus(t *testing.T) {
	n := rand.Int31n(10000)
	n += 10000
	port := strconv.FormatInt(int64(n), 10)
	apiAddr := net.JoinHostPort("localhost", port)
	n = rand.Int31n(10000)
	n += 10000
	port = strconv.FormatInt(int64(n), 10)
	sArgs := []string{
		"server",
		"-addr", port,
		"-apiAddr", apiAddr,
		"-id", "status",
		"-secret", "status",
	}
	s, err := server.New(sArgs)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	httpClient := setupHTTPClient(apiAddr)
	resp, err := httpClient.Get("http://status.example.com/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatal("invalid status code")
	}
	resp1, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s", resp1)

	time.Sleep(time.Second)
	resp, err = httpClient.Get("http://status.example.com/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatal("invalid status code")
	}
	resp2, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s", resp2)

	if !bytes.Equal(resp1, resp2) {
		t.Fatalf("resp1(%s) != resp2(%s)", resp1, resp2)
	}
	s.Shutdown()
}
