package client

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/iyazerski/clipsync/internal/protocol"
)

func TestProfilesOpenOnlyForTheirDevice(t *testing.T) {
	k, err := derive(RandomID(32))
	if err != nil {
		t.Fatal(err)
	}
	// Longer than a name may be, in the widest runes.
	name := strings.Repeat("🔐", maxName+8)
	sealed := k.sealProfile("mac-1", Profile{Name: name, Kind: "mac"})
	if len(sealed) > protocol.MaxProfile {
		t.Errorf("sealed profile is %d characters, over %d", len(sealed), protocol.MaxProfile)
	}
	p, err := k.openProfile("mac-1", sealed)
	if err != nil || utf8.RuneCountInString(p.Name) != maxName || !strings.HasPrefix(name, p.Name) || p.Kind != "mac" {
		t.Fatalf("opened %+v, %v", p, err)
	}
	if _, err := k.openProfile("phone-1", sealed); err == nil {
		t.Error("another device's profile opened")
	}
	other, _ := derive(RandomID(32))
	if _, err := other.openProfile("mac-1", sealed); err == nil {
		t.Error("another pairing's profile opened")
	}
	if _, err := k.open(sealed, time.Now(), map[[nonceSize]byte]uint64{}); err == nil {
		t.Error("a profile opened as a clip")
	}
}

func TestProfilesFromThePhoneOpen(t *testing.T) {
	k, err := derive("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8")
	if err != nil {
		t.Fatal(err)
	}
	const sealed = "AQHmbbfZdJ5QU534SoSbbGtf3PYWzXw8uVMnLgNETm-bITrs5nyf5YDmcoKW014dvcHiAz5-e6_4d2xz6GI3yxEE37T248pMLo-JF_2pqg"
	if p, err := k.openProfile("phone-fixture", sealed); err != nil || p != (Profile{Name: "Ivan’s Pixel 🔐", Kind: "android"}) {
		t.Errorf("opened %+v, %v", p, err)
	}
}
