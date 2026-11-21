package sshagenthost

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"

	"moonpassword/core/sshagent"
)

const (
	nonceBytes   = 32
	messageLimit = 64 << 20
	callTimeout  = 5 * time.Second
)

var (
	errHandshake = errors.New("could not authenticate the SSH Agent process")
	errOwnerBusy = errors.New("the SSH Agent is already connected")
)

type KeyInfo struct {
	Public                  []byte
	EntryID, Title, Comment string
	Hosts                   []string
}

type KeyInput struct {
	PEM            []byte
	EntryID, Title string
	Hosts          []string
}

type Status struct {
	State, Pipe, Error, KeysState string
	Expires                       time.Time
	Keys                          []KeyInfo
	Connections                   int
	Generation                    uint64
}

type Event struct {
	Type, Reason string
	Use          sshagent.Use
	HostKey      []byte
	Client       []sshagent.Proc
}

type request struct {
	Op, Pipe   string
	Forward    bool
	Keys       []KeyInput
	Generation uint64
	OwnerPID   uint32
	Until      time.Time
}

type response struct {
	Busy    bool
	Status  Status
	Events  []Event
	Error   string
	Changed bool
	Cleared int
}

type channel struct {
	net.Conn
	tx, rx         cipher.AEAD
	sent, received uint64
}

func mac(key []byte, label string, nonces []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("MoonPassword Agent v1:" + label))
	m.Write(nonces)
	return m.Sum(nil)
}

// Secret derives a separate IPC credential from the existing profile key.
func Secret(profileKey []byte) []byte {
	return mac(profileKey, "control", nil)
}

// authenticate binds both endpoints and each subsequent frame to fresh session keys.
// Private key material is sent only over this authenticated, encrypted channel.
func authenticate(conn net.Conn, secret []byte, server bool) (*channel, error) {
	_ = conn.SetDeadline(time.Now().Add(callTimeout))
	var seeds [2 * nonceBytes]byte
	left, right := seeds[:nonceBytes], seeds[nonceBytes:]
	var proof [sha256.Size]byte
	if server {
		if _, err := rand.Read(left); err != nil {
			return nil, err
		}
		if err := writeAll(conn, left); err != nil {
			return nil, err
		}
		if _, err := io.ReadFull(conn, right); err != nil {
			return nil, err
		}
		if err := writeAll(conn, mac(secret, "server", seeds[:])); err != nil {
			return nil, err
		}
		if _, err := io.ReadFull(conn, proof[:]); err != nil {
			return nil, err
		}
		if !hmac.Equal(proof[:], mac(secret, "client", seeds[:])) {
			return nil, errHandshake
		}
	} else {
		if _, err := io.ReadFull(conn, left); err != nil {
			return nil, err
		}
		if _, err := rand.Read(right); err != nil {
			return nil, err
		}
		if err := writeAll(conn, right); err != nil {
			return nil, err
		}
		if _, err := io.ReadFull(conn, proof[:]); err != nil {
			return nil, err
		}
		if !hmac.Equal(proof[:], mac(secret, "server", seeds[:])) {
			return nil, errHandshake
		}
		if err := writeAll(conn, mac(secret, "client", seeds[:])); err != nil {
			return nil, err
		}
	}
	toHost, fromHost := makeAEAD(mac(secret, "to host", seeds[:])), makeAEAD(mac(secret, "from host", seeds[:]))
	if server {
		toHost, fromHost = fromHost, toHost
	}
	_ = conn.SetDeadline(time.Time{})
	return &channel{Conn: conn, tx: toHost, rx: fromHost}, nil
}

func makeAEAD(key []byte) cipher.AEAD {
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	} // mac always supplies an AES-256 key
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return aead
}

func writeAll(w io.Writer, b []byte) error {
	n, err := w.Write(b)
	if err == nil && n != len(b) {
		return io.ErrShortWrite
	}
	return err
}

func (c *channel) send(value any) error {
	plain, err := json.Marshal(value)
	if err != nil {
		return err
	}
	defer clear(plain)
	if len(plain) > messageLimit-c.tx.Overhead() {
		return errors.New("SSH Agent message too large")
	}
	var nonce [12]byte
	binary.BigEndian.PutUint64(nonce[4:], c.sent)
	c.sent++
	frame := c.tx.Seal(nil, nonce[:], plain, nil)
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(frame)))
	if err := writeAll(c.Conn, size[:]); err != nil {
		return err
	}
	return writeAll(c.Conn, frame)
}

func (c *channel) read(value any) error {
	var size [4]byte
	if _, err := io.ReadFull(c.Conn, size[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(size[:])
	if n < uint32(c.rx.Overhead()) || n > messageLimit {
		return errors.New("invalid SSH Agent message size")
	}
	frame := make([]byte, int(n))
	if _, err := io.ReadFull(c.Conn, frame); err != nil {
		return err
	}
	var nonce [12]byte
	binary.BigEndian.PutUint64(nonce[4:], c.received)
	c.received++
	plain, err := c.rx.Open(frame[:0], nonce[:], frame, nil)
	if err != nil {
		return err
	}
	defer clear(plain)
	return json.Unmarshal(plain, value)
}
