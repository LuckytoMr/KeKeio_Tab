package server

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

type greetingWaitConnection struct {
	net.Conn
	readStarted chan struct{}
	once        sync.Once
}

func (connection *greetingWaitConnection) Read(buffer []byte) (int, error) {
	connection.once.Do(func() {
		close(connection.readStarted)
	})
	return connection.Conn.Read(buffer)
}

func TestSMTPConnectionStopsWhenServerDoesNotSendGreeting(t *testing.T) {
	clientConnection, serverConnection := net.Pipe()
	defer serverConnection.Close()

	readStarted := make(chan struct{})
	connection := &greetingWaitConnection{Conn: clientConnection, readStarted: readStarted}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	result := make(chan error, 1)
	go func() {
		result <- sendSMTPConnection(ctx, connection, SMTPTestInput{
			Host:      "smtp.example.com",
			TLS:       "none",
			From:      "sender@example.com",
			Recipient: "recipient@example.com",
		}, "SMTP context test", "test body")
	}()

	select {
	case <-readStarted:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("SMTP client did not start waiting for the server greeting")
	}

	select {
	case sendErr := <-result:
		if !errors.Is(sendErr, context.Canceled) {
			t.Fatalf("SMTP connection error = %v, want context canceled", sendErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SMTP connection remained blocked while waiting for the server greeting")
	}
}

func TestSendSMTPStopsWhenServerDoesNotSendGreeting(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	accepted := make(chan net.Conn, 1)
	acceptError := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			acceptError <- acceptErr
			return
		}
		accepted <- connection
	}()

	address := listener.Addr().(*net.TCPAddr)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	result := make(chan error, 1)
	go func() {
		result <- sendSMTP(ctx, SMTPTestInput{
			Host:      "127.0.0.1",
			Port:      address.Port,
			TLS:       "none",
			From:      "sender@example.com",
			Recipient: "recipient@example.com",
		}, "SMTP deadline test", "test body")
	}()

	var serverConnection net.Conn
	select {
	case serverConnection = <-accepted:
		defer serverConnection.Close()
		cancel()
	case acceptErr := <-acceptError:
		t.Fatalf("accept: %v", acceptErr)
	case sendErr := <-result:
		var operationError *net.OpError
		if errors.As(sendErr, &operationError) && operationError.Op == "dial" {
			t.Skipf("current environment blocks TCP loopback connections: %v", sendErr)
		}
		t.Fatalf("sendSMTP returned before the server accepted the connection: %v", sendErr)
	case <-time.After(5 * time.Second):
		t.Fatal("SMTP client did not establish the TCP connection")
	}

	select {
	case sendErr := <-result:
		if !errors.Is(sendErr, context.Canceled) {
			t.Fatalf("sendSMTP error = %v, want context canceled", sendErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sendSMTP remained blocked while waiting for the SMTP greeting")
	}
}
