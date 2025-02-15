package insecuresocketslayer

import (
	"bufio"
	"errors"
	"log"
	"math/bits"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"
)

func Run() {
	bindAddress, err := net.ResolveTCPAddr("tcp", "0.0.0.0:17777")
	if err != nil {
		panic(err)
	}
	listener, err := net.ListenTCP("tcp", bindAddress)
	if err != nil {
		panic(err)
	}
	log.Println("Listening on 0.0.0.0:17777")

	for {
		conn, err := listener.AcceptTCP()
		if err != nil {
			panic(err)
		}
		log.Printf("Accepted connection %s", conn.RemoteAddr())

		go handle(conn)
	}
}

func handle(conn *net.TCPConn) {
	defer conn.Close()
	decoder := &decoder{}
	session := Session{
		conn: conn,
		r:    bufio.NewReaderSize(decoder, 5000),
	}
	decoder.session = &session
	if err := session.readCipherSpec(); err != nil {
		log.Printf("error reading cipher spec: %+v", err)
		return
	}
	if !session.cipherIsValid() {
		log.Printf("bad cipher spec: %+v", session.cipherSpec)
		return
	}
	session.updateReverseCipherSpec()
	session.Start()
}

type Session struct {
	conn              net.Conn
	cipherSpec        []byte
	reverseCipherSpec []byte
	cipherRecvPos     byte
	cipherSentPos     byte

	decoder *decoder
	r       *bufio.Reader
}

func (o *Session) Start() {
	for {
		start := time.Now()
		startRecv := time.Now()
		line, err := o.recv()
		if line == "" {
			if err != nil {
				log.Printf("readLine: %+v", err)
				return
			}
			continue
		}
		log.Printf("recv took %v", time.Since(startRecv))

		highestAmount := 0
		highestOrder := ""

		toyOrders := strings.Split(line, ",")
		for _, order := range toyOrders {
			parts := strings.Split(order, "x")
			amount, err := strconv.Atoi(parts[0])
			if err != nil {
				log.Printf("parsing %q failed: %+v", parts[0], err)
				return
			}

			if amount > highestAmount {
				highestAmount = amount
				highestOrder = order
			}
		}

		if err := o.send(highestOrder); err != nil {
			log.Printf("send: %+v", err)
			return
		}
		log.Printf("request took %v", time.Since(start))
	}
}

type decoder struct {
	session *Session
}

func (o *decoder) Read(buf []byte) (n int, err error) {
	n, err = o.session.conn.Read(buf)
	if err == nil {
		o.session.decrypt(buf[:n])
		o.session.cipherRecvPos += byte(n)
	}
	return
}

func (o *Session) recv() (string, error) {
	line, err := o.r.ReadString('\n')
	return strings.TrimRight(line, "\n"), err
}

func (o *Session) send(line string) error {
	enc := append([]byte(line), '\n')
	if err := o.encrypt(enc); err != nil {
		panic(err)
	}
	numBytes := len(enc)
	index := 0
	for index < numBytes {
		n, err := o.conn.Write(enc[index:])
		if err != nil {
			return err
		}
		index += n
	}
	o.cipherSentPos += byte(numBytes)
	return nil
}

func (o *Session) cipherIsValid() bool {
	start := time.Now()
	defer func() {
		log.Printf("cipherIsValid took %v", time.Since(start))
	}()
	for j := 0; j < 64; j++ {
		testData := [256]byte{}
		for i := 0; i < 256; i++ {
			testData[i] = byte(i) + byte(j)
		}
		var buf [256]byte
		copy(buf[:], testData[:])
		if err := o.encrypt(buf[:]); err != nil {
			log.Printf("encrypt failed: %+v", err)
			return false
		}
		if !slices.Equal(testData[:], buf[:]) {
			return true
		}
	}
	return false
}

func (o *Session) encrypt(data []byte) error {
	for i, b := range data {
		for cipherIndex := 0; cipherIndex < len(o.cipherSpec); cipherIndex++ {
			f := o.cipherSpec[cipherIndex]
			pos := o.cipherSentPos + byte(i)
			switch f {
			case 1: // reverse bits
				b = bits.Reverse8(b)
			case 2: // xor(n)
				if cipherIndex+1 >= len(o.cipherSpec) {
					return errors.New("invalid cipher")
				}
				arg := o.cipherSpec[cipherIndex+1]
				cipherIndex++
				b ^= arg
			case 3: // xorpos
				b ^= byte(pos)
			case 4: // add(n)
				if cipherIndex+1 >= len(o.cipherSpec) {
					return errors.New("invalid cipher")
				}
				arg := o.cipherSpec[cipherIndex+1]
				cipherIndex++
				b += arg
			case 5: // addpos
				b = byte((uint(b) + uint(pos)) & 0xFF)
			default:
				return errors.New("invalid cipher")
			}
		}
		data[i] = b
	}
	return nil
}

func (o *Session) decrypt(data []byte) {
	for i, b := range data {
		for cipherIndex := 0; cipherIndex < len(o.reverseCipherSpec); cipherIndex++ {
			f := o.reverseCipherSpec[cipherIndex]
			pos := o.cipherRecvPos + byte(i)
			switch f {
			case 1: // reverse bits
				b = bits.Reverse8(b)
			case 2: // xor(n)
				arg := o.reverseCipherSpec[cipherIndex+1]
				cipherIndex++
				b ^= arg
			case 3: // xorpos
				b ^= byte(pos)
			case 4: // add(n)
				arg := o.reverseCipherSpec[cipherIndex+1]
				cipherIndex++
				b -= arg
			case 5: // addpos
				b = byte((uint(b) - uint(pos)) & 0xFF)
			default:
				panic("unreachable")
			}
		}
		data[i] = b
	}
}

func (o *Session) readCipherSpec() error {
	var buf [1]byte
	nextIsArg := false
	for {
		n, err := o.conn.Read(buf[:])
		if err != nil {
			return err
		}
		if n != 1 {
			panic("what the hell man")
		}
		if buf[0] == 0 && !nextIsArg {
			if len(o.cipherSpec) == 0 {
				return errors.New("empty cipher spec")
			}
			return nil
		}

		if !nextIsArg && (buf[0] == 2 || buf[0] == 4) {
			nextIsArg = true
		} else {
			nextIsArg = false
		}
		o.cipherSpec = append(o.cipherSpec, buf[0])
	}
}

func (o *Session) updateReverseCipherSpec() {
	o.reverseCipherSpec = make([]byte, len(o.cipherSpec))
	ri := len(o.cipherSpec) - 1
	for i := 0; i < len(o.cipherSpec); i++ {
		v := o.cipherSpec[i]
		switch v {
		case 1, 3, 5:
			o.reverseCipherSpec[ri] = v
		case 2, 4:
			o.reverseCipherSpec[ri-1] = v
			o.reverseCipherSpec[ri] = o.cipherSpec[i+1]
			i++
			ri--
		default:
			panic("unreachable")
		}
		ri--
	}
}
