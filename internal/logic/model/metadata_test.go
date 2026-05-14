package model

import "testing"

func TestMetadataConstants(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"MetaWeight", MetaWeight, "weight"},
		{"MetaOffline", MetaOffline, "offline"},
		{"MetaAddrs", MetaAddrs, "addrs"},
		{"MetaIPCount", MetaIPCount, "ip_count"},
		{"MetaConnCount", MetaConnCount, "conn_count"},
		{"PlatformWeb", PlatformWeb, "web"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestMetadataConstantsDistinct(t *testing.T) {
	consts := map[string]string{
		"MetaWeight":    MetaWeight,
		"MetaOffline":   MetaOffline,
		"MetaAddrs":     MetaAddrs,
		"MetaIPCount":   MetaIPCount,
		"MetaConnCount": MetaConnCount,
	}

	seen := make(map[string]string)
	for name, val := range consts {
		if prev, ok := seen[val]; ok {
			t.Errorf("duplicate constant value %q: both %s and %s", val, prev, name)
		}
		seen[val] = name
	}
}
