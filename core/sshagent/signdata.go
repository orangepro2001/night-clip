package sshagent

import (
	"bytes"
	"encoding/binary"
	"errors"

	"golang.org/x/crypto/ssh"
)

// Request kinds.
const (
	KindUserauth = "userauth" // logging in to a server
	KindSSHSig   = "sshsig"   // ssh-keygen -Y sign, e.g. git commit signing
	KindOther    = "other"
)

// signRequest is what could be learned from the data a client asked to sign.
type signRequest struct {
	Kind      string
	User      string        // userauth: the account on the server
	HostKey   ssh.PublicKey // userauth with publickey-hostbound-v00: the server's host key
	SessionID []byte
	Namespace string // sshsig: e.g. "git", "file"
}

const (
	msgUserauthRequest = 50
	sshsigMagic        = "SSHSIG"
)

// classify reads the data to be signed. It never fails; unrecognised data
// is KindOther.
func classify(data []byte) signRequest {
	if bytes.HasPrefix(data, []byte(sshsigMagic)) {
		r := reader{b: data[len(sshsigMagic):]}
		ns := r.str()
		if !r.err {
			return signRequest{Kind: KindSSHSig, Namespace: ns}
		}
	}
	r := reader{b: data}
	sid := r.bytes()
	if !r.err && len(r.b) > 0 && r.b[0] == msgUserauthRequest {
		r.b = r.b[1:]
		user := r.str()
		r.str() // service, "ssh-connection"
		method := r.str()
		hasSig := r.byte1()
		r.str()   // algorithm
		r.bytes() // public key
		if !r.err && hasSig == 1 && (method == "publickey" || method == "publickey-hostbound-v00@openssh.com") {
			req := signRequest{Kind: KindUserauth, User: user, SessionID: append([]byte(nil), sid...)}
			if method == "publickey-hostbound-v00@openssh.com" {
				if hk, err := ssh.ParsePublicKey(r.bytes()); err == nil && !r.err {
					req.HostKey = hk
				}
			}
			return req
		}
	}
	return signRequest{Kind: KindOther}
}

// sessionBind is one session-bind@openssh.com message: the client tells the
// agent which server it is talking to, proven by the server's signature
// over the session identifier.
type sessionBind struct {
	HostKey    ssh.PublicKey
	SessionID  []byte
	Forwarding bool
}

var errBadBind = errors.New("sshagent: invalid session-bind")

func parseSessionBind(contents []byte) (sessionBind, error) {
	r := reader{b: contents}
	hkBlob := r.bytes()
	sid := r.bytes()
	sigBlob := r.bytes()
	fwd := r.byte1()
	if r.err || len(r.b) != 0 || len(sid) == 0 || len(sid) > 64 {
		return sessionBind{}, errBadBind
	}
	hk, err := ssh.ParsePublicKey(hkBlob)
	if err != nil {
		return sessionBind{}, errBadBind
	}
	var sig ssh.Signature
	if err := ssh.Unmarshal(sigBlob, &sig); err != nil {
		return sessionBind{}, errBadBind
	}
	if err := hk.Verify(sid, &sig); err != nil {
		return sessionBind{}, errBadBind
	}
	return sessionBind{HostKey: hk, SessionID: append([]byte(nil), sid...), Forwarding: fwd != 0}, nil
}

// reader parses SSH wire format; after the first error everything is zero.
type reader struct {
	b   []byte
	err bool
}

func (r *reader) bytes() []byte {
	if r.err || len(r.b) < 4 {
		r.err = true
		return nil
	}
	n := binary.BigEndian.Uint32(r.b)
	if uint64(n) > uint64(len(r.b)-4) {
		r.err = true
		return nil
	}
	v := r.b[4 : 4+n]
	r.b = r.b[4+n:]
	return v
}

func (r *reader) str() string { return string(r.bytes()) }

func (r *reader) byte1() byte {
	if r.err || len(r.b) < 1 {
		r.err = true
		return 0
	}
	v := r.b[0]
	r.b = r.b[1:]
	return v
}
