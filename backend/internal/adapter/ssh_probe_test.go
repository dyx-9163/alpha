package adapter

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"aifar-deployment/backend/internal/store"
)

func TestProbeSSHContextDeadlineStopsStalledHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		time.Sleep(500 * time.Millisecond)
	}()

	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = ProbeSSH(ctx, store.Server{
		Host: host, Port: port, Username: "root",
		AuthType: "password", Password: "test-only-password",
	})
	if err == nil {
		t.Fatal("expected stalled SSH handshake to fail")
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("context deadline did not stop SSH handshake: %s", elapsed)
	}
}
