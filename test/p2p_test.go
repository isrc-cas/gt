package test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/isrc-cas/gt/util"
	"github.com/pion/webrtc/v3"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestP2P(t *testing.T) {
	t.Parallel()
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
	serverAddr := net.JoinHostPort("localhost", util.RandomPort())
	stunAddr := net.JoinHostPort("localhost", util.RandomPort())
	s, c, _ := setupServerAndClient(t, "", []string{
		"server",
		"-addr", serverAddr,
		"-stunAddr", stunAddr,
	}, []string{
		"client",
		"-id", "abc",
		"-secret", "eec1eabf-2c59-4e19-bf10-34707c17ed89",
		"-local", fmt.Sprintf("http://%s", hsu),
		"-remote", serverAddr,
		"-remoteSTUN", "stun:" + stunAddr,
		"-logLevel", "debug",
	})
	defer func() {
		c.Close()
		s.Close()
	}()
	httpClient := setupHTTPClient(serverAddr, nil)

	pc, ctx, offer, candidates := initOffer(t, stunAddr)

	req, err := http.NewRequest("X1", "http://abc.p2p.com/test", nil)
	if err != nil {
		t.Fatal(err)
	}
	b := &bytes.Buffer{}
	req.Body = ioutil.NopCloser(b)
	req.ContentLength = -1
	sdp, err := json.Marshal(offer)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("X1 sdp: %s", sdp)
	sdpLen := uint16(len(sdp))
	_, err = b.Write(append([]byte{byte(sdpLen >> 8), byte(sdpLen)}, sdp...))
	if err != nil {
		t.Fatal(err)
	}
	for candidate := range candidates {
		cj, err := json.Marshal(candidate.ToJSON())
		if err != nil {
			panic(err)
		}
		l := uint16(len(cj))
		t.Logf("candidate: %s", cj)
		b.Write(append([]byte{byte(l >> 8), byte(l)}, cj...))
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatal("invalid status code")
	}

	var answer webrtc.SessionDescription
	var dataLen uint16
	err = binary.Read(resp.Body, binary.BigEndian, &dataLen)
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 4096)
	_, err = resp.Body.Read(data[:dataLen])
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("X1 sdp: %s", data[:dataLen])
	err = json.Unmarshal(data[:dataLen], &answer)
	if err != nil {
		t.Fatal(err)
	}

	if err := pc.SetRemoteDescription(answer); err != nil {
		t.Fatal(err)
	}

	for {
		err = binary.Read(resp.Body, binary.BigEndian, &dataLen)
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		_, err = resp.Body.Read(data[:dataLen])
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		t.Logf("X1 candidate: %s", data[:dataLen])
		var candidate webrtc.ICECandidateInit
		err = json.Unmarshal(data[:dataLen], &candidate)
		if err != nil {
			t.Fatal(err)
		}
		err = pc.AddICECandidate(candidate)
		if err != nil {
			t.Fatal(err)
		}
	}

	<-ctx.Done()
	if ctx.Err() != context.Canceled {
		t.Fatal("invalid context")
	}
	t.Log("X1 done")
	s.Shutdown()
}

func initOffer(t *testing.T, addr string) (*webrtc.PeerConnection, context.Context, webrtc.SessionDescription, chan *webrtc.ICECandidate) {
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{fmt.Sprintf("stun:%s", addr)},
			},
		},
	}
	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		t.Fatal(err)
	}

	c := make(chan *webrtc.ICECandidate)
	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate == nil {
			close(c)
			return
		}
		c <- candidate
	})

	sendChannel, err := pc.CreateDataChannel("test", nil)
	if err != nil {
		t.Fatal(err)
	}
	sendChannel.OnClose(func() {
		fmt.Println("sendChannel has closed")
	})
	sendChannel.OnOpen(func() {
		fmt.Println("sendChannel has opened")
		if err := sendChannel.SendText("Hello"); err != nil {
			fmt.Println(err)
		}
	})
	ctx, cancelFunc := context.WithTimeout(context.Background(), time.Second*30)
	sendChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
		fmt.Printf("Message from DataChannel %s payload %s\n", sendChannel.Label(), string(msg.Data))
		if string(msg.Data) == "Hello" {
			fmt.Println("sendChannel has received Hello")
			cancelFunc()
		}
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}

	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		fmt.Println(state)
	})

	return pc, ctx, *pc.LocalDescription(), c
}
