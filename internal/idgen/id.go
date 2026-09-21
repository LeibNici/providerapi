package idgen

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

func New(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(b[:]))
}

func RequestID() string { return New("req") }
func StreamID() string  { return New("str") }
func ChatID() string    { return New("chatcmpl") }
func CallID() string    { return New("call") }
