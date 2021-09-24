package client

import (
	"testing"
	"time"
)

func TestClientWaitUntilReady(t *testing.T) {
	c, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	err = c.WaitUntilReady(2 * time.Second)
	if err != errTimeout {
		t.Fatal("err != timeout")
	}
	go func() {
		time.Sleep(time.Second)
		c.addTunnel(&conn{})
	}()
	err = c.WaitUntilReady(30 * time.Second)
	if err == errTimeout {
		t.Fatal("err == timeout")
	}
}
