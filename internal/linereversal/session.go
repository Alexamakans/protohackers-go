package linereversal

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type SessionId = int32

type Session struct {
	conn      *net.UDPConn
	addr      *net.UDPAddr
	sessionId SessionId

	readPos int32
	ackPos  atomic.Int32
	sentPos int32

	incoming      chan Packet
	outgoing      []*OutgoingPacket
	outgoingMutex sync.Mutex
	inoutMutex    sync.Mutex

	messages      *bytes.Buffer
	messagesMutex sync.Mutex

	lastSend      time.Time
	retryInterval time.Duration
	timeout       time.Duration

	connected    atomic.Bool
	hasConnected atomic.Bool
}

// what a disgusting function goddamn, returns true if full line, may reallocate the passed in result
// so ALWAYS reassign it to the returned slice when using this
//
// does not return the newline itself
func readUntilNewLineOrNoData(r *bytes.Buffer, result []byte) ([]byte, bool) {
	for {
		b, err := r.ReadByte()
		if err != nil {
			if err == io.EOF {
				// nothing more to read for now
				return result, false
			}
			panic(err)
		}
		if b == '\n' {
			return result, true
		}
		// horrible horrible code
		result = append(result, b)
	}
}

func newSession(conn *net.UDPConn, sessionId SessionId) *Session {
	messages := bytes.NewBuffer(make([]byte, 0, 11000))
	return &Session{
		conn:          conn,
		sessionId:     sessionId,
		incoming:      make(chan Packet, 10000),
		retryInterval: 3 * time.Second,
		timeout:       60 * time.Second,
		messages:      messages,
	}
}

func (o *Session) Start() {
	go o.handleIncoming()
	go o.handleOutgoing()

	for !o.connected.Load() {
		time.Sleep(10 * time.Millisecond)
	}

	for o.connected.Load() {
		message := []byte(o.GetMessage())
		log.Printf("got message: %q", string(message))
		slices.Reverse(message)
		log.Printf("reversed message: %q", string(message))
		o.SendMessage(string(message))
	}
}

func (o *Session) SendMessage(msg string) {
	msg = escaper.Replace(msg)
	chunkSize := maxMessageSize - 100
	numChunks := int(math.Ceil(float64(len(msg)) / float64(chunkSize)))
	chunks := slices.Chunk([]byte(msg), chunkSize)
	pos := o.sentPos
	i := 0
	for chunk := range chunks {
		if i == numChunks-1 {
			if len(chunk) == chunkSize {
				unescapedChunkLen := len(chunk) - strings.Count(string(chunk), "\\\\") - strings.Count(string(chunk), "\\/")
				defer func() {
					o.send(PacketData{
						BasePacket: BasePacket{
							endpoint:  o.addr,
							sessionId: o.sessionId,
						},
						Pos:  pos + int32(unescapedChunkLen),
						Data: []byte("\n"),
					})
					o.inoutMutex.Lock()
					o.sentPos = pos + int32(unescapedChunkLen)
					o.inoutMutex.Unlock()
				}()
			} else {
				chunk = append(chunk, '\n')
			}
		}
		log.Printf("queue send of chunk: %q", string(chunk))
		o.send(PacketData{
			BasePacket: BasePacket{
				endpoint:  o.addr,
				sessionId: o.sessionId,
			},
			Pos:  pos,
			Data: chunk,
		})
		unescapedChunkLen := len(chunk) - strings.Count(string(chunk), "\\\\") - strings.Count(string(chunk), "\\/")
		pos += int32(unescapedChunkLen)
		i++
	}
	o.inoutMutex.Lock()
	o.sentPos = pos
	o.inoutMutex.Unlock()
}

func (o *Session) GetMessage() string {
	var line []byte
	for {
		var fullLine bool
		o.messagesMutex.Lock()
		line, fullLine = readUntilNewLineOrNoData(o.messages, line)
		if fullLine {
			break
		}
		o.messagesMutex.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	defer o.messagesMutex.Unlock()
	remaining := o.messages.Bytes()
	o.messages.Reset()
	_, err := o.messages.Write(remaining)
	if err != nil {
		panic(err)
	}
	return string(line)
}

func (o *Session) handleIncoming() {
	handle := func() bool {
		if o.hasConnected.Load() && !o.connected.Load() {
			return false
		}

		select {
		case packet := <-o.incoming:
			base := BasePacket{
				endpoint:  packet.Endpoint(),
				sessionId: o.sessionId,
			}
			var responsePacket Packet

			o.inoutMutex.Lock()
			o.addr = packet.Endpoint().(*net.UDPAddr)
			switch p := packet.(type) {
			case PacketConnect:
				log.Printf("%q", p.String())
				o.connected.Store(true)
				o.hasConnected.Store(true)
				responsePacket = PacketAck{BasePacket: base, Length: 0}
			case PacketData:
				log.Printf("%q", p.String())
				log.Printf("data length: %d", len(p.Data))
				if len(p.Data) >= 1000 {
					log.Println("TOO BIG")
					break
				}
				if !o.connected.Load() {
					// case 1, not connected but trying to send us data, close
					log.Println("case 1, not connected but receiving data packets, close")
					responsePacket = PacketClose{
						BasePacket: base,
					}
					break
				}
				if o.readPos == p.Pos {
					// case 2, nice path, we got the right data
					log.Println("case 2, got nice data packet")
					if len(p.Data) == 0 {
						log.Println("warning! zero-length data received")
						break
					}
					newReadPos := o.readPos + int32(len(p.Data))
					responsePacket = PacketAck{
						BasePacket: base,
						Length:     newReadPos,
					}
					o.messagesMutex.Lock()
					n, err := o.messages.Write(p.Data)
					o.messagesMutex.Unlock()
					if err != nil {
						panic(err)
					}
					if n != len(p.Data) {
						panic(fmt.Sprintf("wrote %d but expected to write %d bytes", n, len(p.Data)))
					}
					o.readPos = newReadPos
					break
				}
				// case 3, remind client of how much we have read
				if p.Pos > o.readPos {
					log.Println("case 3.1, pos too big")
				} else {
					log.Println("case 3.2, pos too small")
				}
				responsePacket = PacketAck{
					BasePacket: base,
					Length:     o.readPos,
				}
			case PacketAck:
				log.Printf("%q", p.String())
				if !o.connected.Load() {
					// case 1, not connected but thinks we sent data, close
					log.Println("case 1, not connected but thinks we sent data, close")
					responsePacket = PacketClose{
						BasePacket: base,
					}
					break
				}
				if p.Length < o.ackPos.Load() {
					// case 2, ignore possible duplicate ack
					log.Println("case 2, possible duplicate ack, ignore")
					break
				}
				if p.Length > o.sentPos {
					// case 3, client is misbehaving, thinks we have sent more than we have, close
					log.Printf("case 3, client misbehaving, got ack (%d), but we have sent only %d", p.Length, o.sentPos)
					responsePacket = PacketClose{
						BasePacket: base,
					}
					break
				}
				if p.Length < o.sentPos {
					// case 4, ack for less data than we have sent, resend data from Length > sentPos
					// TODO: Idk how to handle this? Would need to keep a history of sent data packets or something
					log.Println("case 4, TODO? ack for less data than we have sent")
					break
				}
				// case 5, nice path, no response
				o.ackPos.Store(o.sentPos)
			case PacketClose:
				log.Printf("%q", p.String())
				responsePacket = PacketClose{
					BasePacket: base,
				}
				o.connected.Store(false)
			}
			o.inoutMutex.Unlock()

			if responsePacket != nil {
				o.send(responsePacket)
			} else {
				log.Println("responsePacket is nil")
			}
			return true
		case <-time.After(30 * time.Second):
			return false
		}
	}

	for handle() {
		time.Sleep(10 * time.Millisecond)
	}
}

func (o *Session) send(packet Packet) {
	if p, ok := packet.(PacketData); ok {
		if len(p.Data) > 1000 {
			panic(fmt.Sprintf("WTF: %+v", p))
		}
	}
	log.Printf("queueing send of %q", packet.String())
	o.outgoingMutex.Lock()
	defer o.outgoingMutex.Unlock()
	o.outgoing = append(o.outgoing, &OutgoingPacket{
		Packet:   packet,
		spawned:  time.Now(),
		lastSend: time.Time{},
	})
}

func (o *Session) handleOutgoing() {
	handle := func() bool {
		o.outgoingMutex.Lock()
		defer o.outgoingMutex.Unlock()
		for _, packet := range o.outgoing {
			// log.Printf("handling outgoing packet: %q", packet.String())
			_, isClosePacket := packet.Packet.(PacketClose)
			if !isClosePacket && !o.connected.Load() {
				continue
			}

			if packet.ShouldSend(o.retryInterval) {
				// log.Printf("should send %q = true", packet.String())
				dataPacket, isDataPacket := packet.Packet.(PacketData)
				if isDataPacket {
					if o.ackPos.Load() >= (dataPacket.Pos + int32(len(dataPacket.Data))) {
						packet.forceRemove = true
						continue
					}
					cont := func() bool {
						o.inoutMutex.Lock()
						defer o.inoutMutex.Unlock()
						if o.sentPos != o.ackPos.Load() && dataPacket.Pos > o.sentPos {
							log.Printf("delaying %q", packet.String())
							// delay if previous packets are not acked yet
							packet.spawned = time.Now()
							return true
						}
						return false
					}()
					if cont {
						continue
					}
				}
				msg := []byte(packet.String())
				if isDataPacket && dataPacket.Pos+int32(len(dataPacket.Data)) > o.sentPos {
					o.inoutMutex.Lock()
					log.Println("updating sentPos to", dataPacket.Pos)
					o.sentPos = dataPacket.Pos + int32(len(dataPacket.Data))
					o.inoutMutex.Unlock()
				} else if isDataPacket {
					log.Printf("%q did not update the sentPos from %d", packet.String(), o.sentPos)
				}
				n, _, err := o.conn.WriteMsgUDPAddrPort(msg, nil, packet.Endpoint().AddrPort())
				if err != nil {
					log.Printf("error handling outgoing: %+v", err)
					continue
				}
				if n != len(msg) {
					panic(fmt.Sprintf("sent too little data, sent %d but expected to send %d", n, len(msg)))
				}
				log.Printf("sent %q", packet.String())
				packet.lastSend = time.Now()

				if !isDataPacket {
					packet.forceRemove = true
				}
			}

			if isClosePacket && !o.connected.Load() {
				return false
			}
		}

		filteredLen := 0
		for _, packet := range o.outgoing {
			if !packet.forceRemove && !packet.ShouldTimeout(o.timeout) {
				o.outgoing[filteredLen] = packet
				filteredLen++
			}
		}
		o.outgoing = o.outgoing[:filteredLen]
		return true
	}
	for handle() {
		time.Sleep(10 * time.Millisecond)
		if o.hasConnected.Load() && !o.connected.Load() {
			log.Printf("CLOSING SESSION %d", o.sessionId)
			break
		}
	}
}
