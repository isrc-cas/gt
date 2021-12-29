package test

import (
	"errors"
	"fmt"
	"github.com/isrc-cas/gt/client"
	"github.com/isrc-cas/gt/util"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestFailToDialLocalServer(t *testing.T) {
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

	port := util.RandomPort()
	hsu := net.JoinHostPort("localhost", port)
	s, c, serverAddr := setupServerAndClient(t, fmt.Sprintf("http://%s", hsu), nil, nil)
	defer func() {
		c.Close()
		s.Close()
	}()
	client.OnTunnelClose.Store(func() {
		panic("tunnel should not be closed")
	})
	httpClient := setupHTTPClient(serverAddr)
	resp, err := httpClient.Get("http://05797ac9-86ae-40b0-b767-7a41e03a5486.example.com/test?hello=world")
	if err == nil {
		t.Fatal("should failed to connect")
	}
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

	resp, err = httpClient.Get("http://05797ac9-86ae-40b0-b767-7a41e03a5486.example.com/test?hello=world")
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
	client.OnTunnelClose.Store(func() {})
	t.Logf("%s", all)
	s.Shutdown()
}

func TestInCompleteHTTPReqToServer(t *testing.T) {
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
	port := util.RandomPort()
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

	s, c, serverAddr := setupServerAndClient(t, fmt.Sprintf("http://%s", hsu), nil, nil)
	defer func() {
		c.Close()
		s.Close()
	}()
	client.OnTunnelClose.Store(func() {
		panic("tunnel should not be closed")
	})

	conn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Write([]byte("GET "))
	if err != nil {
		t.Fatal(err)
	}
	//err = conn.Close()
	//if err != nil {
	//	t.Fatal(err)
	//}
	time.Sleep(12 * time.Second)

	httpClient := setupHTTPClient(serverAddr)
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
	client.OnTunnelClose.Store(func() {})
	t.Logf("%s", all)
	s.Shutdown()
}
