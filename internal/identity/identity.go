// Package identity implements local Ed25519 did:key bootstrap. Keys are not worker credentials.
package identity

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mr-tron/base58"
)

type Owner struct {
	DID        string `json:"did"`
	PrivateKey []byte `json:"privateKey"`
	AdminToken string `json:"adminToken"`
}

func DID(public ed25519.PublicKey) string {
	return "did:key:z" + base58.Encode(append([]byte{0xed, 0x01}, public...))
}
func Token() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func Hash(token string) string { h := sha256.Sum256([]byte(token)); return hex.EncodeToString(h[:]) }
func PrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("state directory must be a private directory (0700): %s", dir)
	}
	return nil
}
func Bootstrap(dir string) (Owner, error) {
	var o Owner
	if err := PrivateDir(dir); err != nil {
		return o, err
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return o, err
	}
	o = Owner{DID(pub), key, Token()}
	b, err := json.Marshal(o)
	if err != nil {
		return o, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "owner.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return o, err
	}
	defer f.Close()
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	return o, err
}
func Load(dir string) (Owner, error) {
	var o Owner
	if err := PrivateDir(dir); err != nil {
		return o, err
	}
	if err := PrivateFile(filepath.Join(dir, "owner.json")); err != nil {
		return o, err
	}
	b, err := os.ReadFile(filepath.Join(dir, "owner.json"))
	if err != nil {
		return o, err
	}
	err = json.Unmarshal(b, &o)
	if err == nil && (len(o.PrivateKey) != ed25519.PrivateKeySize || len(o.AdminToken) != 64) {
		err = fmt.Errorf("invalid owner file")
	}
	if err == nil && DID(ed25519.PrivateKey(o.PrivateKey).Public().(ed25519.PublicKey)) != o.DID {
		err = fmt.Errorf("owner key mismatch")
	}
	if err == nil && !bytes.Equal(ed25519.NewKeyFromSeed(o.PrivateKey[:ed25519.SeedSize]), o.PrivateKey) {
		err = fmt.Errorf("invalid Ed25519 private key")
	}
	return o, err
}

// PrivateFile rejects accidental public files and symlinks within application state.
func PrivateFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("file must be private and regular (0600): %s", path)
	}
	return nil
}
