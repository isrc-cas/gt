package server

import (
	"bytes"
	"errors"
	"github.com/isrc-cas/gt/bufio"
	"io"
	"math/rand"
	"strings"
	"testing"
)

const headerTargetPrefix = "Target-ID:"

func TestPeekHostReader(t *testing.T) {
	text := "GET / HTTP/1.1\r\n" +
		"Host: localhost\r\n" +
		"User-Agent: curl/7.64.1\r\n" +
		"Accept: */*"
	host, err := peekHost(bufio.NewReader(strings.NewReader(text)))
	if err != nil {
		t.Fatal(err)
	}
	if len(host) < 1 || string(host) != "localhost" {
		t.Fatal()
	}
	t.Logf("host: %s", host)
}

func TestPeekTargetReader(t *testing.T) {
	text := "GET / HTTP/1.1\r\n" +
		"Host: localhost\r\n" +
		"Target-ID: target.localhost\r\n" +
		"User-Agent: curl/7.64.1\r\n" +
		"Accept: */*"
	target, err := peekHeader(bufio.NewReader(strings.NewReader(text)), headerTargetPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if len(target) < 1 || string(target) != "target.localhost" {
		t.Fatal()
	}
	t.Logf("target: %s", target)
}

func TestPeekHostReaderError(t *testing.T) {
	text := "GET / HTTP/1.1\r\n" +
		"Host: \r\n" +
		"User-Agent: curl/7.64.1\r\n" +
		"Accept: */*"
	host, err := peekHost(bufio.NewReader(strings.NewReader(text)))
	if err == nil {
		t.Fatal()
	}
	t.Logf("host: %s", host)
}

func TestPeekHostReaderNoHost(t *testing.T) {
	text := "GET / HTTP/1.1\r\n" +
		"User-Agent: curl/7.64.1\r\n" +
		"Accept: */*"
	value, err := peekHost(bufio.NewReader(strings.NewReader(text)))
	if err == nil {
		t.Fatal()
	}
	t.Logf("value: %s, err: %s", value, err)
	value, err = peekHeader(bufio.NewReader(strings.NewReader(text)), headerTargetPrefix)
	if err == nil {
		t.Fatal()
	}
	t.Logf("value: %s, err: %s", value, err)
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
	host, err := peekHost(bufio.NewReaderSize(strings.NewReader(text), 20*1024))
	if err == nil || !errors.Is(err, ErrInvalidHTTPProtocol) {
		t.Fatal()
	}
	t.Logf("host: %s, err: %s", host, err)
	text = "GET / HTTP/1.1\r\n\r\n" + text
	host, err = peekHost(bufio.NewReaderSize(strings.NewReader(text), 20*1024))
	if err == nil || !errors.Is(err, io.EOF) {
		t.Fatal()
	}
	t.Logf("host: %s, err: %s", host, err)
}

func TestParseTokenFromHost(t *testing.T) {
	id, err := parseIDFromHost([]byte("id"))
	if err == nil {
		t.Fatal("invalid id should returns error")
	}
	t.Log(id, err)
	id, err = parseIDFromHost([]byte("id.com"))
	if err == nil {
		t.Fatal("invalid id should returns error")
	}
	t.Log(id, err)
	id, err = parseIDFromHost([]byte("abc.id.com"))
	if err != nil {
		t.Fatal("invalid id should not returns error", err)
	}
	if bytes.Compare(id, []byte("abc")) != 0 {
		t.Fatal("only 'abc' should be returned")
	}
	t.Logf("%s", id)
}

func BenchmarkParseTokenFromHost(b *testing.B) {
	host := []byte("abc.id.com")
	var id []byte
	var err error
	for i := 0; i < b.N; i++ {
		id, err = parseIDFromHost(host)
		if err != nil {
			b.Fatal("invalid id should not returns error", err)
		}
	}
	_ = id
}
