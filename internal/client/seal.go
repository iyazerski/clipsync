package client

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/iyazerski/clipsync/internal/protocol"
)

const (
	clipVersion = 2
	nonceSize   = 12
	tagSize     = 16
	stampSize   = 8
	headerSize  = 1 + nonceSize
	maxAge      = 5 * time.Minute
)

var wireEncoding = base64.RawURLEncoding.Strict()

type keys struct {
	token string
	aead  cipher.AEAD
}

func RandomID(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return wireEncoding.EncodeToString(b)
}

func derive(secret string) (keys, error) {
	raw, err := wireEncoding.DecodeString(strings.TrimSpace(secret))
	if err != nil || len(raw) != 32 {
		return keys{}, errors.New("the secret should be 43 characters of base64url, as in a pairing link")
	}
	sign := func(label string) []byte {
		mac := hmac.New(sha256.New, raw)
		mac.Write([]byte(label))
		return mac.Sum(nil)
	}
	block, err := aes.NewCipher(sign("clipsync/key"))
	if err != nil {
		return keys{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return keys{}, err
	}
	return keys{token: wireEncoding.EncodeToString(sign("clipsync/token")), aead: aead}, nil
}

func (k keys) seal(text string, now time.Time) string {
	// Existing devices expect version || nonce || encrypted(timestamp || text) || tag.
	buf := make([]byte, headerSize+stampSize+len(text), headerSize+stampSize+len(text)+tagSize)
	buf[0] = clipVersion
	nonce := buf[1:headerSize]
	rand.Read(nonce)
	binary.BigEndian.PutUint64(buf[headerSize:], uint64(now.UnixMilli()))
	copy(buf[headerSize+stampSize:], text)
	buf = k.aead.Seal(buf[:headerSize], nonce, buf[headerSize:], nil)
	return wireEncoding.EncodeToString(buf)
}

func (k keys) open(sealed string, now time.Time, opened map[[nonceSize]byte]uint64) (string, error) {
	if len(sealed) > protocol.MaxSealed {
		return "", errors.New("sealed clip is too large")
	}
	buf, err := wireEncoding.DecodeString(sealed)
	if err != nil || len(buf) < headerSize+stampSize+tagSize || buf[0] != clipVersion {
		return "", errors.New("invalid clip format")
	}
	nonce := [nonceSize]byte(buf[1:headerSize])
	plain, err := k.aead.Open(buf[headerSize:headerSize], nonce[:], buf[headerSize:], nil)
	if err != nil {
		return "", errors.New("can't open the clip: the devices may use different secrets")
	}
	stamp := binary.BigEndian.Uint64(plain)
	fresh := func(at uint64) bool {
		current := uint64(now.UnixMilli())
		return max(current, at)-min(current, at) <= uint64(maxAge.Milliseconds())
	}
	if !fresh(stamp) {
		return "", errors.New("clip is outside the allowed time window")
	}
	for seen, at := range opened {
		if !fresh(at) {
			delete(opened, seen)
		}
	}
	if _, seen := opened[nonce]; seen {
		return "", errors.New("clip has already been opened")
	}
	text := plain[stampSize:]
	if len(text) > protocol.MaxText || !utf8.Valid(text) {
		return "", errors.New("invalid clip text")
	}
	opened[nonce] = stamp
	return string(text), nil
}
