package client

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
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
	// profile seals profiles, apart from clips so that neither opens as the other.
	profile cipher.AEAD
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
	// Neither fails with a 32-byte key.
	gcm := func(label string) cipher.AEAD {
		block, _ := aes.NewCipher(sign(label))
		aead, _ := cipher.NewGCM(block)
		return aead
	}
	return keys{token: wireEncoding.EncodeToString(sign("clipsync/token")), aead: gcm("clipsync/key"), profile: gcm("clipsync/profile")}, nil
}

// Profile is how a device shows in the others' lists.
type Profile struct {
	Name string `json:"name"`
	// Kind is "mac", "windows" or "android".
	Kind string `json:"kind"`
}

const (
	profileVersion = 1
	maxName        = 64
	maxKind        = 16
)

// sealProfile seals p to device, so that the relay can't pass it off as
// another device's: version || nonce || encrypted(JSON) || tag.
func (k keys) sealProfile(device string, p Profile) string {
	name := []rune(strings.Map(func(r rune) rune {
		if !unicode.IsPrint(r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(p.Name, "")))
	p.Name = strings.TrimSpace(string(name[:min(len(name), maxName)]))
	var plain bytes.Buffer
	encoder := json.NewEncoder(&plain)
	encoder.SetEscapeHTML(false)
	encoder.Encode(p)
	buf := make([]byte, headerSize, headerSize+plain.Len()+tagSize)
	buf[0] = profileVersion
	rand.Read(buf[1:headerSize])
	buf = k.profile.Seal(buf, buf[1:headerSize], plain.Bytes(), []byte(device))
	return wireEncoding.EncodeToString(buf)
}

func (k keys) openProfile(device, sealed string) (Profile, error) {
	buf, err := wireEncoding.DecodeString(sealed)
	if err != nil || len(buf) < headerSize+tagSize || buf[0] != profileVersion {
		return Profile{}, errors.New("invalid profile format")
	}
	plain, err := k.profile.Open(nil, buf[1:headerSize], buf[headerSize:], []byte(device))
	if err != nil {
		return Profile{}, errors.New("can't open the profile: it's from another pairing or device")
	}
	var p Profile
	if err := json.Unmarshal(plain, &p); err != nil || utf8.RuneCountInString(p.Name) > maxName || len(p.Kind) > maxKind {
		return Profile{}, errors.New("invalid profile")
	}
	return p, nil
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
