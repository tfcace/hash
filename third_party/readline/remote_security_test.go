package readline

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestReadMessageRejectsInvalidLengthsBeforeAllocation(t *testing.T) {
	for _, length := range []int32{1, maxRemoteMessageSize + 1} {
		var frame bytes.Buffer
		if err := binary.Write(&frame, binary.BigEndian, length); err != nil {
			t.Fatal(err)
		}
		if err := binary.Write(&frame, binary.BigEndian, T_DATA); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadMessage(&frame); err == nil {
			t.Fatalf("ReadMessage accepted invalid length %d", length)
		}
	}
}

func TestReadMessageReadsBoundedPayload(t *testing.T) {
	payload := []byte("pods")
	var frame bytes.Buffer
	if err := binary.Write(&frame, binary.BigEndian, int32(len(payload)+2)); err != nil {
		t.Fatal(err)
	}
	if err := binary.Write(&frame, binary.BigEndian, T_DATA); err != nil {
		t.Fatal(err)
	}
	if _, err := frame.Write(payload); err != nil {
		t.Fatal(err)
	}
	message, err := ReadMessage(&frame)
	if err != nil {
		t.Fatal(err)
	}
	if message.Type != T_DATA || !bytes.Equal(message.Data, payload) {
		t.Fatalf("ReadMessage() = type %v payload %q", message.Type, message.Data)
	}
}

func TestWriteToRejectsOversizedPayload(t *testing.T) {
	message := NewMessage(T_DATA, make([]byte, int(maxRemoteMessageSize)-1))
	var frame bytes.Buffer
	if _, err := message.WriteTo(&frame); err == nil {
		t.Fatal("WriteTo accepted an oversized payload")
	}
	if frame.Len() != 0 {
		t.Fatalf("WriteTo emitted %d bytes for an oversized payload", frame.Len())
	}
}

func TestMessageRoundTrip(t *testing.T) {
	original := NewMessage(T_DATA, []byte("pods"))
	var frame bytes.Buffer
	if _, err := original.WriteTo(&frame); err != nil {
		t.Fatal(err)
	}
	decoded, err := ReadMessage(&frame)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Type != original.Type || !bytes.Equal(decoded.Data, original.Data) {
		t.Fatalf("round trip = type %v payload %q", decoded.Type, decoded.Data)
	}
}
