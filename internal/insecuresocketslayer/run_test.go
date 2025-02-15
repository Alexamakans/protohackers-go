package insecuresocketslayer

import (
	"bytes"
	"log"
	"math/rand"
	"slices"
	"strings"
	"testing"
)

func TestEncryptDecrypt(t *testing.T) {
	session := Session{
		cipherSpec: []byte("\x01\x02\x11\x03\x04\x33\x05"),
	}
	session.updateReverseCipherSpec()

	ref := [256]byte{}
	for i := 0; i < 256; i++ {
		ref[i] = byte(i)
	}
	var buf [256]byte
	copy(buf[:], ref[:])
	if err := session.encrypt(buf[:]); err != nil {
		panic(err)
	}
	session.decrypt(buf[:])
	if !slices.Equal(ref[:], buf[:]) {
		log.Printf("expected v\n%+v", ref)
		log.Printf("%+v\ngot ^", buf)
		t.FailNow()
	}
}

func TestEncryptDecryptCase1(t *testing.T) {
	session := Session{
		cipherSpec: []byte{2, 151, 2, 207, 4, 160, 5, 1, 5, 4, 67, 2, 24, 4, 80, 1, 4, 249, 5, 4, 255, 3, 4, 6, 5, 4, 223, 1, 4, 240, 2, 90, 2, 212, 4, 210, 1, 1, 5, 3, 1, 2, 50, 3, 2, 24},
	}
	session.updateReverseCipherSpec()

	ref := [1024]byte{}
	for i := 0; i < 1024; i++ {
		ref[i] = byte(i & 0xFF)
	}
	var buf [1024]byte
	copy(buf[:], ref[:])
	if err := session.encrypt(buf[:]); err != nil {
		panic(err)
	}
	session.decrypt(buf[:])
	if !slices.Equal(ref[:], buf[:]) {
		log.Printf("expected v\n%+v", ref)
		log.Printf("%+v\ngot ^", buf)
		t.FailNow()
	}
}

func randomCipherSpec() []byte {
	spec := []byte{}
	ops := []byte{0x01, 0x02, 0x03, 0x04, 0x05} // Possible ops
	for i := 0; i < rand.Intn(10)+1; i++ {      // 1-10 ops
		op := ops[rand.Intn(len(ops))]
		spec = append(spec, op)
		if op == 0x02 || op == 0x04 { // Add operand for 02, 04
			spec = append(spec, byte(rand.Intn(256)))
		}
	}
	return spec
}

func FuzzCipher(f *testing.F) {
	f.Add([]byte(strings.Repeat("Hello, world!", 128)), randomCipherSpec()) // Seed case

	f.Fuzz(func(t *testing.T, input []byte, cipherSpec []byte) {
		// Skip empty input or cipherSpec for meaningful results
		if len(input) == 0 || len(cipherSpec) == 0 {
			t.Skip()
		}

		// Create a session with the generated cipherSpec
		session := &Session{
			cipherSpec: cipherSpec,
		}

		// Validate the cipher
		if !session.cipherIsValid() {
			t.Logf("Skipping invalid cipher spec: %+v", cipherSpec)
			return
		}

		// Reverse the cipher spec
		session.updateReverseCipherSpec()

		// Encrypt data
		encrypted := append([]byte{}, input...)
		_ = session.encrypt(encrypted)

		// Decrypt data
		decrypted := append([]byte{}, encrypted...)
		session.decrypt(decrypted)

		// Compare original and decrypted data
		if !bytes.Equal(input, decrypted) {
			t.Errorf("Decryption failed: input=%q, encrypted=%q, decrypted=%q, cipherSpec=%+v, reverseSpec=%+v",
				input, encrypted, decrypted, session.cipherSpec, session.reverseCipherSpec)
		}
	})
}
