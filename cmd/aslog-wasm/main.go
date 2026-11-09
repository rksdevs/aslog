//go:build js && wasm

// aslog-wasm runs the review step in a browser, so a log can be checked and trimmed before any of it
// leaves the player's computer. Build:
//
//	GOOS=js GOARCH=wasm go build -ldflags="-s -w" -o aslog.wasm ./cmd/aslog-wasm
//
// JavaScript gets two functions:
//
//	aslogOpen(Uint8Array) -> JSON string: {"runs": [...]} or {"error": "..."}
//	aslogExtract("[1,3]") -> Uint8Array of gzip, or a string error
//	aslogSaves("[1,3]", addon Uint8Array|null, stats Uint8Array|null) -> {addon, stats, addonRuns,
//	  statsRuns, addonError, statsError}: each save cut down to the chosen runs (Uint8Array, or null
//	  when no chosen run has data in it), or a string error
package main

import (
	"encoding/json"
	"syscall/js"

	"github.com/rksdevs/aslog/parse"
	"github.com/rksdevs/aslog/review"
)

var current *review.Scan

func open(_ js.Value, args []js.Value) any {
	data := make([]byte, args[0].Get("length").Int())
	js.CopyBytesToGo(data, args[0])
	scan, err := review.Open(data)
	if err != nil {
		text, _ := json.Marshal(map[string]string{"error": err.Error()})
		return string(text)
	}
	current = scan
	text, err := json.Marshal(struct {
		*review.Scan
		ParseRev int `json:"parseRev"`
	}{scan, parse.ParseRev})
	if err != nil {
		return `{"error":"could not read this log"}`
	}
	return string(text)
}

func extract(_ js.Value, args []js.Value) any {
	if current == nil {
		return "no log open"
	}
	var indexes []int
	if err := json.Unmarshal([]byte(args[0].String()), &indexes); err != nil {
		return err.Error()
	}
	gz, err := current.Extract(indexes)
	if err != nil {
		return err.Error()
	}
	out := js.Global().Get("Uint8Array").New(len(gz))
	js.CopyBytesToJS(out, gz)
	return out
}

func bytesOf(v js.Value) string {
	if v.IsUndefined() || v.IsNull() {
		return ""
	}
	data := make([]byte, v.Get("length").Int())
	js.CopyBytesToGo(data, v)
	return string(data)
}

func toJS(data []byte) any {
	if data == nil {
		return nil
	}
	out := js.Global().Get("Uint8Array").New(len(data))
	js.CopyBytesToJS(out, data)
	return out
}

func errText(err error) any {
	if err == nil {
		return nil
	}
	return err.Error()
}

// chosenSaves cuts the addon saves down to the chosen runs, so the rest of a player's history,
// Azeroth Stats backups and co-op names never leave the browser.
func chosenSaves(_ js.Value, args []js.Value) any {
	if current == nil {
		return "no log open"
	}
	var indexes []int
	if err := json.Unmarshal([]byte(args[0].String()), &indexes); err != nil {
		return err.Error()
	}
	got := current.ChosenSaves(indexes, bytesOf(args[1]), bytesOf(args[2]))
	return map[string]any{
		"addon": toJS(got.Addon), "stats": toJS(got.Stats),
		"addonRuns": got.AddonRuns, "statsRuns": got.StatsRuns,
		"addonError": errText(got.AddonErr), "statsError": errText(got.StatsErr),
	}
}

func main() {
	js.Global().Set("aslogOpen", js.FuncOf(open))
	js.Global().Set("aslogExtract", js.FuncOf(extract))
	js.Global().Set("aslogSaves", js.FuncOf(chosenSaves))
	select {}
}
