package transport

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"time"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"google.golang.org/protobuf/proto"
)

// TLV（Type-Length-Value）格式 解决粘包拆包问题

// Header TLV header format
// +--------+--------+--------+--------+
// |  magic |  ver   |  type  | length |
// +--------+--------+--------+--------+
//
//	2 byte   1 byte   1 byte   4 bytes
const (
	Magic      = 0x1234      // 魔数，用于标识消息的开始, 固定 0x1234，过滤非法数据包
	HeaderLen  = 8           // TLV 包头总长度：2 + 1 + 1 + 4
	MaxBodyLen = 1024 * 1024 // 最大Protobuf包体1MB，防大包攻击
	Version    = 0x01
)

// TCPTransport TLV format: [Header][Body]
// Header: [Magic(1)][Ver(1)][Type(1)][Length(2BE)]
type TCPTransport struct {
	conn      net.Conn
	mu        sync.Mutex
	packetBuf []byte // 半包缓存：处理TCP沾包/半包
}

func NewTCPTransport(conn net.Conn) *TCPTransport {
	return &TCPTransport{conn: conn}
}

func (c *TCPTransport) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

func (t *TCPTransport) Read(ctx context.Context) (*gateway.ClientSignal, error) {
	// 1. 循环读取，直到拿到完整的TLV包
	for {
		// 先检查半包缓存是否已有完整包头
		if len(t.packetBuf) < HeaderLen {
			readBuf := make([]byte, 4096) // 单次读取 4KB
			n, err := t.conn.Read(readBuf)
			if err != nil {
				return nil, fmt.Errorf("read tcp failed: %w", err)
			}
			t.packetBuf = append(t.packetBuf, readBuf[:n]...)
			continue
		}

		// 解析TLV包头
		magic := binary.BigEndian.Uint16(t.packetBuf[:2])
		if magic != Magic {
			return nil, ErrInvalidMagic
		}
		// 2.2 解析版本号（可选，用于兼容升级）
		// version := c.packetBuf[2]

		// 2.3 解析消息类型
		// msgType := c.packetBuf[3]

		protobufLen := binary.BigEndian.Uint32(t.packetBuf[4:8])
		if protobufLen > MaxBodyLen { // 防止大包攻击
			return nil, fmt.Errorf("protobuf body too large: %d", protobufLen)
		}

		totalPacketLen := HeaderLen + int(protobufLen)
		if len(t.packetBuf) < totalPacketLen { // 包体不完整，继续读取
			continue
		}
		protobufBody := t.packetBuf[HeaderLen:totalPacketLen]
		t.packetBuf = t.packetBuf[totalPacketLen:]

		// 解析 protobuf 消息体
		clientSignal := clientSignalPool.Get().(*gateway.ClientSignal)
		clientSignal.Reset()
		if err := proto.Unmarshal(protobufBody, clientSignal); err != nil {
			clientSignalPool.Put(clientSignal)
			return nil, fmt.Errorf("unmarshal protobuf failed: %w", err)
		}
		return clientSignal, nil
	}
}

func (t *TCPTransport) Write(ctx context.Context, data *gateway.ClientSignal) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Check context before write
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	// 1. 序列化Protobuf
	protobufBody, err := marshalOpts.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal protobuf failed: %w", err)
	}
	// 2. 构造TLV包头
	packet := make([]byte, HeaderLen+len(protobufBody))
	binary.BigEndian.PutUint16(packet[:2], Magic)
	// 填充版本号
	packet[2] = 0x01
	// 填充消息类型
	// packet[3] = byte(gateway.BusinessType_BusinessType_IM)
	// 填充包体长度
	binary.BigEndian.PutUint32(packet[4:8], uint32(len(protobufBody)))
	// 3. 写入包体
	copy(packet[HeaderLen:], protobufBody)
	_, err = t.conn.Write(packet)
	return err
}

func (t *TCPTransport) Close() error {
	return t.conn.Close()
}

func (t *TCPTransport) RemoteAddr() string {
	return t.conn.RemoteAddr().String()
}
