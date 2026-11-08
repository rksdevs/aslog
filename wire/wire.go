// Package wire holds the Go types for the live upload protocol, version 1.
// It is plain data with JSON tags: no I/O, no behaviour. Decoding ignores
// unknown fields (encoding/json default), so newer peers can add fields without
// breaking older ones; do not use DisallowUnknownFields on these types.
//
// Hashes and nonces are lowercase hex strings; the hash chain itself is in
// package chain.
package wire

// Protocol is the live protocol version carried in every message's "v".
const Protocol = 1

// Claims are the totals the uploader asserts for a batch, so the server can
// cross-check them against what it derives from the lines.
type Claims struct {
	Events int64 `json:"events"`
	DmgOut int64 `json:"dmgOut"`
	DmgIn  int64 `json:"dmgIn"`
	Kills  int64 `json:"kills"`
}

// Event is the compact per-event form an uploader may attach to a batch. Keys
// are one letter to keep batches small; zero values are omitted.
//
//	t  milliseconds since the batch's first line
//	k  kind (parse.Kind.String())
//	s  source, g target, p spell or aura, c school
//	n  amount, x stacks, r crit
type Event struct {
	T int64  `json:"t"`
	K string `json:"k"`
	S string `json:"s,omitempty"`
	G string `json:"g,omitempty"`
	P string `json:"p,omitempty"`
	C string `json:"c,omitempty"`
	N int    `json:"n,omitempty"`
	X int    `json:"x,omitempty"`
	R bool   `json:"r,omitempty"`
}

// Batch is one upload unit. Lines are the raw log lines (what the hash chain
// covers); Events is an optional compact decode of the same lines.
type Batch struct {
	V           int      `json:"v"`
	Seq         uint64   `json:"seq"`
	FileEpoch   int64    `json:"fileEpoch"`
	RunKey      string   `json:"runKey"`
	OffsetStart int64    `json:"offsetStart"`
	OffsetEnd   int64    `json:"offsetEnd"`
	Lines       []string `json:"lines"`
	PrevHash    string   `json:"prevHash"`
	Hash        string   `json:"hash"`
	Claims      Claims   `json:"claims"`
	Events      []Event  `json:"events,omitempty"`
}

// Ack is the server's reply to a batch. Nonce is hashed into the next batch.
type Ack struct {
	AckSeq     uint64 `json:"ackSeq"`
	Nonce      string `json:"nonce"`
	ServerTime int64  `json:"serverTime"` // unix milliseconds
}

// OpenRequest starts (or resumes) a live session for one run.
type OpenRequest struct {
	V             int    `json:"v"`
	RunKey        string `json:"runKey"`
	FileEpoch     int64  `json:"fileEpoch"`
	Token         string `json:"token,omitempty"`
	ClientVersion string `json:"clientVersion,omitempty"`
	ParseRev      int    `json:"parseRev,omitempty"`
	Resume        bool   `json:"resume,omitempty"`
	LastSeq       uint64 `json:"lastSeq,omitempty"`
}

// OpenResponse accepts or refuses a session. ServerNonce seeds the chain
// (chain.Genesis); Nonce is the first ack nonce (hashed into batch 1).
type OpenResponse struct {
	V           int    `json:"v"`
	OK          bool   `json:"ok"`
	Error       string `json:"error,omitempty"`
	SessionID   string `json:"sessionId,omitempty"`
	ServerNonce string `json:"serverNonce,omitempty"`
	Nonce       string `json:"nonce,omitempty"`
	NextSeq     uint64 `json:"nextSeq,omitempty"`
	ServerTime  int64  `json:"serverTime,omitempty"`
}
