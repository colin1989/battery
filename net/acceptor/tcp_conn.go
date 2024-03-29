package acceptor

import (
	"io"
	"net"

	"github.com/colin1989/battery/errors"
	"github.com/colin1989/battery/facade"
	"github.com/colin1989/battery/net/codec"
)

var _ facade.Connector = (*TCPConn)(nil)

type TCPConn struct {
	net.Conn
	remoteAddr net.Addr
}

func (tc *TCPConn) RemoteAddr() net.Addr {
	return tc.remoteAddr
}

// GetNextMessage reads the next message available in the stream
func (tc *TCPConn) GetNextMessage() (b []byte, err error) {
	header, err := io.ReadAll(io.LimitReader(tc.Conn, codec.HeadLength))
	if err != nil {
		return nil, err
	}
	// if the header has no data, we can consider it as a closed connection
	if len(header) == 0 {
		return nil, errors.ErrConnectionClosed
	}
	_, size, err := codec.ParseHeader(header)
	if err != nil {
		return nil, err
	}

	data, err := io.ReadAll(io.LimitReader(tc.Conn, int64(size)))
	if err != nil {
		return nil, err
	}
	if len(data) < size {
		return nil, errors.ErrReceivedMsgSmallerThanExpected
	}
	return append(header, data...), nil
}
