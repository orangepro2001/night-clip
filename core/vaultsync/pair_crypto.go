package vaultsync

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
)

var pairEncoding = base64.RawURLEncoding.Strict()

func pairBytes(value string, size int) []byte {
	if len(value) != base64.RawURLEncoding.EncodedLen(size) {
		return nil
	}
	data, err := pairEncoding.DecodeString(value)
	if err != nil || len(data) != size {
		return nil
	}
	return data
}

func PairID() string {
	var b [24]byte
	rand.Read(b[:])
	return pairEncoding.EncodeToString(b[:])
}

func NewPairKey() (*ecdh.PrivateKey, string, error) {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	return key, pairEncoding.EncodeToString(key.PublicKey().Bytes()), nil
}

func pairMessage(parts ...string) []byte {
	data, _ := json.Marshal(parts)
	return data
}

// PairCommit binds the requester to its public key before the approver's
// public key is visible. This prevents either side of an active relay from
// searching for a matching short code after seeing both honest public keys.
func PairCommit(id, nonce, public string) string {
	sum := sha256.Sum256(pairMessage("MoonPassword pair commitment v1", id, nonce, public))
	return pairEncoding.EncodeToString(sum[:])
}

type PairChannel struct {
	aead       cipher.AEAD
	transcript []byte
	Code       string
}

func NewPairChannel(key *ecdh.PrivateKey, subject string, pair Pair, peer string) (*PairChannel, error) {
	public, err := ecdh.X25519().NewPublicKey(pairBytes(peer, 32))
	if err != nil {
		return nil, ErrInvalid
	}
	shared, err := key.ECDH(public)
	if err != nil {
		return nil, ErrInvalid
	}
	defer clear(shared)
	transcript := pairMessage("MoonPassword pair transcript v1", subject, pair.ID, pair.Commitment, pair.Requester, pair.Approver)
	salt := sha256.Sum256(transcript)
	sas, err := hkdf.Key(sha256.New, shared, salt[:], "comparison code", 8)
	if err != nil {
		return nil, err
	}
	code := fmt.Sprintf("%06d", binary.BigEndian.Uint64(sas)%1_000_000)
	clear(sas)
	secret, err := hkdf.Key(sha256.New, shared, salt[:], "vault key encryption", 32)
	if err != nil {
		return nil, err
	}
	defer clear(secret)
	block, err := aes.NewCipher(secret)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return nil, err
	}
	return &PairChannel{aead: aead, transcript: transcript, Code: code}, nil
}

const pairCipherSize = 43 + 12 + 16

func (p *PairChannel) Seal(key string) string {
	return pairEncoding.EncodeToString(p.aead.Seal(nil, nil, []byte(key), p.transcript))
}

func (p *PairChannel) Open(payload string) (string, error) {
	data, err := p.aead.Open(nil, nil, pairBytes(payload, pairCipherSize), p.transcript)
	if err != nil {
		return "", ErrInvalid
	}
	defer clear(data)
	key := string(data)
	if raw := pairBytes(key, 32); raw == nil {
		return "", ErrInvalid
	} else {
		clear(raw)
	}
	return key, nil
}

func vaultWriteKey(recovery string) ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("MoonPassword vault sync write key v1:" + recovery))
	defer clear(seed[:])
	return ed25519.NewKeyFromSeed(seed[:])
}

func pairApproval(subject string, p Pair) []byte {
	return pairMessage("MoonPassword pair approval v1", subject, p.ID, p.Commitment, p.Requester, p.Approver, p.Payload)
}

func SignPair(subject string, p Pair, recovery string) string {
	key := vaultWriteKey(recovery)
	defer clear(key)
	return pairEncoding.EncodeToString(ed25519.Sign(key, pairApproval(subject, p)))
}
