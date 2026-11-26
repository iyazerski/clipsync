package client

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha512"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"unicode"

	"github.com/iyazerski/clipsync/internal/protocol"
)

// Pairing by code: the inviter, a paired device, opens a mailbox on the relay
// and shows a code, the mailbox's nameplate and 6 secret digits. The joiner
// types it in, and both run CPace with the digits. The inviter seals the
// pairing to the resulting key, after each side proves it has the same key.
//
// Slot a holds the session id and the inviter's share, b the joiner's share
// and tag, and c the inviter's tag and the sealed pairing.

const (
	codeDigits   = 6
	pairingCI    = "clipsync pairing"
	pairingSID   = 16
	cpaceShareSz = 32
	tagSize512   = sha512.Size
)

var (
	adInviter = []byte("inviter")
	adJoiner  = []byte("joiner")
)

var (
	// ErrWrongCode means the two devices used different codes.
	ErrWrongCode = errors.New("the code didn't match")
	// ErrCodeGone means the code expired, was used, or never was.
	ErrCodeGone = errors.New("no pairing has this code")
)

// Pairing is what a device needs to join the others.
type Pairing struct {
	Relay  string `json:"relay"`
	Secret string `json:"secret"`
}

// Invitation is an open pairing that waits for a device to join with Code.
type Invitation struct {
	address, nameplate, owner, password string
	sid                                 []byte
	share                               cpaceShare
}

func (inv *Invitation) Code() string { return inv.nameplate + inv.password }

// FormatCode groups a code's digits for reading out: 42 183 059.
func FormatCode(code string) string {
	n := len(code) - codeDigits
	return code[:n] + " " + code[n:n+3] + " " + code[n+3:]
}

// ParseCode reads a code as people type it, with spaces or dashes, and in
// any script's digits.
func ParseCode(s string) (string, bool) {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == ' ' || r == '-' || r == '\u00a0':
		case unicode.IsDigit(r):
			// Digits come in runs of ten, from zero.
			v := 0
			for v < 9 && unicode.IsDigit(r-1) {
				r--
				v++
			}
			b.WriteByte(byte('0' + v))
		default:
			return "", false
		}
	}
	code := b.String()
	return code, len(code) >= codeDigits+2 && len(code) <= codeDigits+4
}

func macKey(sid, isk []byte) []byte {
	sum := sha512.Sum512(slices.Concat([]byte("CPaceMac"), sid, isk))
	return sum[:]
}

func tag(key, share, ad []byte) []byte {
	mac := hmac.New(sha512.New, key)
	mac.Write(lvCat(share, ad))
	return mac.Sum(nil)
}

func pairingAEAD(isk []byte) cipher.AEAD {
	mac := hmac.New(sha512.New, isk)
	mac.Write([]byte("clipsync/pairing"))
	block, _ := aes.NewCipher(mac.Sum(nil)[:32])
	aead, _ := cipher.NewGCM(block)
	return aead
}

func randomScalar() []byte {
	b := make([]byte, 32)
	rand.Read(b)
	return b
}

func pairingRequest(ctx context.Context, method, url, owner, body string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	if owner != "" {
		req.Header.Set("Authorization", "Bearer "+owner)
	}
	return httpClient.Do(req)
}

func slotURL(address, nameplate, slot string) string {
	return address + protocol.PairingPath + "/" + nameplate + "/" + slot
}

// readSlot waits for a slot's message.
func readSlot(ctx context.Context, address, nameplate, slot string) ([]byte, error) {
	for {
		resp, err := pairingRequest(ctx, http.MethodGet, slotURL(address, nameplate, slot), "", "")
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 2048))
		resp.Body.Close()
		switch {
		case err != nil:
			return nil, err
		case resp.StatusCode == http.StatusNotFound:
			return nil, ErrCodeGone
		case resp.StatusCode == http.StatusNoContent:
			continue
		case resp.StatusCode != http.StatusOK:
			return nil, fmt.Errorf("the relay answered %d", resp.StatusCode)
		}
		return wireEncoding.DecodeString(string(body))
	}
}

func writeSlot(ctx context.Context, address, nameplate, slot, owner string, message []byte) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	resp, err := pairingRequest(ctx, http.MethodPut, slotURL(address, nameplate, slot), owner, wireEncoding.EncodeToString(message))
	if err != nil {
		return err
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil
	case http.StatusNotFound, http.StatusConflict:
		return ErrCodeGone
	}
	return fmt.Errorf("the relay answered %d", resp.StatusCode)
}

// Invite opens a pairing on the relay for one device to join.
func (c *Client) Invite(ctx context.Context) (*Invitation, error) {
	n, _ := rand.Int(rand.Reader, big.NewInt(1_000_000))
	inv := &Invitation{address: c.address, password: fmt.Sprintf("%06d", n), sid: make([]byte, pairingSID)}
	rand.Read(inv.sid)
	var err error
	inv.share, err = newCPaceShare(cpaceGenerator([]byte(inv.password), []byte(pairingCI), inv.sid), randomScalar())
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	resp, err := c.request(ctx, http.MethodPost, protocol.PairingPath, wireEncoding.EncodeToString(slices.Concat(inv.sid, inv.share.public)), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var opened struct{ Nameplate, Owner string }
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1024)).Decode(&opened); err != nil || opened.Nameplate == "" {
		return nil, errors.New("the relay answered with no pairing")
	}
	inv.nameplate, inv.owner = opened.Nameplate, opened.Owner
	return inv, nil
}

// Finish waits for a device to join and hands it pairing. A joiner with
// another code ends the invitation with ErrWrongCode.
func (inv *Invitation) Finish(ctx context.Context, pairing Pairing) error {
	answer, err := readSlot(ctx, inv.address, inv.nameplate, "b")
	if err != nil {
		return err
	}
	if len(answer) != cpaceShareSz+tagSize512 {
		inv.Close()
		return ErrWrongCode
	}
	yb, tb := answer[:cpaceShareSz], answer[cpaceShareSz:]
	isk, err := inv.share.isk(inv.sid, yb, inv.share.public, adInviter, yb, adJoiner)
	if err != nil {
		inv.Close()
		return ErrWrongCode
	}
	key := macKey(inv.sid, isk)
	if !hmac.Equal(tb, tag(key, yb, adJoiner)) {
		inv.Close()
		return ErrWrongCode
	}
	plain, err := json.Marshal(pairing)
	if err != nil {
		return err
	}
	aead := pairingAEAD(isk)
	nonce := make([]byte, aead.NonceSize())
	rand.Read(nonce)
	reply := slices.Concat(tag(key, inv.share.public, adInviter), nonce)
	return writeSlot(ctx, inv.address, inv.nameplate, "c", inv.owner, aead.Seal(reply, nonce, plain, nil))
}

// Close ends the invitation, so its code no longer works.
func (inv *Invitation) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	if resp, err := pairingRequest(ctx, http.MethodDelete, inv.address+protocol.PairingPath+"/"+inv.nameplate, inv.owner, ""); err == nil {
		resp.Body.Close()
	}
}

// Join pairs with the device that shows code, through relay.
func Join(ctx context.Context, relay, code string) (Pairing, error) {
	code, ok := ParseCode(code)
	if !ok {
		return Pairing{}, ErrCodeGone
	}
	nameplate, password := code[:len(code)-codeDigits], code[len(code)-codeDigits:]
	opening, err := readSlot(ctx, relay, nameplate, "a")
	if err != nil {
		return Pairing{}, err
	}
	if len(opening) != pairingSID+cpaceShareSz {
		return Pairing{}, ErrWrongCode
	}
	sid, ya := opening[:pairingSID], opening[pairingSID:]
	share, err := newCPaceShare(cpaceGenerator([]byte(password), []byte(pairingCI), sid), randomScalar())
	if err != nil {
		return Pairing{}, err
	}
	isk, err := share.isk(sid, ya, ya, adInviter, share.public, adJoiner)
	if err != nil {
		return Pairing{}, ErrWrongCode
	}
	key := macKey(sid, isk)
	if err := writeSlot(ctx, relay, nameplate, "b", "", slices.Concat(share.public, tag(key, share.public, adJoiner))); err != nil {
		return Pairing{}, err
	}
	reply, err := readSlot(ctx, relay, nameplate, "c")
	if errors.Is(err, ErrCodeGone) {
		// The inviter closes the pairing when the code doesn't match.
		return Pairing{}, ErrWrongCode
	}
	if err != nil {
		return Pairing{}, err
	}
	aead := pairingAEAD(isk)
	if len(reply) < tagSize512+aead.NonceSize() || !hmac.Equal(reply[:tagSize512], tag(key, ya, adInviter)) {
		return Pairing{}, ErrWrongCode
	}
	nonce := reply[tagSize512 : tagSize512+aead.NonceSize()]
	plain, err := aead.Open(nil, nonce, reply[tagSize512+aead.NonceSize():], nil)
	if err != nil {
		return Pairing{}, ErrWrongCode
	}
	var p Pairing
	if err := json.Unmarshal(plain, &p); err != nil {
		return Pairing{}, err
	}
	if p.Relay, err = NormalizeRelay(p.Relay); err != nil {
		return Pairing{}, err
	}
	if _, err := Token(p.Secret); err != nil {
		return Pairing{}, err
	}
	return p, nil
}
