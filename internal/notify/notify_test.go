package notify

import (
	"encoding/json"
	"testing"
	"time"
)

func TestShouldNotify(t *testing.T) {
	tests := []struct {
		notifyOn string
		status   string
		expected bool
	}{
		{"always", "success", true},
		{"always", "failure", true},
		{"failure", "failure", true},
		{"failure", "success", false},
		{"success", "success", true},
		{"success", "failure", false},
		{"", "failure", true},  // default to failure
		{"", "success", false}, // default to failure
	}

	for _, tt := range tests {
		t.Run(tt.notifyOn+"_"+tt.status, func(t *testing.T) {
			if got := shouldNotify(tt.notifyOn, tt.status); got != tt.expected {
				t.Errorf("shouldNotify(%q, %q) = %v, want %v", tt.notifyOn, tt.status, got, tt.expected)
			}
		})
	}
}

func TestIsDiscord(t *testing.T) {
	tests := []struct {
		url      string
		expected bool
	}{
		{"https://discord.com/api/webhooks/123/abc", true},
		{"https://hooks.slack.com/services/T/B/X", false},
		{"https://example.com/webhook", false},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			if got := isDiscord(tt.url); got != tt.expected {
				t.Errorf("isDiscord(%q) = %v, want %v", tt.url, got, tt.expected)
			}
		})
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{0, "unknown"},
		{500, "500 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := FormatSize(tt.bytes); got != tt.expected {
				t.Errorf("FormatSize(%d) = %q, want %q", tt.bytes, got, tt.expected)
			}
		})
	}
}

func TestBuildSlackPayload(t *testing.T) {
	result := Result{
		Status:     "success",
		Engine:     "pg",
		Database:   "mydb",
		RemotePath: "s3:bucket/mydb-2026-01-01.sql",
		FileSize:   1048576,
		Duration:   30 * time.Second,
	}

	data, err := BuildSlackPayload(result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if _, ok := payload["text"]; !ok {
		t.Error("slack payload missing 'text' field")
	}
	if _, ok := payload["attachments"]; !ok {
		t.Error("slack payload missing 'attachments' field")
	}

	attachments := payload["attachments"].([]any)
	if len(attachments) != 1 {
		t.Errorf("expected 1 attachment, got %d", len(attachments))
	}

	att := attachments[0].(map[string]any)
	if att["color"] != "#36a64f" {
		t.Errorf("expected success color, got %q", att["color"])
	}
}

func TestBuildSlackPayload_Failure(t *testing.T) {
	result := Result{
		Status:   "failure",
		Engine:   "mongo",
		Database: "analytics",
		Error:    "connection refused",
		Duration: 5 * time.Second,
	}

	data, err := BuildSlackPayload(result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	attachments := payload["attachments"].([]any)
	att := attachments[0].(map[string]any)
	if att["color"] != "#dc3545" {
		t.Errorf("expected failure color, got %q", att["color"])
	}

	// Should have error field
	fields := att["fields"].([]any)
	hasError := false
	for _, f := range fields {
		field := f.(map[string]any)
		if field["title"] == "Error" {
			hasError = true
			if field["value"] != "connection refused" {
				t.Errorf("expected error message, got %q", field["value"])
			}
		}
	}
	if !hasError {
		t.Error("expected error field in failure payload")
	}
}

func TestBuildDiscordPayload(t *testing.T) {
	result := Result{
		Status:     "success",
		Engine:     "pg",
		Database:   "mydb",
		RemotePath: "s3:bucket/mydb-2026-01-01.sql",
		FileSize:   2048,
		Duration:   10 * time.Second,
	}

	data, err := BuildDiscordPayload(result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	embeds, ok := payload["embeds"].([]any)
	if !ok || len(embeds) != 1 {
		t.Fatal("discord payload should have exactly 1 embed")
	}

	embed := embeds[0].(map[string]any)
	if embed["color"] != float64(0x36a64f) {
		t.Errorf("expected success color int, got %v", embed["color"])
	}
	if _, ok := embed["timestamp"]; !ok {
		t.Error("discord embed missing timestamp")
	}
}

func TestBuildDiscordPayload_Failure(t *testing.T) {
	result := Result{
		Status:   "failure",
		Engine:   "redis",
		Database: "default",
		Error:    "timeout",
		Duration: 60 * time.Second,
	}

	data, err := BuildDiscordPayload(result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	embeds := payload["embeds"].([]any)
	embed := embeds[0].(map[string]any)
	if embed["color"] != float64(0xdc3545) {
		t.Errorf("expected failure color int, got %v", embed["color"])
	}

	fields := embed["fields"].([]any)
	hasError := false
	for _, f := range fields {
		field := f.(map[string]any)
		if field["name"] == "Error" {
			hasError = true
		}
	}
	if !hasError {
		t.Error("expected error field in failure discord payload")
	}
}
