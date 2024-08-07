package client

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/colin1989/battery/net/acceptor"
	"github.com/gorilla/websocket"

	"github.com/colin1989/battery"

	"github.com/colin1989/battery/blog"
	"github.com/colin1989/battery/facade"
	"github.com/colin1989/battery/net/codec"
	"github.com/colin1989/battery/net/message"
	"github.com/colin1989/battery/net/packet"
	"github.com/colin1989/battery/util/compression"
)

// HandshakeSys struct
type HandshakeSys struct {
	Dict       map[string]uint16 `json:"dict"`
	Heartbeat  int               `json:"heartbeat"`
	Serializer string            `json:"serializer"`
}

// HandshakeData struct
type HandshakeData struct {
	Code int          `json:"code"`
	Sys  HandshakeSys `json:"sys"`
}

type pendingRequest struct {
	msg    *message.Message
	sentAt time.Time
}

// Client struct
type Client struct {
	conn                net.Conn
	Connected           bool
	packetEncoder       facade.PacketEncoder
	packetDecoder       facade.PacketDecoder
	packetChan          chan *packet.Packet
	IncomingMsgChan     chan *message.Message
	pendingChan         chan bool
	pendingRequests     map[uint]*pendingRequest
	pendingReqMutex     sync.Mutex
	requestTimeout      time.Duration
	closeChan           chan struct{}
	nextID              uint32
	messageEncoder      facade.MessageEncoder
	clientHandshakeData *packet.HandshakeData
}

// MsgChannel return the incoming message channel
func (c *Client) MsgChannel() chan *message.Message {
	return c.IncomingMsgChan
}

// ConnectedStatus return the connection status
func (c *Client) ConnectedStatus() bool {
	return c.Connected
}

// New returns a new client
func New(loglevel slog.Level, requestTimeout ...time.Duration) *Client {
	//blog.NewLogger()

	reqTimeout := 5 * time.Second
	if len(requestTimeout) > 0 {
		reqTimeout = requestTimeout[0]
	}

	client := &Client{
		Connected:       false,
		messageEncoder:  message.NewMessagesEncoder(true),
		packetDecoder:   codec.NewPomeloPacketDecoder(),
		packetEncoder:   codec.NewPomeloPacketEncoder(),
		packetChan:      make(chan *packet.Packet, 10),
		pendingRequests: make(map[uint]*pendingRequest),
		pendingReqMutex: sync.Mutex{},
		requestTimeout:  reqTimeout,
		// 30 here is the limit of inflight messages
		// TODO this should probably be configurable
		pendingChan: make(chan bool, 30),
		clientHandshakeData: &packet.HandshakeData{
			Sys: packet.HandshakeClientData{
				Platform:    "mac",
				LibVersion:  "0.3.5-release",
				BuildNumber: "20",
				Version:     "2.1",
			},
			User: map[string]interface{}{
				"age": 30,
			},
		},
	}

	return client
}

// SetClientHandshakeData sets the data to send inside handshake
func (c *Client) SetClientHandshakeData(data *packet.HandshakeData) {
	c.clientHandshakeData = data
}

func (c *Client) sendHandshakeRequest() error {
	enc, err := json.Marshal(c.clientHandshakeData)
	if err != nil {
		return err
	}

	p, err := c.packetEncoder.Encode(packet.Handshake, enc)
	if err != nil {
		return err
	}

	_, err = c.conn.Write(p)
	return err
}

func (c *Client) handleHandshakeResponse() error {
	buf := bytes.NewBuffer(nil)
	packes, err := c.readPackets(buf)
	if err != nil {
		return err
	}

	handshakePacket := packes[0]
	if handshakePacket.Type != packet.Handshake {
		return fmt.Errorf("got first packet from server that is not a handshake, aborting")
	}

	handshake := &HandshakeData{}
	if compression.IsCompressed(handshakePacket.Data) {
		handshakePacket.Data, err = compression.InflateData(handshakePacket.Data)
		if err != nil {
			return err
		}
	}

	err = json.Unmarshal(handshakePacket.Data, handshake)
	if err != nil {
		return err
	}

	blog.Debug("got handshake from sv", slog.Any("data", handshake))

	if handshake.Sys.Dict != nil {
		message.SetDictionary(handshake.Sys.Dict)
	}
	p, err := c.packetEncoder.Encode(packet.HandshakeAck, []byte{})
	if err != nil {
		return err
	}
	_, err = c.conn.Write(p)
	if err != nil {
		return err
	}

	c.Connected = true

	go c.sendHeartbeats(handshake.Sys.Heartbeat)
	go c.handleServerMessages()
	go c.handlePackets()
	go c.pendingRequestsReaper()

	return nil
}

// pendingRequestsReaper delete timeout requests
func (c *Client) pendingRequestsReaper() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			toDelete := make([]*pendingRequest, 0)
			c.pendingReqMutex.Lock()
			for _, v := range c.pendingRequests {
				if time.Now().Sub(v.sentAt) > c.requestTimeout {
					toDelete = append(toDelete, v)
				}
			}
			for _, pendingReq := range toDelete {
				err := battery.Error(errors.New("request timeout"), "PIT-504")
				errMarshalled, _ := json.Marshal(err)
				// send a timeout to incoming msg chan
				m := &message.Message{
					Type:  message.Response,
					ID:    pendingReq.msg.ID,
					Route: pendingReq.msg.Route,
					Data:  errMarshalled,
					Err:   true,
				}
				delete(c.pendingRequests, pendingReq.msg.ID)
				<-c.pendingChan
				c.IncomingMsgChan <- m
			}
			c.pendingReqMutex.Unlock()
		case <-c.closeChan:
			return
		}
	}
}

func (c *Client) handlePackets() {
	for {
		select {
		case p := <-c.packetChan:
			switch p.Type {
			case packet.Data:
				//handle data
				blog.Debug("got", slog.String("data", string(p.Data)))
				m, err := message.Decode(p.Data)
				if err != nil {
					blog.Error("error decoding msg from", slog.String("data", string(p.Data)))
				}
				if m.Type == message.Response {
					c.pendingReqMutex.Lock()
					if _, ok := c.pendingRequests[m.ID]; ok {
						delete(c.pendingRequests, m.ID)
						<-c.pendingChan
					} else {
						c.pendingReqMutex.Unlock()
						continue // do not process msg for already timedout request
					}
					c.pendingReqMutex.Unlock()
				}
				c.IncomingMsgChan <- m
			case packet.Kick:
				blog.Warn("got kick packet from the server! disconnecting...")
				c.Disconnect()
			}
		case <-c.closeChan:
			return
		}
	}
}

func (c *Client) readPackets(buf *bytes.Buffer) ([]*packet.Packet, error) {
	var (
		data = make([]byte, 1024)
		n    = len(data)
		err  error
	)

	for n == len(data) {
		n, err = c.conn.Read(data)
		if err != nil {
			return nil, err
		}
		buf.Write(data[:n])
	}

	packets, err := c.packetDecoder.Decode(buf.Bytes())
	if err != nil {
		blog.Error("error decoding packet from server: %s", blog.ErrAttr(err))
	}

	totalProcessed := 0
	for _, p := range packets {
		totalProcessed += codec.HeadLength + p.Length()
	}
	buf.Next(totalProcessed)

	return packets, nil
}

func (c *Client) handleServerMessages() {
	buf := bytes.NewBuffer(nil)
	defer c.Disconnect()
	for c.Connected {
		packets, err := c.readPackets(buf)
		if err != nil && c.Connected {
			blog.Error(err.Error())
			break
		}

		for _, p := range packets {
			c.packetChan <- p
		}
	}
}

func (c *Client) sendHeartbeats(interval int) {
	t := time.NewTicker(time.Duration(interval) * time.Second)
	defer func() {
		t.Stop()
		c.Disconnect()
	}()
	for {
		select {
		case <-t.C:
			p, _ := c.packetEncoder.Encode(packet.Heartbeat, []byte{})
			_, err := c.conn.Write(p)
			if err != nil {
				blog.Error("error sending heartbeat to server", blog.ErrAttr(err))
				return
			}
		case <-c.closeChan:
			return
		}
	}
}

// Disconnect disconnects the client
func (c *Client) Disconnect() {
	if c.Connected {
		c.Connected = false
		close(c.closeChan)
		c.conn.Close()
	}
}

// ConnectTo connects to the server at addr, for now the only supported protocol is tcp
// if tlsConfig is sent, it connects using TLS
func (c *Client) ConnectTo(addr string, tlsConfig ...*tls.Config) error {
	var conn net.Conn
	var err error
	if len(tlsConfig) > 0 {
		conn, err = tls.Dial("tcp", addr, tlsConfig[0])
	} else {
		conn, err = net.Dial("tcp", addr)
	}
	if err != nil {
		return err
	}
	c.conn = conn
	c.IncomingMsgChan = make(chan *message.Message, 10)

	if err = c.handleHandshake(); err != nil {
		return err
	}

	c.closeChan = make(chan struct{})

	return nil
}

// ConnectToWS connects using webshocket protocol
func (c *Client) ConnectToWS(addr string, path string, tlsConfig ...*tls.Config) error {
	u := url.URL{Scheme: "ws", Host: addr, Path: path}
	dialer := websocket.DefaultDialer

	if len(tlsConfig) > 0 {
		dialer.TLSClientConfig = tlsConfig[0]
		u.Scheme = "wss"
	}

	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		return err
	}

	c.conn = acceptor.NewWSConn(conn)
	if err != nil {
		return err
	}

	c.IncomingMsgChan = make(chan *message.Message, 10)

	if err = c.handleHandshake(); err != nil {
		return err
	}

	c.closeChan = make(chan struct{})

	return nil
}

func (c *Client) handleHandshake() error {
	if err := c.sendHandshakeRequest(); err != nil {
		return err
	}

	if err := c.handleHandshakeResponse(); err != nil {
		return err
	}
	return nil
}

// SendRequest sends a request to the server
func (c *Client) SendRequest(route string, data []byte) (uint, error) {
	return c.sendMsg(message.Request, route, data)
}

// SendNotify sends a notify to the server
func (c *Client) SendNotify(route string, data []byte) error {
	_, err := c.sendMsg(message.Notify, route, data)
	return err
}

func (c *Client) buildPacket(msg message.Message) ([]byte, error) {
	encMsg, err := c.messageEncoder.Encode(&msg)
	if err != nil {
		return nil, err
	}
	p, err := c.packetEncoder.Encode(packet.Data, encMsg)
	if err != nil {
		return nil, err
	}

	return p, nil
}

// sendMsg sends the request to the server
func (c *Client) sendMsg(msgType message.Type, route string, data []byte) (uint, error) {
	id := uint(atomic.AddUint32(&c.nextID, 1))
	messageRoute, err := message.DecodeRoute(route)
	if err != nil {
		return id, err
	}
	// TODO mount msg and encode
	m := message.Message{
		Type:  msgType,
		ID:    id,
		Route: messageRoute,
		Data:  data,
		Err:   false,
	}
	p, err := c.buildPacket(m)
	if msgType == message.Request {
		c.pendingChan <- true
		c.pendingReqMutex.Lock()
		if _, ok := c.pendingRequests[m.ID]; !ok {
			c.pendingRequests[m.ID] = &pendingRequest{
				msg:    &m,
				sentAt: time.Now(),
			}
		}
		c.pendingReqMutex.Unlock()
	}

	if err != nil {
		return m.ID, err
	}
	_, err = c.conn.Write(p)
	return m.ID, err
}
