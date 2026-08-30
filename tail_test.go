package main

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestTailReturnsOnlyTheBoundedRetainedBytesForOnePane(t *testing.T) {
	reg := newRegistry("/bin/sh")
	value := &session{paneID: "tab-a.1", ring: newRing(32)}
	value.ring.write([]byte("0123456789"))
	reg.sessions[1] = value
	daemon := &daemon{registry: reg}
	encoded, _ := json.Marshal(tailRequest{PaneID: "tab-a.1", Bytes: 4})
	code, raw, err := daemon.tail(map[string]json.RawMessage{"request": encoded})
	if err != nil || code != "" {
		t.Fatalf("tail failed: code=%q err=%v", code, err)
	}
	answer := raw.(map[string]any)
	decoded, err := base64.StdEncoding.DecodeString(answer["dataB64"].(string))
	if err != nil || string(decoded) != "6789" {
		t.Fatalf("tail bytes=%q err=%v", decoded, err)
	}
	if answer["retained"] != 10 || answer["returned"] != 4 {
		t.Fatalf("tail counts=%v", answer)
	}
}
