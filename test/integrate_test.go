package test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/buger/jsonparser"
	"github.com/gorilla/websocket"
	"github.com/isrc-cas/gt/client"
	"github.com/isrc-cas/gt/server"
)

func randomPort() string {
	n := rand.Int31n(10000)
	n += 10000
	return strconv.FormatInt(int64(n), 10)
}

func setupServerAndClient(t *testing.T, local string) (*server.Server, *client.Client, string) {
	if local == "" {
		local = "http://www.baidu.com"
	}
	port := randomPort()
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

	port := randomPort()
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
	upgrader := websocket.Upgrader{} // use default options

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

	port := randomPort()
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
	port := randomPort()
	apiAddr := net.JoinHostPort("localhost", port)
	port = randomPort()
	sArgs := []string{
		"server",
		"-addr", port,
		"-apiAddr", apiAddr,
		"-id", "status", // 这里的 id secret 可以随便设置，因为 API 使用的 id secret 每次使用都会随机生成，但服务端的 users 不能为空
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
	resp, err := httpClient.Get("http://status.example.com/status") // 只要路径是 /status 就行，域名不需要与上面的设置相同
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

func TestAuthAPI(t *testing.T) {
	// 模拟 AuthAPI
	authAddr := "localhost:" + randomPort()
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(rw http.ResponseWriter, r *http.Request) {
			all, err := io.ReadAll(r.Body)
			if err != nil {
				panic(err) // 不允许使用 t.Fatal()
			}
			id, _ := jsonparser.GetString(all, "clientId")
			secret, _ := jsonparser.GetString(all, "secretKey")
			if id != "05797ac9-86ae-40b0-b767-7a41e03a5486" || secret != "eec1eabf-2c59-4e19-bf10-34707c17ed89" {
				err = errors.New("invalid id or secret")
				panic(err)
			}
			_, err = rw.Write([]byte("{\"result\":true}"))
			if err != nil {
				panic(err)
			}
		})
		httpServer := http.Server{
			Addr:    authAddr,
			Handler: mux,
		}
		if err := httpServer.ListenAndServe(); err != nil {
			panic(err)
		}
	}()
	time.Sleep(100 * time.Millisecond)

	// 启动服务端
	serverAddr := "localhost:" + randomPort()
	sArgs := []string{
		"server",
		"-addr", serverAddr,
		"-authAPI=http://" + authAddr + "/",
	}
	s, err := server.New(sArgs)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Start()
	if err != nil {
		t.Fatal(err)
	}

	// 启动客户端
	cArgs := []string{
		"client",
		"-id", "05797ac9-86ae-40b0-b767-7a41e03a5486",
		"-secret", "eec1eabf-2c59-4e19-bf10-34707c17ed89",
		"-local=http://www.baidu.com",
		"-remote", serverAddr,
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

	// 通过 http 测试
	httpClient := setupHTTPClient(serverAddr)
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
