package identity

import (
	"crypto/ed25519"
	"encoding/hex"
	"github.com/mr-tron/base58"
	"os"
	"path/filepath"
	"testing"
)

func TestDIDMulticodec(t *testing.T) {
	// Ed25519 public key from RFC 8032 section 7.1, encoded with multicodec 0xed.
	raw, _ := hex.DecodeString("d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a")
	did := DID(ed25519.PublicKey(raw))
	decoded, e := base58.Decode(did[len("did:key:z"):])
	if e != nil || hex.EncodeToString(decoded) != "ed01"+hex.EncodeToString(raw) {
		t.Fatalf("bad multicodec: %s %v", did, e)
	}
	// Known Ed25519 Multikey vector from W3C vc-di-eddsa, example 1.
	key, e := base58.Decode("6Mkf5rGMoatrSj1f4CyvuHBeXJELe9RPdzo2PKGNCKVtZxP")
	if e != nil {
		t.Fatal(e)
	}
	if got := DID(ed25519.PublicKey(key[2:])); got != "did:key:z6Mkf5rGMoatrSj1f4CyvuHBeXJELe9RPdzo2PKGNCKVtZxP" {
		t.Fatal(got)
	}
}
func TestBootstrapPrivateAndNonDestructive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	o, e := Bootstrap(dir)
	if e != nil {
		t.Fatal(e)
	}
	loaded, e := Load(dir)
	if e != nil || o.DID != loaded.DID {
		t.Fatal(e)
	}
	if _, e = Bootstrap(dir); e == nil {
		t.Fatal("overwrote identity")
	}
	st, _ := os.Stat(filepath.Join(dir, "owner.json"))
	if st.Mode().Perm() != 0600 {
		t.Fatal(st.Mode())
	}
	if e = os.Chmod(dir, 0755); e != nil {
		t.Fatal(e)
	}
	if e = PrivateDir(dir); e == nil {
		t.Fatal("accepted public directory")
	}
}
