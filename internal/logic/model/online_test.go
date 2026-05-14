package model

import (
	"encoding/json"
	"testing"
)

func TestOnlineStruct(t *testing.T) {
	o := Online{
		Server:    "comet-1",
		RoomCount: map[string]int32{"live://1000": 5, "live://2000": 3},
		Updated:   1700000000,
	}

	if o.Server != "comet-1" {
		t.Errorf("Server = %q, want %q", o.Server, "comet-1")
	}
	if o.RoomCount["live://1000"] != 5 {
		t.Errorf("RoomCount[live://1000] = %d, want 5", o.RoomCount["live://1000"])
	}
	if o.Updated != 1700000000 {
		t.Errorf("Updated = %d, want 1700000000", o.Updated)
	}
}

func TestOnlineJSON(t *testing.T) {
	o := Online{
		Server:    "comet-1",
		RoomCount: map[string]int32{"live://1000": 10},
		Updated:   1700000000,
	}

	data, err := json.Marshal(o)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}

	var decoded Online
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}

	if decoded.Server != o.Server {
		t.Errorf("decoded Server = %q, want %q", decoded.Server, o.Server)
	}
	if decoded.RoomCount["live://1000"] != 10 {
		t.Errorf("decoded RoomCount = %v, want map[live://1000:10]", decoded.RoomCount)
	}
}

func TestOnlineZeroValue(t *testing.T) {
	var o Online
	if o.Server != "" {
		t.Errorf("zero Server = %q, want empty", o.Server)
	}
	if o.RoomCount != nil {
		t.Errorf("zero RoomCount = %v, want nil", o.RoomCount)
	}
	if o.Updated != 0 {
		t.Errorf("zero Updated = %d, want 0", o.Updated)
	}
}

func TestTopStruct(t *testing.T) {
	top := Top{
		RoomID: "live://1000",
		Count:  42,
	}

	if top.RoomID != "live://1000" {
		t.Errorf("RoomID = %q, want %q", top.RoomID, "live://1000")
	}
	if top.Count != 42 {
		t.Errorf("Count = %d, want 42", top.Count)
	}
}

func TestTopJSON(t *testing.T) {
	top := Top{RoomID: "live://1000", Count: 42}
	data, err := json.Marshal(top)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}

	var decoded Top
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}

	if decoded.RoomID != top.RoomID || decoded.Count != top.Count {
		t.Errorf("decoded = %+v, want %+v", decoded, top)
	}
}
