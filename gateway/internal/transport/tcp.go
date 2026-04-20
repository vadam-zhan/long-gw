package transport

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"time"
)

// TLV（Type-Length-Value）格式 解决粘包拆包问题

// Header TLV header format
// +--------+----------+--------+
// |  magic |  length  | codec |
// +--------+----------+--------+
//
//	2 byte    4 bytes   1 byte
const (
	Magic     = 0x1234  // 魔数，用于标识消息的开始, 固定 0x1234，过滤非法数据包
	HeaderLen = 6       // TLV 包头总长度：2 + 4 = 6
	MaxFrame  = 4 << 20 // 最大Protobuf 包体 4 MiB，防大包攻击
)

// TCPTransport TLV format: [Header][Body]
// Header: [Magic(1)][Ver(1)][Type(1)][Length(2BE)]
type TCPTransport struct {
	conn      net.Conn
	mu        sync.Mutex
	packetBuf []byte // 半包缓存：处理TCP沾包/半包
}

// NewTCPTransport creates a new TCP transport
func NewTCPTransport(conn net.Conn) *TCPTransport {
	return &TCPTransport{conn: conn}
}

func (t *TCPTransport) Read(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	for {
		if len(t.packetBuf) < HeaderLen {
			readBuf := make([]byte, 4096)
			n, err := t.conn.Read(readBuf)
			if err != nil {
				return nil, fmt.Errorf("read tcp failed: %w", err)
			}
			t.packetBuf = append(t.packetBuf, readBuf[:n]...)
			continue
		}
		magic := binary.BigEndian.Uint16(t.packetBuf[:2])
		if magic != Magic {
			return nil, ErrInvalidMagic
		}
		protobufLen := binary.BigEndian.Uint32(t.packetBuf[2:6])
		if protobufLen > MaxFrame {
			return nil, fmt.Errorf("protobuf body too large: %d", protobufLen)
		}
		totalPacketLen := HeaderLen + int(protobufLen)
		if len(t.packetBuf) < totalPacketLen { // 包体不完整，继续读取
			continue
		}
		// Return codec byte + payload so the caller can decode correctly.
		full := t.packetBuf[HeaderLen:totalPacketLen]
		t.packetBuf = t.packetBuf[totalPacketLen:]
		return full, nil
	}
}

func (t *TCPTransport) Write(ctx context.Context, data []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// 1. 构造TLV包头
	packet := make([]byte, HeaderLen+len(data))
	binary.BigEndian.PutUint16(packet[:2], Magic)
	// 2. 填充包体长度
	binary.BigEndian.PutUint32(packet[2:6], uint32(len(data)))
	// 3. 写入包体, (codec byte is already included in data)
	copy(packet[HeaderLen:], data)
	_, err := t.conn.Write(packet)
	return err
}

func (t *TCPTransport) Close() {
	t.conn.Close()
}

func (t *TCPTransport) RemoteAddr() string {
	return t.conn.RemoteAddr().String()
}

func (c *TCPTransport) SetReadDeadline(tm time.Time) error {
	return c.conn.SetReadDeadline(tm)
}
