package service

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Verify startup, owner-only access, a real request, and graceful socket cleanup.
func TestLifecycle(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "diskmap-service-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "run", "control.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- Run(ctx, socket) }()
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	var response *http.Response
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		response, err = client.Get("http://unix/api/v1/health")
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(body), `"ready":true`) {
		t.Fatalf("unexpected service health: %s, %v", body, err)
	}
	info, err := os.Stat(socket)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("unsafe socket permissions: %v", info.Mode())
	}
	cancel()
	select {
	case err = <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("shutdown timed out")
	}
	if _, err = os.Stat(socket); !os.IsNotExist(err) {
		t.Fatal("socket remains after stop")
	}
}

// TestShutdownWaitsForActiveResponse prevents daemon exit while a response is still running.
func TestShutdownWaitsForActiveResponse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write([]byte("complete response"))
	})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- serveUntilCanceled(ctx, server, listener, time.Second) }()
	response := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 2 * time.Second}
		r, err := client.Get("http://" + listener.Addr().String())
		if err != nil {
			response <- err
			return
		}
		defer r.Body.Close()
		data, err := io.ReadAll(r.Body)
		if err == nil && string(data) != "complete response" {
			err = errors.New("incomplete response")
		}
		response <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("handler not entered")
	}
	cancel()
	select {
	case err := <-result:
		t.Fatalf("shutdown returned before response: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-response; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not finish")
	}
}

// TestShutdownDeadlineClosesConnection proves timeout propagation and bounded termination.
func TestShutdownDeadlineClosesConnection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	entered, exited := make(chan struct{}), make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(exited)
	})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- serveUntilCanceled(ctx, server, listener, 30*time.Millisecond) }()
	response := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: time.Second}
		r, err := client.Get("http://" + listener.Addr().String())
		if r != nil {
			r.Body.Close()
		}
		response <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("handler not entered")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("timeout not propagated: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown exceeded bound")
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("active connection not closed")
	}
	if err := <-response; err == nil {
		t.Fatal("expired response unexpectedly succeeded")
	}
}

// TestServeFailureReturns preserves listener failures instead of hiding them as shutdown success.
func TestServeFailureReturns(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener.Close()
	err = serveUntilCanceled(context.Background(), &http.Server{}, listener, time.Second)
	if err == nil || !errors.Is(err, net.ErrClosed) {
		t.Fatalf("listener error lost: %v", err)
	}
}
