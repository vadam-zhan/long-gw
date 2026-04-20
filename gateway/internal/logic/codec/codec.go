package codec

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"google.golang.org/protobuf/proto"
)

// CodecType identifies the serialization format used for the message payload.
type CodecType byte

const (
	CodecJSON     CodecType = 1
	CodecProtobuf CodecType = 2
	CodecMsgpack  CodecType = 3
)

// // CompressAlgo identifies the compression applied after serialization.
type CompressAlgo byte

const (
	CompressNone   CompressAlgo = 0
	CompressGzip   CompressAlgo = 1
	CompressZstd   CompressAlgo = 2
	CompressSnappy CompressAlgo = 3
)

// frameCodecByte packs both CodecType and CompressAlgo into one byte.
//
//	layout: [compress:4][codec:4]
type frameCodecByte byte

func makeFrameCodecByte(ct CodecType, ca CompressAlgo) frameCodecByte {
	return frameCodecByte((byte(ca) << 4) | byte(ct))
}

func (f frameCodecByte) CodecType() CodecType {
	return CodecType(f & 0x0F)
}

func (f frameCodecByte) CompressAlgo() CompressAlgo {
	return CompressAlgo((f >> 4) & 0x0F)
}

// Codec converts between proto.Message and the byte slice passed to/from Transport.
// Contract:
//   - Encode output format:  [1 byte frameCodecByte][encoded+compressed payload]
//   - Decode input format:   same — Transport.Read() returns this exact slice.
//   - Both methods are safe for concurrent use.
type Codec interface {
	// Decode auto-detects codec and compression from the first byte, then
	// decompresses and deserializes the remaining bytes into a Message.
	Decode(data []byte) (*gateway.Message, error)

	// Encode serializes msg and prepends the frameCodecByte.
	Encode(msg *gateway.Message) ([]byte, error)
}

type FrameCodec struct {
	codecType    CodecType
	compressAlgo CompressAlgo
	gzipPool     sync.Pool // pools *gzip.Writer to reduce GC pressure
}

// Config holds the encoding preferences for a FrameCodec.
type Config struct {
	CodecType    CodecType
	CompressAlgo CompressAlgo
}

// DefaultConfig returns a production-suitable configuration:
// Protobuf encoding with zstd compression.
//
// Use JSONConfig() for development/debugging — human-readable wire dumps.
func DefaultConfig() Config {
	return Config{
		CodecType:    CodecProtobuf,
		CompressAlgo: CompressZstd,
	}
}

func JSONConfig() Config {
	return Config{
		CodecType:    CodecJSON,
		CompressAlgo: CompressNone,
	}
}

func NewCodec(cfg Config) *FrameCodec {
	fc := &FrameCodec{
		codecType:    cfg.CodecType,
		compressAlgo: cfg.CompressAlgo,
	}
	fc.gzipPool = sync.Pool{
		New: func() any {
			w, _ := gzip.NewWriterLevel(nil, gzip.BestSpeed)
			return w
		},
	}
	return fc
}

// Output layout:
//
//	[frameCodecByte (1B)] [compressed serialized payload (N bytes)]
func (fc *FrameCodec) Encode(msg *gateway.Message) ([]byte, error) {
	payload, err := fc.marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("codec: marshal (%v): %w", fc.codecType, err)
	}

	compressed, err := fc.compress(payload)
	if err != nil {
		return nil, fmt.Errorf("codec: compress (%v): %w", fc.compressAlgo, err)
	}

	out := make([]byte, 1+len(compressed))
	out[0] = byte(makeFrameCodecByte(fc.codecType, fc.compressAlgo))
	copy(out[1:], compressed)
	return out, nil
}

func (fc *FrameCodec) Decode(data []byte) (*gateway.Message, error) {
	if len(data) < 1 {
		return nil, fmt.Errorf("codec: frame too short (%d bytes)", len(data))
	}
	cb := frameCodecByte(data[0])
	payload := data[1:]
	decompressed, err := fc.decompress(cb.CompressAlgo(), payload)
	if err != nil {
		return nil, fmt.Errorf("codec: decompress (%v): %w", cb.CompressAlgo(), err)
	}
	msg, err := fc.unmarshal(cb.CodecType(), decompressed)
	if err != nil {
		return nil, fmt.Errorf("codec: unmarshal (%v): %w", cb.CodecType(), err)
	}
	return msg, nil
}

// 使用 proto.MarshalOptions 减少内存分配
var marshalOpts = proto.MarshalOptions{
	UseCachedSize: true,
}

func (fc *FrameCodec) marshal(msg *gateway.Message) ([]byte, error) {
	switch fc.codecType {
	case CodecJSON:
		return json.Marshal(msg)
	case CodecProtobuf:
		return marshalOpts.Marshal(msg)
	default:
		return nil, fmt.Errorf("codec: unsupported codec type %d", fc.codecType)
	}
}

func (fc *FrameCodec) unmarshal(ct CodecType, data []byte) (*gateway.Message, error) {
	msg := new(gateway.Message)
	switch ct {
	case CodecJSON:
		if err := json.Unmarshal(data, msg); err != nil {
			return nil, err
		}
	case CodecProtobuf:
		if err := proto.Unmarshal(data, msg); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("codec: unsupported codec type %d for decode", ct)
	}
	return msg, nil
}

func (fc *FrameCodec) compress(data []byte) ([]byte, error) {
	switch fc.compressAlgo {
	case CompressNone:
		return data, nil

	case CompressGzip:
		var buf bytes.Buffer
		w := fc.gzipPool.Get().(*gzip.Writer)
		w.Reset(&buf)
		if _, err := w.Write(data); err != nil {
			fc.gzipPool.Put(w)
			return nil, err
		}
		if err := w.Close(); err != nil {
			fc.gzipPool.Put(w)
			return nil, err
		}
		fc.gzipPool.Put(w)
		return buf.Bytes(), nil

	case CompressZstd:
		// Production: use github.com/klauspost/compress/zstd
		// Stub: fall through to no compression for compilation.
		// Replace with: enc.EncodeAll(data, nil)
		return data, nil // TODO: replace with real zstd

	case CompressSnappy:
		// Production: use github.com/golang/snappy
		// Stub: return data, nil
		return data, nil // TODO: replace with real snappy

	default:
		return nil, fmt.Errorf("codec: unsupported compress algo %d", fc.compressAlgo)
	}
}

func (fc *FrameCodec) decompress(algo CompressAlgo, data []byte) ([]byte, error) {
	switch algo {
	case CompressNone:
		return data, nil

	case CompressGzip:
		r, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer r.Close()
		return io.ReadAll(r)

	case CompressZstd:
		// TODO: replace with real zstd decoder
		return data, nil

	case CompressSnappy:
		// TODO: replace with real snappy decoder
		return data, nil

	default:
		return nil, fmt.Errorf("codec: unsupported decompress algo %d", algo)
	}
}
