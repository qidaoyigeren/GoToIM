package errors

import (
	"testing"
)

func TestErrorConstantsNonNil(t *testing.T) {
	errors := map[string]error{
		"ErrHandshake":            ErrHandshake,
		"ErrOperation":            ErrOperation,
		"ErrRingEmpty":            ErrRingEmpty,
		"ErrRingFull":             ErrRingFull,
		"ErrTimerFull":            ErrTimerFull,
		"ErrTimerEmpty":           ErrTimerEmpty,
		"ErrTimerNoItem":          ErrTimerNoItem,
		"ErrPushMsgArg":           ErrPushMsgArg,
		"ErrPushMsgsArg":          ErrPushMsgsArg,
		"ErrMPushMsgArg":          ErrMPushMsgArg,
		"ErrMPushMsgsArg":         ErrMPushMsgsArg,
		"ErrSignalFullMsgDropped": ErrSignalFullMsgDropped,
		"ErrBroadCastArg":         ErrBroadCastArg,
		"ErrBroadCastRoomArg":     ErrBroadCastRoomArg,
		"ErrRoomDroped":           ErrRoomDroped,
		"ErrLogic":                ErrLogic,
	}

	for name, err := range errors {
		t.Run(name, func(t *testing.T) {
			if err == nil {
				t.Errorf("%s is nil", name)
			}
			if err.Error() == "" {
				t.Errorf("%s has empty message", name)
			}
		})
	}
}

func TestErrorMessages(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantMsg string
	}{
		{"ErrHandshake", ErrHandshake, "handshake failed"},
		{"ErrOperation", ErrOperation, "request operation not valid"},
		{"ErrRingEmpty", ErrRingEmpty, "ring buffer empty"},
		{"ErrRingFull", ErrRingFull, "ring buffer full"},
		{"ErrTimerFull", ErrTimerFull, "timer full"},
		{"ErrTimerEmpty", ErrTimerEmpty, "timer empty"},
		{"ErrTimerNoItem", ErrTimerNoItem, "timer item not exist"},
		{"ErrPushMsgArg", ErrPushMsgArg, "rpc pushmsg arg error"},
		{"ErrPushMsgsArg", ErrPushMsgsArg, "rpc pushmsgs arg error"},
		{"ErrMPushMsgArg", ErrMPushMsgArg, "rpc mpushmsg arg error"},
		{"ErrMPushMsgsArg", ErrMPushMsgsArg, "rpc mpushmsgs arg error"},
		{"ErrSignalFullMsgDropped", ErrSignalFullMsgDropped, "signal channel full, msg dropped"},
		{"ErrBroadCastArg", ErrBroadCastArg, "rpc broadcast arg error"},
		{"ErrBroadCastRoomArg", ErrBroadCastRoomArg, "rpc broadcast  room arg error"},
		{"ErrRoomDroped", ErrRoomDroped, "room droped"},
		{"ErrLogic", ErrLogic, "logic rpc is not available"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Error() != tt.wantMsg {
				t.Errorf("%s.Error() = %q, want %q", tt.name, tt.err.Error(), tt.wantMsg)
			}
		})
	}
}

func TestErrorsAreDistinct(t *testing.T) {
	allErrors := []error{
		ErrHandshake, ErrOperation,
		ErrRingEmpty, ErrRingFull,
		ErrTimerFull, ErrTimerEmpty, ErrTimerNoItem,
		ErrPushMsgArg, ErrPushMsgsArg, ErrMPushMsgArg, ErrMPushMsgsArg,
		ErrSignalFullMsgDropped,
		ErrBroadCastArg, ErrBroadCastRoomArg,
		ErrRoomDroped, ErrLogic,
	}

	seen := make(map[string]bool)
	for _, err := range allErrors {
		msg := err.Error()
		if seen[msg] {
			t.Errorf("duplicate error message: %q", msg)
		}
		seen[msg] = true
	}
}
