package chain

import (
	"bytes"
	"crypto/sha256"
	"testing"
)

var (
	serverNonce = []byte("server-nonce-0123456789abcdef")
	runKey      = "run-key-1"
)

func build(nonces [][]byte, batches [][]string) []Step {
	prev := Genesis(serverNonce, runKey)
	steps := make([]Step, len(batches))
	for i, b := range batches {
		prev = Advance(prev, nonces[i], b)
		steps[i] = Step{AckNonce: nonces[i], Lines: b, Hash: prev}
	}
	return steps
}

func sample() []Step {
	return build(
		[][]byte{[]byte("n0"), []byte("n1"), []byte("n2")},
		[][]string{{"a", "b"}, {"c"}, {"d", "e", "f"}})
}

func TestCanonical(t *testing.T) {
	got := Canonical([]string{"a\r\n", "b\n", "c", ""})
	if string(got) != "a\nb\nc\n\n" {
		t.Fatalf("got %q", got)
	}
	if len(Canonical(nil)) != 0 {
		t.Fatal("no lines should be empty")
	}
}

func TestGenesisAndAdvanceMatchSpec(t *testing.T) {
	h0 := sha256.Sum256(append(append([]byte{}, serverNonce...), "run-key-1"...))
	if Genesis(serverNonce, runKey) != Hash(h0) {
		t.Fatal("genesis differs from SHA256(serverNonce||runKey)")
	}
	var buf bytes.Buffer
	buf.Write(h0[:])
	buf.WriteString("ack")
	buf.WriteString("x\ny\n")
	want := sha256.Sum256(buf.Bytes())
	if got := Advance(Hash(h0), []byte("ack"), []string{"x", "y\r\n"}); got != Hash(want) {
		t.Fatal("advance differs from SHA256(prev||nonce||canonical)")
	}
}

func TestDeterministic(t *testing.T) {
	a, b := sample(), sample()
	for i := range a {
		if a[i].Hash != b[i].Hash {
			t.Fatalf("step %d not deterministic", i)
		}
	}
	if bad, ok := Verify(serverNonce, runKey, a); !ok || bad != -1 {
		t.Fatalf("clean chain rejected at %d", bad)
	}
	if bad, ok := Verify(serverNonce, runKey, nil); !ok || bad != -1 {
		t.Fatal("empty chain should verify")
	}
}

func TestLineEndingsDoNotMatter(t *testing.T) {
	steps := sample()
	steps[0].Lines = []string{"a\r\n", "b\n"}
	if bad, ok := Verify(serverNonce, runKey, steps); !ok {
		t.Fatalf("CRLF changed hash at %d", bad)
	}
}

func TestTamperDetected(t *testing.T) {
	steps := sample()
	steps[1].Lines = []string{"C"}
	if bad, ok := Verify(serverNonce, runKey, steps); ok || bad != 1 {
		t.Fatalf("tamper: bad=%d ok=%v, want 1,false", bad, ok)
	}
	// Moving a line between batches is caught at the first batch touched.
	steps = sample()
	steps[0].Lines = []string{"a"}
	steps[1].Lines = []string{"b", "c"}
	if bad, ok := Verify(serverNonce, runKey, steps); ok || bad != 0 {
		t.Fatalf("moved line: bad=%d ok=%v, want 0,false", bad, ok)
	}
	// Flipping a hash bit.
	steps = sample()
	steps[2].Hash[0] ^= 1
	if bad, ok := Verify(serverNonce, runKey, steps); ok || bad != 2 {
		t.Fatalf("hash flip: bad=%d ok=%v", bad, ok)
	}
}

func TestWrongNonceAndKey(t *testing.T) {
	steps := sample()
	if bad, ok := Verify([]byte("other"), runKey, steps); ok || bad != 0 {
		t.Fatalf("wrong server nonce: bad=%d ok=%v", bad, ok)
	}
	if bad, ok := Verify(serverNonce, "run-key-2", steps); ok || bad != 0 {
		t.Fatalf("wrong run key: bad=%d ok=%v", bad, ok)
	}
	steps[1].AckNonce = []byte("forged")
	if bad, ok := Verify(serverNonce, runKey, steps); ok || bad != 1 {
		t.Fatalf("wrong ack nonce: bad=%d ok=%v, want 1,false", bad, ok)
	}
}

func TestReorderDropDuplicate(t *testing.T) {
	steps := sample()
	swapped := []Step{steps[1], steps[0], steps[2]}
	if bad, ok := Verify(serverNonce, runKey, swapped); ok || bad != 0 {
		t.Fatalf("reorder: bad=%d ok=%v", bad, ok)
	}
	dropped := []Step{steps[0], steps[2]}
	if bad, ok := Verify(serverNonce, runKey, dropped); ok || bad != 1 {
		t.Fatalf("drop: bad=%d ok=%v", bad, ok)
	}
	dup := []Step{steps[0], steps[0], steps[1]}
	if bad, ok := Verify(serverNonce, runKey, dup); ok || bad != 1 {
		t.Fatalf("duplicate: bad=%d ok=%v", bad, ok)
	}
}

func TestHashHex(t *testing.T) {
	h := Genesis(serverNonce, runKey)
	back, err := ParseHash(h.String())
	if err != nil || back != h {
		t.Fatalf("round trip: %v", err)
	}
	for _, bad := range []string{"", "zz", h.String()[:63], h.String() + "0", "g" + h.String()[1:]} {
		if _, err := ParseHash(bad); err == nil {
			t.Errorf("ParseHash(%q) should fail", bad)
		}
	}
}
