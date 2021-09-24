package server

import (
	"bytes"
	"testing"
)

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
