package model

import "testing"

func TestEncodeRoomKey(t *testing.T) {
	tests := []struct {
		name string
		typ  string
		room string
		want string
	}{
		{"live room", "live", "1000", "live://1000"},
		{"chat room", "chat", "room-abc", "chat://room-abc"},
		{"empty room", "live", "", "live://"},
		{"empty type", "", "1000", "://1000"},
		{"both empty", "", "", "://"},
		{"special chars", "live", "room/with/slash", "live://room/with/slash"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EncodeRoomKey(tt.typ, tt.room)
			if got != tt.want {
				t.Errorf("EncodeRoomKey(%q, %q) = %q, want %q", tt.typ, tt.room, got, tt.want)
			}
		})
	}
}

func TestDecodeRoomKey(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		wantTyp  string
		wantRoom string
		wantErr  bool
	}{
		{"live room", "live://1000", "live", "1000", false},
		{"chat room", "chat://room-abc", "chat", "room-abc", false},
		{"empty room", "live://", "live", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			typ, room, err := DecodeRoomKey(tt.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("DecodeRoomKey(%q) error = %v, wantErr %v", tt.key, err, tt.wantErr)
				return
			}
			if typ != tt.wantTyp {
				t.Errorf("DecodeRoomKey(%q) typ = %q, want %q", tt.key, typ, tt.wantTyp)
			}
			if room != tt.wantRoom {
				t.Errorf("DecodeRoomKey(%q) room = %q, want %q", tt.key, room, tt.wantRoom)
			}
		})
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	tests := []struct {
		typ  string
		room string
	}{
		{"live", "1000"},
		{"chat", "abc"},
		{"live", "99999"},
	}

	for _, tt := range tests {
		t.Run(tt.typ+"://"+tt.room, func(t *testing.T) {
			key := EncodeRoomKey(tt.typ, tt.room)
			typ, room, err := DecodeRoomKey(key)
			if err != nil {
				t.Fatalf("DecodeRoomKey(%q) error: %v", key, err)
			}
			if typ != tt.typ {
				t.Errorf("round trip typ = %q, want %q", typ, tt.typ)
			}
			if room != tt.room {
				t.Errorf("round trip room = %q, want %q", room, tt.room)
			}
		})
	}
}
