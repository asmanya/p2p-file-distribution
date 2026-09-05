package peer

import (
	"net"
	"testing"
)

func testConnPair() (*Conn, net.Conn) {
	client, server := net.Pipe()
	return newConn(client, [20]byte{}, [8]byte{}), server
}

func TestSendSimpleMessage(t *testing.T) {
	tests := []struct {
		name string
		send func(*Conn) error
		want MessageID
	}{
		{"interested", (*Conn).SendInterested, MsgInterested},
		{"not interested", (*Conn).SendNotInterested, MsgNotInterested},
		{"choke", (*Conn).SendChoke, MsgChoke},
		{"unchoke", (*Conn).SendUnchoke, MsgUnchoke},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, server := testConnPair()
			defer c.Close()
			defer server.Close()

			done := make(chan error, 1)
			go func() { done <- tt.send(c) }()

			got, err := ReadMessage(server)
			if err != nil {
				t.Fatalf("ReadMessage: %v", err)
			}
			if err := <-done; err != nil {
				t.Fatalf("send: %v", err)
			}
			if got.ID != tt.want {
				t.Errorf("got ID %s, want %s", got.ID, tt.want)
			}
			if len(got.Payload) != 0 {
				t.Errorf("got payload %v, want empty", got.Payload)
			}
		})
	}
}

func TestSendHave(t *testing.T) {
	c, server := testConnPair()
	defer c.Close()
	defer server.Close()

	done := make(chan error, 1)
	go func() { done <- c.SendHave(21) }()

	got, err := ReadMessage(server)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("send: %v", err)
	}

	have, err := ParseHavePayload(got.Payload, 100)
	if err != nil {
		t.Fatalf("ParseHavePayload: %v", err)
	}
	if have.Index != 21 {
		t.Errorf("got index %d, want 21", have.Index)
	}
}

func TestSendRequest(t *testing.T) {
	c, server := testConnPair()
	defer c.Close()
	defer server.Close()

	done := make(chan error, 1)
	go func() { done <- c.SendRequest(3, 16384, 16384) }()

	got, err := ReadMessage(server)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("send: %v", err)
	}

	req, err := ParseRequestPayload(got.Payload, 100, 32768)
	if err != nil {
		t.Fatalf("ParseRequestPayload: %v", err)
	}
	if req.Index != 3 || req.Begin != 16384 || req.Length != 16384 {
		t.Errorf("got %+v, want {316384 16384}", req)
	}
}
