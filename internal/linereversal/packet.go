package linereversal

import (
	"fmt"
	"net/netip"
	"time"
)

type Endpoint interface {
	AddrPort() netip.AddrPort
}

type Packet interface {
	Endpoint() Endpoint
	SessionId() SessionId
	String() string
}

type OutgoingPacket struct {
	Packet
	spawned     time.Time
	lastSend    time.Time
	forceRemove bool
}

func (o OutgoingPacket) ShouldSend(retryInterval time.Duration) bool {
	return time.Since(o.lastSend) >= retryInterval
}

func (o OutgoingPacket) ShouldTimeout(timeout time.Duration) bool {
	return time.Since(o.spawned) >= timeout
}

type BasePacket struct {
	endpoint  Endpoint
	sessionId SessionId
}

func (o BasePacket) Endpoint() Endpoint {
	return o.endpoint
}

func (o BasePacket) SessionId() SessionId {
	return o.sessionId
}

type PacketConnect struct {
	BasePacket
}

func (o PacketConnect) String() string {
	return fmt.Sprintf("/connect/%d/", o.sessionId)
}

type PacketData struct {
	BasePacket
	Pos  int32
	Data []byte
}

func (o PacketData) String() string {
	return fmt.Sprintf("/data/%d/%d/%s/", o.sessionId, o.Pos, string(o.Data))
}

type PacketAck struct {
	BasePacket
	Length int32
}

func (o PacketAck) String() string {
	return fmt.Sprintf("/ack/%d/%d/", o.sessionId, o.Length)
}

type PacketClose struct {
	BasePacket
}

func (o PacketClose) String() string {
	return fmt.Sprintf("/close/%d/", o.sessionId)
}
