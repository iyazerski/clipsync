package client

import (
	"crypto/ecdh"
	"crypto/sha512"
	"errors"
	"math/big"
	"slices"
)

// CPace-X25519-SHA512 from draft-irtf-cfrg-cpace-21, in initiator-responder
// mode. It turns a short code into a shared key without letting the relay,
// or anyone else, test more than one guess of the code per pairing.

const cpaceDSI = "CPace255"

var (
	field       = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 255), big.NewInt(19))
	montgomeryA = big.NewInt(486662)
)

func prependLen(data []byte) []byte {
	var out []byte
	for n := len(data); ; n >>= 7 {
		if n < 128 {
			out = append(out, byte(n))
		} else {
			out = append(out, byte(n&0x7f|0x80))
		}
		if n>>7 == 0 {
			break
		}
	}
	return append(out, data...)
}

func lvCat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, prependLen(p)...)
	}
	return out
}

// cpaceGenerator maps the code (prs), channel identifier and session id to a
// point that nobody knows a discrete logarithm of.
func cpaceGenerator(prs, ci, sid []byte) []byte {
	zpad := max(0, sha512.BlockSize-1-len(prependLen(prs))-len(prependLen([]byte(cpaceDSI))))
	sum := sha512.Sum512(lvCat([]byte(cpaceDSI), prs, make([]byte, zpad), ci, sid))
	return elligator2(sum[:32])
}

// elligator2 maps a little-endian field element to the u-coordinate of a point
// on Curve25519. Its arithmetic isn't constant-time, which is fine for the
// short, one-off pairing exchange it serves.
func elligator2(r []byte) []byte {
	le := slices.Clone(r)
	le[31] &= 0x7f
	slices.Reverse(le)
	u := new(big.Int).SetBytes(le)

	mod := func(x *big.Int) *big.Int { return x.Mod(x, field) }
	// v = -A / (1 + 2u²), where 2 is the non-square that RFC 9380 picks for Curve25519.
	den := mod(new(big.Int).Add(big.NewInt(1), new(big.Int).Lsh(new(big.Int).Mul(u, u), 1)))
	v := mod(new(big.Int).Neg(new(big.Int).Mul(montgomeryA, new(big.Int).ModInverse(den, field))))
	// ε = (v³ + Av² + v)^((p-1)/2) is 1 when v is a point's u-coordinate, else -1 (or 0).
	v2 := new(big.Int).Mul(v, v)
	rhs := mod(new(big.Int).Add(new(big.Int).Add(new(big.Int).Mul(v2, v), new(big.Int).Mul(montgomeryA, v2)), v))
	eps := new(big.Int).Exp(rhs, new(big.Int).Rsh(new(big.Int).Sub(field, big.NewInt(1)), 1), field)
	// x = εv - (1-ε)A/2
	halfA := mod(new(big.Int).Mul(montgomeryA, new(big.Int).ModInverse(big.NewInt(2), field)))
	x := mod(new(big.Int).Sub(new(big.Int).Mul(eps, v), new(big.Int).Mul(mod(new(big.Int).Sub(big.NewInt(1), eps)), halfA)))

	out := x.FillBytes(make([]byte, 32))
	slices.Reverse(out)
	return out
}

var errLowOrder = errors.New("the other device sent an invalid share")

// cpaceShare is one side's secret scalar and the public share it sends.
type cpaceShare struct {
	scalar *ecdh.PrivateKey
	public []byte
}

// newCPaceShare multiplies the generator by scalar, 32 random bytes.
func newCPaceShare(generator, scalar []byte) (cpaceShare, error) {
	key, err := ecdh.X25519().NewPrivateKey(scalar)
	if err != nil {
		return cpaceShare{}, err
	}
	public, err := x25519(key, generator)
	return cpaceShare{scalar: key, public: public}, err
}

func x25519(scalar *ecdh.PrivateKey, point []byte) ([]byte, error) {
	pub, err := ecdh.X25519().NewPublicKey(point)
	if err != nil {
		return nil, errLowOrder
	}
	// ECDH refuses low-order points, whose product is the identity.
	k, err := scalar.ECDH(pub)
	if err != nil {
		return nil, errLowOrder
	}
	return k, nil
}

// isk derives the intermediate session key from this side's share and the
// other side's, given in the initiator's and responder's order.
func (s cpaceShare) isk(sid, peer, ya, ada, yb, adb []byte) ([]byte, error) {
	k, err := x25519(s.scalar, peer)
	if err != nil {
		return nil, err
	}
	sum := sha512.Sum512(slices.Concat(lvCat([]byte(cpaceDSI+"_ISK"), sid, k), lvCat(ya, ada), lvCat(yb, adb)))
	return sum[:], nil
}
