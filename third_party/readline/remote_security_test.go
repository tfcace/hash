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
