package config

import (
	"strings"
	"testing"
	"time"
)

func TestParseStreamRetention(t *testing.T) {
	for raw, want := range map[string]time.Duration{
		"168h": 7 * 24 * time.Hour,
		"72h":  72 * time.Hour,
		"1h":   time.Hour,
	} {
		got, err := parseStreamRetention(raw)
		if err != nil || got != want {
			t.Errorf("parseStreamRetention(%q) = %v, %v; want %v", raw, got, err, want)
		}
	}
	// Under an hour could drop a status services/api has not read during a
	// restart; "0" would drop every entry. Both fail the boot.
	for _, raw := range []string{"30m", "0", "0s", "-168h", "7d", "forever"} {
		if _, err := parseStreamRetention(raw); err == nil || !strings.Contains(err.Error(), "PACA_STREAM_RETENTION") {
			t.Errorf("parseStreamRetention(%q) = %v, want an error naming PACA_STREAM_RETENTION", raw, err)
		}
	}
}

func TestLoad_StreamRetention(t *testing.T) {
	for k, v := range map[string]string{
		"DATABASE_URL":                   "postgres://x",
		"VALKEY_URL":                     "redis://x:6379/0",
		"ENCRYPTION_KEY":                 strings.Repeat("ab", 32),
		"AGENT_SERVER_IMAGE":             "img:1",
		"INTERNAL_API_KEY":               "k",
		"AGENT_RUNNER_ALLOWED_AGENT_IDS": "*",
		"SANDBOX_BACKEND":                "docker",
	} {
		t.Setenv(k, v)
	}

	t.Setenv("PACA_STREAM_RETENTION", "")
	s, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.StreamRetention != 7*24*time.Hour {
		t.Fatalf("default StreamRetention = %v, want 168h", s.StreamRetention)
	}

	t.Setenv("PACA_STREAM_RETENTION", "48h")
	if s, err = Load(); err != nil || s.StreamRetention != 48*time.Hour {
		t.Fatalf("PACA_STREAM_RETENTION=48h: %v, %v", s.StreamRetention, err)
	}

	t.Setenv("PACA_STREAM_RETENTION", "10m")
	if _, err = Load(); err == nil {
		t.Fatal("PACA_STREAM_RETENTION=10m: want an error")
	}
}

func TestValidatePortRange(t *testing.T) {
	cases := []struct {
		name       string
		start, end int
		wantErr    bool
	}{
		{"both zero disables the feature", 0, 0, false},
		{"valid range", 2200, 2299, false},
		{"start equals end", 2200, 2200, false},
		{"only start set", 2200, 0, true},
		{"only end set", 0, 2299, true},
		{"end before start", 2299, 2200, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePortRange("SSH_BASTION_PORT_RANGE", tc.start, tc.end)
			if tc.wantErr && err == nil {
				t.Errorf("validatePortRange(%d, %d) = nil, want error", tc.start, tc.end)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validatePortRange(%d, %d) = %v, want nil", tc.start, tc.end, err)
			}
		})
	}
}
