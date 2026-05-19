package tracectx

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

const (
	TraceIDHeader       = "X-Trace-ID"
	CorrelationIDHeader = "X-Correlation-ID"
	TraceParentHeader   = "traceparent"
)

type traceIDKey struct{}

// WithTraceID stores a trace identifier in context.
func WithTraceID(ctx context.Context, traceID string) context.Context {
	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		return ctx
	}
	return context.WithValue(ctx, traceIDKey{}, traceID)
}

// TraceID returns the trace identifier stored in context.
func TraceID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(traceIDKey{}).(string); ok {
		return v
	}
	return ""
}

// FromHTTP extracts trace metadata from standard and goim-specific headers.
func FromHTTP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if v := strings.TrimSpace(r.Header.Get(TraceIDHeader)); v != "" {
		return v
	}
	if v := strings.TrimSpace(r.Header.Get(CorrelationIDHeader)); v != "" {
		return v
	}
	return TraceIDFromTraceParent(r.Header.Get(TraceParentHeader))
}

// WithHTTPTrace returns a context carrying trace metadata from HTTP headers.
func WithHTTPTrace(ctx context.Context, r *http.Request) context.Context {
	return WithTraceID(ctx, FromHTTP(r))
}

// InjectHTTP writes trace metadata to outbound HTTP headers.
func InjectHTTP(req *http.Request, traceID string) {
	traceID = strings.TrimSpace(traceID)
	if req == nil || traceID == "" {
		return
	}
	req.Header.Set(TraceIDHeader, traceID)
	req.Header.Set(CorrelationIDHeader, traceID)
}

// FromHeaders extracts trace metadata from MQ-style headers.
func FromHeaders(headers map[string]string) string {
	if len(headers) == 0 {
		return ""
	}
	for _, key := range []string{"goim_trace_id", TraceIDHeader, CorrelationIDHeader, TraceParentHeader} {
		if v := strings.TrimSpace(headers[key]); v != "" {
			if key == TraceParentHeader {
				return TraceIDFromTraceParent(v)
			}
			return v
		}
	}
	return ""
}

// FromJSONPayload extracts trace_id/correlation_id from a JSON object.
func FromJSONPayload(payload []byte) string {
	var m map[string]any
	if len(payload) == 0 || json.Unmarshal(payload, &m) != nil {
		return ""
	}
	for _, key := range []string{"trace_id", "correlation_id"} {
		if v, ok := m[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// TraceIDFromTraceParent extracts the W3C trace id portion from traceparent.
func TraceIDFromTraceParent(traceparent string) string {
	parts := strings.Split(strings.TrimSpace(traceparent), "-")
	if len(parts) >= 2 && len(parts[1]) == 32 {
		return parts[1]
	}
	return ""
}
