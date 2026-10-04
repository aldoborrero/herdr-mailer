// Package testutil provides isolated broker processes; never connects to a shared server.
package testutil

import (
	"bytes"
	"fmt"
	"github.com/nats-io/nats.go"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

type Broker struct {
	URL    string
	path   string
	port   int
	dir    string
	config string
	cmd    *exec.Cmd
	log    bytes.Buffer
}

func NewBroker(t *testing.T) *Broker {
	return NewBrokerWithConfig(t, "", "", "")
}

// NewBrokerWithConfig keeps authentication/ACL fixtures inside private test state.
func NewBrokerWithConfig(t *testing.T, config, user, password string) *Broker {
	t.Helper()
	path := os.Getenv("NATS_SERVER")
	if path == "" {
		var e error
		path, e = exec.LookPath("nats-server")
		if e != nil {
			t.Fatal("nats-server required: use nix develop or set NATS_SERVER; integration tests never skip")
		}
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	b := &Broker{path: path, port: port, dir: filepath.Join(t.TempDir(), "jetstream"), URL: fmt.Sprintf("nats://127.0.0.1:%d", port)}
	if user != "" {
		u, err := url.Parse(b.URL)
		if err != nil {
			t.Fatal(err)
		}
		u.User = url.UserPassword(user, password)
		b.URL = u.String()
	}
	if config != "" {
		b.config = filepath.Join(t.TempDir(), "server.conf")
		if err := os.WriteFile(b.config, []byte(config), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { b.Stop() })
	b.Start(t)
	return b
}
func (b *Broker) Start(t *testing.T) {
	t.Helper()
	b.log.Reset()
	args := []string{"-js", "-a", "127.0.0.1", "-p", strconv.Itoa(b.port), "-sd", b.dir}
	if b.config != "" {
		args = append(args, "-c", b.config)
	}
	b.cmd = exec.Command(b.path, args...)
	b.cmd.Stdout = &b.log
	b.cmd.Stderr = &b.log
	if e := b.cmd.Start(); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		nc, e := nats.Connect(b.URL, nats.Timeout(50*time.Millisecond), nats.NoReconnect())
		if e == nil {
			nc.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	b.Stop()
	t.Fatalf("broker did not start: %s", b.log.String())
}
func (b *Broker) Stop() {
	if b.cmd != nil {
		_ = b.cmd.Process.Signal(os.Interrupt)
		_ = b.cmd.Wait()
		b.cmd = nil
	}
}
