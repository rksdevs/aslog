package wire

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestBatchRoundTrip(t *testing.T) {
	in := Batch{
		V: Protocol, Seq: 7, FileEpoch: 1730845551, RunKey: "rk",
		OffsetStart: 100, OffsetEnd: 4096,
		Lines:    []string{"11/5 22:05:52.888  Your Fireball hits Kobold Vermin for 70 Fire damage."},
		PrevHash: "aa", Hash: "bb",
		Claims: Claims{Events: 1, DmgOut: 70},
		Events: []Event{{T: 0, K: "damage_out_spell", G: "Kobold Vermin", P: "Fireball", C: "Fire", N: 70, R: true}},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Batch
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip differs:\n%+v\n%+v", in, out)
	}
}

func TestBatchJSONKeys(t *testing.T) {
	b, _ := json.Marshal(Batch{V: 1})
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"v", "seq", "fileEpoch", "runKey", "offsetStart", "offsetEnd", "lines", "prevHash", "hash", "claims"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing key %q", k)
		}
	}
	if _, ok := m["events"]; ok {
		t.Error("empty events should be omitted")
	}
	c := m["claims"].(map[string]any)
	for _, k := range []string{"events", "dmgOut", "dmgIn", "kills"} {
		if _, ok := c[k]; !ok {
			t.Errorf("missing claims key %q", k)
		}
	}
}

func TestAckAndOpenRoundTrip(t *testing.T) {
	ack := Ack{AckSeq: 3, Nonce: "ff00", ServerTime: 1730845551123}
	req := OpenRequest{V: Protocol, RunKey: "rk", FileEpoch: 5, Token: "t", ClientVersion: "1.2.3", ParseRev: 1, Resume: true, LastSeq: 9}
	resp := OpenResponse{V: Protocol, OK: true, SessionID: "s", ServerNonce: "aa", Nonce: "bb", NextSeq: 10, ServerTime: 1}
	for _, v := range []any{&ack, &req, &resp} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		out := reflect.New(reflect.TypeOf(v).Elem()).Interface()
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(v, out) {
			t.Fatalf("%T round trip differs: %+v vs %+v", v, v, out)
		}
	}
}

func TestUnknownFieldsIgnored(t *testing.T) {
	var b Batch
	err := json.Unmarshal([]byte(`{"v":1,"seq":2,"future":{"x":1},"claims":{"events":3,"extra":9},"events":[{"t":1,"k":"died","zzz":true}],"lines":[]}`), &b)
	if err != nil {
		t.Fatal(err)
	}
	if b.Seq != 2 || b.Claims.Events != 3 || len(b.Events) != 1 || b.Events[0].K != "died" {
		t.Fatalf("decoded wrong: %+v", b)
	}
	var a Ack
	if err := json.Unmarshal([]byte(`{"ackSeq":1,"nonce":"n","serverTime":2,"newField":[1,2]}`), &a); err != nil || a.AckSeq != 1 {
		t.Fatalf("ack: %v %+v", err, a)
	}
	var r OpenResponse
	if err := json.Unmarshal([]byte(`{"v":1,"ok":true,"somethingNew":"x"}`), &r); err != nil || !r.OK {
		t.Fatalf("open response: %v %+v", err, r)
	}
	var q OpenRequest
	if err := json.Unmarshal([]byte(`{"v":1,"runKey":"k","somethingNew":"x"}`), &q); err != nil || q.RunKey != "k" {
		t.Fatalf("open request: %v %+v", err, q)
	}
}

func TestProtocolConst(t *testing.T) {
	if Protocol != 1 {
		t.Fatal("protocol v1")
	}
}
