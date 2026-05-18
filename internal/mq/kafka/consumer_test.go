package kafka

import (
	"testing"

	"github.com/Terry-Mao/goim/internal/mq"
)

func TestIsDeadLetter(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "DeadLetterError returns true",
			err:  &mq.DeadLetterError{Err: errSentinel, Reason: "max retries", Retries: 3},
			want: true,
		},
		{
			name: "plain error returns false",
			err:  errSentinel,
			want: false,
		},
		{
			name: "wrapped error without DeadLetter returns false",
			err:  &someError{msg: "not dl"},
			want: false,
		},
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDeadLetter(tt.err); got != tt.want {
				t.Errorf("isDeadLetter() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDeadLetterError_Error(t *testing.T) {
	dl := &mq.DeadLetterError{
		Err:     errSentinel,
		Reason:  "max retries exceeded (3)",
		Retries: 3,
	}
	s := dl.Error()
	if s == "" {
		t.Error("Error() returned empty string")
	}
}

func TestDeadLetterError_Unwrap(t *testing.T) {
	dl := &mq.DeadLetterError{
		Err:    errSentinel,
		Reason: "test",
	}
	if dl.Unwrap() != errSentinel {
		t.Error("Unwrap() should return wrapped error")
	}
}

func TestMaxRetries(t *testing.T) {
	if mq.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", mq.MaxRetries)
	}
}

var errSentinel = &someError{msg: "sentinel"}

type someError struct{ msg string }

func (e *someError) Error() string { return e.msg }
