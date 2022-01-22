package server

import (
	"encoding/binary"
	"errors"

	"github.com/isrc-cas/gt/bufio"
)

func peekTLSHost(reader *bufio.Reader) ([]byte, error) {
	_, err := reader.Peek(42) // 保证 Client Hello 已经被缓冲，不然 bufLen 可能为 0
	bufLen := reader.Buffered()
	buf, err := reader.Peek(bufLen)
	if err != nil {
		return nil, err
	}
	bufIndex := 0

	// 判断 Record Layer 是否是 Handshake
	if bufIndex+1 > bufLen {
		return nil, errors.New("failed to read Record Layer Type")
	}
	recordLayerType := buf[bufIndex]
	bufIndex++
	if recordLayerType != 22 {
		return nil, errors.New("Record Layer is not Handshake")
	}
	bufIndex += 2 + 2 // Record Layer Version, recordLayerLen

	// 判断 Handshake Type 是否是 Client Hello
	if bufIndex+1 > bufLen {
		return nil, errors.New("failed to read Handshake Type")
	}
	handshakeType := buf[bufIndex]
	bufIndex++
	if handshakeType != 1 {
		return nil, errors.New("Handshake Type is not Client Hello")
	}
	bufIndex += 3 + 2 + 32 // Handshake Length, Handshake Version, Handshake Random
	if bufIndex+1 > bufLen {
		return nil, errors.New("failed to read Session ID Length")
	}
	sessionIDLen := buf[bufIndex]
	bufIndex += 1 + int(sessionIDLen)
	if bufIndex+2 > bufLen {
		return nil, errors.New("failed to read Cipher Suites Length")
	}
	cipherSuitesLen := binary.BigEndian.Uint16(buf[bufIndex:])
	bufIndex += 2 + int(cipherSuitesLen)
	if bufIndex+1 > bufLen {
		return nil, errors.New("failed to read Compression Methods Length")
	}
	compressionMethodsLen := buf[bufIndex]
	bufIndex += 1 + int(compressionMethodsLen)
	if bufIndex+2 > bufLen {
		return nil, errors.New("failed to read Extensions Length")
	}
	extensionsLen := int(binary.BigEndian.Uint16(buf[bufIndex:]))
	bufIndex += 2

	// 遍历 Extensions
	for extensionsLen > 0 {
		if bufIndex+2 > bufLen {
			return nil, errors.New("failed to read Extension Type")
		}
		extensionType := binary.BigEndian.Uint16(buf[bufIndex:])
		bufIndex += 2
		extensionsLen -= 2
		if bufIndex+2 > bufLen {
			return nil, errors.New("failed to read Extension Length")
		}
		extensionLen := binary.BigEndian.Uint16(buf[bufIndex:])
		bufIndex += 2
		extensionsLen -= 2
		// 判断 Extension Type 是否是 Server Name Indication
		if extensionType != 0 {
			bufIndex += int(extensionLen)
			extensionsLen -= int(extensionLen)
			continue
		}
		bufIndex += 2 // Sever Name List Length
		extensionsLen -= 2
		if bufIndex+1 > bufLen {
			return nil, errors.New("failed to read Server Name Type")
		}
		serverNameType := buf[bufIndex]
		bufIndex++
		extensionsLen--
		// 判断 Server Name Type 是否是 host_name
		if serverNameType != 0 {
			return nil, errors.New("Server Name Type is not host_name")
		}
		if bufIndex+2 > bufLen {
			return nil, errors.New("failed to read Server Name Length")
		}
		serverNameLen := binary.BigEndian.Uint16(buf[bufIndex:])
		bufIndex += 2
		extensionsLen -= 2
		if bufIndex+int(serverNameLen) > bufLen {
			return nil, errors.New("failed to read Server Name")
		}
		serverName := buf[bufIndex : bufIndex+int(serverNameLen)]
		return serverName, nil
	}
	return nil, errors.New("failed to read Server Name Indication")
}
