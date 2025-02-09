package udp

import (
	"fmt"
	"net"
)

func Listen(ip string, port int) (*net.UDPConn, error) {
	addr := net.UDPAddr{
		Port: port,
		IP:   net.ParseIP(ip),
	}
	conn, err := net.ListenUDP("udp", &addr)
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	return conn, nil
}
