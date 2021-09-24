package client

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

type fakeConn struct {
	conn io.ReadWriter
}

func (f fakeConn) Read(b []byte) (n int, err error) {
	return f.conn.Read(b)
}

func (f fakeConn) Write(b []byte) (n int, err error) {
	return f.conn.Write(b)
}

func (f fakeConn) Close() error {
	panic("implement me")
}

func (f fakeConn) LocalAddr() net.Addr {
	panic("implement me")
}

func (f fakeConn) RemoteAddr() net.Addr {
	panic("implement me")
}

func (f fakeConn) SetDeadline(time.Time) error {
	panic("implement me")
}

func (f fakeConn) SetReadDeadline(time.Time) error {
	panic("implement me")
}

func (f fakeConn) SetWriteDeadline(time.Time) error {
	panic("implement me")
}

func Test_task_Write(t1 *testing.T) {
	type fields struct {
		host string
	}
	type args struct {
		p []byte
	}
	data := []byte("GET / HTTP/1.1\r\n" +
		"Host: www.baidu.com\r\n" +
		"User-Agent: curl/7.64.1\r\n" +
		"Accept: */*")
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
		result  []byte
	}{
		{
			name: "normal",
			fields: fields{
				host: "localhost",
			},
			args: args{
				p: data,
			},
			wantErr: false,
			result: []byte("GET / HTTP/1.1\r\n" +
				"Host: localhost\r\n" +
				"User-Agent: curl/7.64.1\r\n" +
				"Accept: */*"),
		},
	}
	for _, tt := range tests {
		t1.Run(tt.name, func(t1 *testing.T) {
			var n int
			for i := 1; i <= len(tt.args.p); i++ {
				var err error
				buffer := bytes.NewBuffer(nil)
				t := newHTTPTask(&fakeConn{buffer})
				err = t.setHost(tt.fields.host)
				if err != nil {
					t1.Fatal(err)
				}
				buf := make([]byte, i)
				in := bytes.NewReader(tt.args.p)
				for {
					nr, er := io.ReadFull(in, buf)
					if nr > 0 {
						n, err = t.Write(buf[:nr])
						if err != nil {
							break
						}
						if nr != n {
							t1.Fatalf("%d is expected, but got %d", nr, n)
						}
					}
					if er != nil {
						break
					}
				}
				if (err != nil) != tt.wantErr {
					t1.Errorf("Write() error = %v, wantErr %v", err, tt.wantErr)
					return
				}
				if !bytes.Equal(buffer.Bytes(), tt.result) {
					t1.Errorf("%s is not expected %s", buffer.Bytes(), tt.result)
				}
			}
		})
	}
}
