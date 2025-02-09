package linereversal

import (
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxMessageSize = 999

type Server struct {
	buf           []byte
	conn          *net.UDPConn
	sessions      map[SessionId]*Session
	sessionsMutex sync.Mutex
}

func newServer(conn *net.UDPConn) Server {
	return Server{
		buf:      make([]byte, maxMessageSize),
		conn:     conn,
		sessions: make(map[SessionId]*Session),
	}
}

func split(s string) []string {
	var parts []string
	start := 1
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '/' && i > 0 && s[i-1] != '\\' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return parts
}

func (o *Server) ReadPacket() (Packet, error) {
	n, addr, err := o.conn.ReadFromUDP(o.buf)
	if err != nil {
		return nil, fmt.Errorf("read packet: %w", err)
	}
	log.Printf("o.conn.ReadFromUDP: n=%d", n)
	data := o.buf[:n]
	rawData := strings.TrimSuffix(string(data), "\x00")
	parts := split(rawData)
	if len(rawData) == 0 {
		return nil, fmt.Errorf("%w: zero-sized packet", ErrMalformedPacket)
	}
	if rawData[0] != '/' || rawData[len(rawData)-1] != '/' {
		return nil, fmt.Errorf("%w: %q does not start and end with '/'", ErrMalformedPacket, rawData)
	}
	if len(parts) < 2 {
		return nil, fmt.Errorf("%w: less than 2 parts", ErrMalformedPacket)
	}
	sessionId, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformedPacket, err)
	}
	base := BasePacket{
		endpoint:  addr,
		sessionId: SessionId(sessionId),
	}
	log.Println(sessionId, "received", parts[0])
	switch parts[0] {
	case "connect":
		if len(parts) != 2 {
			return nil, fmt.Errorf("%w: PacketConnect too many parts: %d, expected 2", ErrMalformedPacket, len(parts))
		}
		return PacketConnect{
			BasePacket: base,
		}, nil
	case "data":
		if len(parts) != 4 {
			return nil, fmt.Errorf("%w: PacketData too many parts: %d, expected 4", ErrMalformedPacket, len(parts))
		}
		pos, err := strconv.Atoi(parts[2])
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrMalformedPacket, err)
		}
		data := []byte(unescaper.Replace(parts[3]))
		if len(data) >= 1000 {
			return nil, fmt.Errorf("%w: damn boi you hung (packet too large)", ErrMalformedPacket)
		}
		return PacketData{
			BasePacket: base,
			Pos:        int32(pos),
			Data:       data,
		}, nil
	case "ack":
		if len(parts) != 3 {
			return nil, fmt.Errorf("%w: PacketAck too many parts: %d, expected 3", ErrMalformedPacket, len(parts))
		}
		length, err := strconv.Atoi(parts[2])
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrMalformedPacket, err)
		}
		return PacketAck{
			BasePacket: base,
			Length:     int32(length),
		}, nil
	case "close":
		if len(parts) != 2 {
			return nil, fmt.Errorf("%w: PacketClose too many parts: %d, expected 2", ErrMalformedPacket, len(parts))
		}
		return PacketClose{
			BasePacket: base,
		}, nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrInvalidPacketType, parts[1])
	}
}

func (o *Server) Start() {
	packets := make(chan Packet, 100)
	go func() {
		for {
			packet, err := o.ReadPacket()
			if err != nil {
				log.Printf("error: %+v", err)
			}
			// shortcircuit close requests
			if packet != nil {
				if _, isClosePacket := packet.(PacketClose); isClosePacket {
					_, _, err := o.getSession(packet.SessionId()).conn.WriteMsgUDPAddrPort([]byte(packet.String()), nil, packet.Endpoint().AddrPort())
					if err != nil {
						log.Println("asdfasdfasdf", err)
					}
				} else {
					packets <- packet
				}
			}
		}
	}()

	for packet := range packets {
		log.Println("Processing packet")
		session := o.getSession(packet.SessionId())
		session.incoming <- packet
		time.Sleep(25 * time.Millisecond)
	}
}

func (o *Server) getSession(sessionId SessionId) *Session {
	o.sessionsMutex.Lock()
	defer o.sessionsMutex.Unlock()
	if session, ok := o.sessions[sessionId]; ok {
		return session
	}
	session := newSession(o.conn, sessionId)
	o.sessions[sessionId] = session
	go session.Start()
	return session
}
