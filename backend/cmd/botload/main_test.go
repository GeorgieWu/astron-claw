package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestResolveWSURL(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		want    string
		wantErr bool
	}{
		{name: "full ws url", host: "ws://example.com/bridge/bot", want: "ws://example.com/bridge/bot"},
		{name: "http host", host: "http://example.com", want: "ws://example.com/bridge/bot"},
		{name: "https host", host: "https://example.com/api", want: "wss://example.com/api/bridge/bot"},
		{name: "bare host", host: "example.com:8765", want: "ws://example.com:8765/bridge/bot"},
		{name: "empty", host: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveWSURL(tt.host)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveWSURL error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("resolveWSURL(%q) = %q, want %q", tt.host, got, tt.want)
			}
		})
	}
}

func TestLoadTokensAndNormalizeConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens.txt")
	content := "\n# comment\nsk-1\n\nsk-2\nsk-3\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	tokens, err := loadTokens(path)
	if err != nil {
		t.Fatalf("loadTokens error: %v", err)
	}
	wantTokens := []string{"sk-1", "sk-2", "sk-3"}
	if !reflect.DeepEqual(tokens, wantTokens) {
		t.Fatalf("tokens = %#v, want %#v", tokens, wantTokens)
	}

	cfg := config{
		Host:             "pre.example.com:8765",
		TokenFile:        path,
		Target:           10,
		BatchSize:        0,
		BatchInterval:    0,
		PingInterval:     0,
		ReportInterval:   0,
		HandshakeTimeout: 0,
		ReconnectDelay:   0,
	}
	cfg, err = normalizeConfig(cfg, len(tokens))
	if err != nil {
		t.Fatalf("normalizeConfig error: %v", err)
	}
	if cfg.Target != len(tokens) {
		t.Fatalf("target = %d, want %d", cfg.Target, len(tokens))
	}
	if cfg.BatchSize <= 0 {
		t.Fatalf("batch size = %d, want > 0", cfg.BatchSize)
	}
	if cfg.BatchInterval <= 0 || cfg.PingInterval <= 0 || cfg.ReportInterval <= 0 || cfg.HandshakeTimeout <= 0 || cfg.ReconnectDelay <= 0 {
		t.Fatalf("expected positive default durations, got %+v", cfg)
	}
	if cfg.WSURL != "ws://pre.example.com:8765/bridge/bot" {
		t.Fatalf("ws url = %q", cfg.WSURL)
	}
}

func TestNormalizeConfigRejectsInvalidTarget(t *testing.T) {
	cfg := config{
		Host:             "ws://example.com/bridge/bot",
		TokenFile:        "tokens.txt",
		Target:           -1,
		BatchSize:        10,
		BatchInterval:    time.Second,
		PingInterval:     time.Second,
		ReportInterval:   time.Second,
		HandshakeTimeout: time.Second,
		ReconnectDelay:   time.Second,
	}
	if _, err := normalizeConfig(cfg, 1); err == nil {
		t.Fatal("expected error for negative target")
	}
}
