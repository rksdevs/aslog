// Package chain is the hash chain shared by the uploader and the server.
//
//	h0  = SHA256(serverNonce || runKey)
//	h_k = SHA256(h_{k-1} || ackNonce_{k-1} || canonicalLines_k)
//
// ackNonce_{k-1} is the nonce the server handed out immediately before batch k
// (the session-open nonce for the first batch, the previous ack's nonce after
// that), so a batch cannot be hashed ahead of time or replayed in another
// position.
//
// Canonical lines: every line with its trailing CR/LF removed, each followed by
// a single "\n". Zero lines canonicalise to the empty string.
package chain

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
)

// Size is the byte length of a chain hash.
const Size = sha256.Size

// Hash is one link of the chain.
type Hash [Size]byte

// String is the lowercase hex form used on the wire.
func (h Hash) String() string { return hex.EncodeToString(h[:]) }

// ParseHash decodes the hex form of a Hash.
func ParseHash(s string) (Hash, error) {
	var h Hash
	if len(s) != Size*2 {
		return h, errors.New("chain: hash must be 64 hex characters")
	}
	if _, err := hex.Decode(h[:], []byte(s)); err != nil {
		return h, errors.New("chain: hash is not valid hex")
	}
	return h, nil
}

// Equal compares two hashes in constant time.
func (h Hash) Equal(o Hash) bool { return subtle.ConstantTimeCompare(h[:], o[:]) == 1 }

func trimEOL(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// Canonical returns the canonical byte form of a batch of lines.
func Canonical(lines []string) []byte {
	n := 0
	for _, l := range lines {
		n += len(l) + 1
	}
	out := make([]byte, 0, n)
	for _, l := range lines {
		out = append(out, trimEOL(l)...)
		out = append(out, '\n')
	}
	return out
}

// Genesis computes h0 from the server nonce and the run key (its UTF-8 bytes).
func Genesis(serverNonce []byte, runKey string) Hash {
	hs := sha256.New()
	hs.Write(serverNonce)
	hs.Write([]byte(runKey))
	var h Hash
	hs.Sum(h[:0])
	return h
}

// Advance computes h_k from h_{k-1}, the nonce that preceded the batch, and the
// batch's lines.
func Advance(prev Hash, ackNonce []byte, lines []string) Hash {
	hs := sha256.New()
	hs.Write(prev[:])
	hs.Write(ackNonce)
	hs.Write(Canonical(lines))
	var h Hash
	hs.Sum(h[:0])
	return h
}

// Step is one batch as replayed by Verify.
type Step struct {
	AckNonce []byte // nonce that preceded this batch
	Lines    []string
	Hash     Hash // hash the sender claimed for this batch
}

// Verify replays steps from Genesis and returns the index of the first step
// whose claimed hash does not match, or (-1, true) when every step matches.
// Steps must be in send order with no gaps; a reordered, dropped or altered
// step breaks the chain at that index.
func Verify(serverNonce []byte, runKey string, steps []Step) (bad int, ok bool) {
	prev := Genesis(serverNonce, runKey)
	for i, s := range steps {
		next := Advance(prev, s.AckNonce, s.Lines)
		if !next.Equal(s.Hash) {
			return i, false
		}
		prev = next
	}
	return -1, true
}
