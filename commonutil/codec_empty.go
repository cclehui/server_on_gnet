package commonutil

import (
	"github.com/panjf2000/gnet"
)

type CodecEmpty struct{}

func (ce *CodecEmpty) Encode(c gnet.Conn, buf []byte) ([]byte, error) {
	return buf, nil
}

func (ce *CodecEmpty) Decode(c gnet.Conn) ([]byte, error) {
	return nil, nil
}
