package sshagenthost

import (
	"bytes"
	"io"
	"net"
	"testing"
)

func TestAgentChannelRejectsWrongOwner(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	defer server.Close()
	hostKey := bytes.Repeat([]byte{1}, nonceBytes)
	wrongKey := bytes.Repeat([]byte{2}, nonceBytes)
	result := make(chan error, 1)
	go func() {
		defer server.Close()
		_, err := authenticate(server, hostKey, true)
		result <- err
	}()
	if _, err := authenticate(client, wrongKey, false); err != errHandshake {
		t.Fatalf("client accepted an impersonated host: %v", err)
	}
	client.Close()
	if err := <-result; err == nil {
		t.Fatal("host accepted the wrong owner")
	}
}

func TestAgentChannelRejectsModifiedCiphertext(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	defer server.Close()
	key := bytes.Repeat([]byte{3}, nonceBytes)
	result := make(chan error, 1)
	go func() {
		ch, err := authenticate(server, key, true)
		if err == nil {
			var req request
			err = ch.read(&req)
		}
		result <- err
	}()
	ch, err := authenticate(client, key, false)
	if err != nil {
		t.Fatal(err)
	}
	// Write a correctly framed message with invalid authenticated ciphertext.
	if err := writeAll(ch.Conn, []byte{0, 0, 0, 16}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(ch.Conn, bytes.NewReader(make([]byte, 16))); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err == nil {
		t.Fatal("host accepted a modified control frame")
	}
}
