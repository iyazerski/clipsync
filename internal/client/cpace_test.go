package client

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The test vectors of draft-irtf-cfrg-cpace-21, appendix B.1.
func TestCPaceVectors(t *testing.T) {
	prs := []byte("Password")
	ci := unhex(t, "0b415f696e69746961746f720b425f726573706f6e646572")
	sid := unhex(t, "7e4b4791d6a8ef019b936c79fb7f2c57")
	g := cpaceGenerator(prs, ci, sid)
	if want := unhex(t, "d04bf6d41f6a289632a2e929fa29bebd51092512a7829fdde7d314b62f05a73f"); !bytes.Equal(g, want) {
		t.Fatalf("generator = %x, want %x", g, want)
	}
	a, err := newCPaceShare(g, unhex(t, "21b4f4bd9e64ed355c3eb676a28ebedaf6d8f17bdc365995b319097153044080"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := newCPaceShare(g, unhex(t, "848b0779ff415f0af4ea14df9dd1d3c29ac41d836c7808896c4eba19c51ac40a"))
	if err != nil {
		t.Fatal(err)
	}
	if want := unhex(t, "1d13c89278cdadd826f6d8d7f887701430f8380ddc17611cdd6dc989ce0c9f32"); !bytes.Equal(a.public, want) {
		t.Fatalf("Ya = %x, want %x", a.public, want)
	}
	if want := unhex(t, "248cccf6d5cdc3646f0ad593f9e6cef4e69d4945f8372e623512ecea32185623"); !bytes.Equal(b.public, want) {
		t.Fatalf("Yb = %x, want %x", b.public, want)
	}
	want := unhex(t, "6e19b875f7a561d6b3ca3dbb9ef42ac55de3e717881018204b8922b4d5e53bb2aa82c300bea7b65d2b671da71922ddf6472301b79bc270adfa8bf413285f2263")
	for name, s := range map[string]cpaceShare{"A": a, "B": b} {
		peer := b.public
		if name == "B" {
			peer = a.public
		}
		isk, err := s.isk(sid, peer, a.public, []byte("ADa"), b.public, []byte("ADb"))
		if err != nil || !bytes.Equal(isk, want) {
			t.Errorf("ISK of %s = %x (%v), want %x", name, isk, err, want)
		}
	}
	if _, err := a.isk(sid, make([]byte, 32), a.public, nil, nil, nil); err == nil {
		t.Error("accepted a low-order point")
	}
}
