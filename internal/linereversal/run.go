package linereversal

import (
	"errors"
	"log"
	"strings"

	"github.com/Alexamakans/protohackers-go/pkg/udp"
)

var (
	ErrMalformedPacket   = errors.New("malformed packet")
	ErrInvalidPacketType = errors.New("invalid packet type")
)

var (
	escaper   = strings.NewReplacer("\\", "\\\\", "/", "\\/")
	unescaper = strings.NewReplacer("\\\\", "\\", "\\/", "/")
)

func Run() {
	conn, err := udp.Listen("0.0.0.0", 17777)
	if err != nil {
		panic(err)
	}
	log.Println("listening on 0.0.0.0:17777")

	server := newServer(conn)
	server.Start()
}
