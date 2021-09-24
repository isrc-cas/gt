package server

import (
	"github.com/isrc-cas/gt/bufio"
	"math/rand"
	"strings"
	"testing"
)

func TestPeekHostReader(t *testing.T) {
	text := "GET / HTTP/1.1\r\n" +
		"Host: localhost\r\n" +
		"User-Agent: curl/7.64.1\r\n" +
		"Accept: */*"
	r := &peekHostReader{reader: bufio.NewReader(strings.NewReader(text))}
	host, err := r.PeekHost()
	if err != nil {
		t.Fatal(err)
	}
	if len(host) < 1 {
		t.Fatal()
	}
	t.Logf("host: %s", host)
}

func TestPeekHostReaderError(t *testing.T) {
	text := "GET / HTTP/1.1\r\n" +
		"Host: \r\n" +
		"User-Agent: curl/7.64.1\r\n" +
		"Accept: */*"
	r := &peekHostReader{reader: bufio.NewReader(strings.NewReader(text))}
	host, err := r.PeekHost()
	if err == nil {
		t.Fatal()
	}
	t.Logf("host: %s", host)
}

func TestPeekHostReaderNoHost(t *testing.T) {
	text := "GET / HTTP/1.1\r\n" +
		"User-Agent: curl/7.64.1\r\n" +
		"Accept: */*"
	r := &peekHostReader{reader: bufio.NewReader(strings.NewReader(text))}
	host, err := r.PeekHost()
	if err == nil {
		t.Fatal()
	}
	t.Logf("host: %s, err: %s", host, err)
}

var defaultLetters = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")

func randomString(n int, allowedChars ...[]rune) string {
	var letters []rune

	if len(allowedChars) == 0 {
		letters = defaultLetters
	} else {
		letters = allowedChars[0]
	}

	b := make([]rune, n)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}

	return string(b)
}

func TestPeekHostReaderInvalidHeaders(t *testing.T) {
	text := randomString(17 * 1024)
	r := &peekHostReader{reader: bufio.NewReaderSize(strings.NewReader(text), 20*1024)}
	host, err := r.PeekHost()
	if err == nil {
		t.Fatal()
	}
	t.Logf("host: %s, err: %s", host, err)
}
