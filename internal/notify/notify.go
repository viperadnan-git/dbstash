// Package notify sends backup status notifications to Slack and Discord
// webhooks.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/viperadnan-git/dbstash/internal/logger"
)

// Result contains the details of a backup run for notification purposes.
type Result struct {
	Status     string        // "success" or "failure"
	Engine     string        // engine key (e.g. "pg")
	Database   string        // database name
	RemotePath string        // remote file/dir path
	FileSize   int64         // file size in bytes (0 if unknown)
	Duration   time.Duration // backup duration
	Error      string        // error message (empty on success)
}

// Send dispatches a notification based on the configured webhook URL and
// NOTIFY_ON policy. It never returns an error to avoid failing the backup
// due to notification issues.
func Send(ctx context.Context, webhookURL, notifyOn string, result Result) {
	if webhookURL == "" {
		return
	}

	if !shouldNotify(notifyOn, result.Status) {
		logger.Log.Debug().Str("notify_on", notifyOn).Str("status", result.Status).Msg("skipping notification per policy")
		return
	}

	var payload []byte
	var err error

	if isDiscord(webhookURL) {
		payload, err = buildDiscordPayload(result)
	} else {
		payload, err = buildSlackPayload(result)
	}

	if err != nil {
		logger.Log.Warn().Err(err).Msg("failed to build notification payload")
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(payload))
	if err != nil {
		logger.Log.Warn().Err(err).Msg("failed to create notification request")
		return
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		logger.Log.Warn().Err(err).Msg("failed to send notification")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		logger.Log.Warn().Int("status_code", resp.StatusCode).Msg("notification webhook returned non-success status")
		return
	}

	logger.Log.Info().Str("platform", platform(webhookURL)).Msg("notification sent successfully")
}

func shouldNotify(notifyOn, status string) bool {
	switch notifyOn {
	case "always":
		return true
	case "failure":
		return status == "failure"
	case "success":
		return status == "success"
	default:
		return status == "failure"
	}
}

func isDiscord(url string) bool {
	return strings.Contains(url, "discord.com/api/webhooks")
}

func platform(url string) string {
	if isDiscord(url) {
		return "discord"
	}
	return "slack"
}

// FormatSize returns a human-readable file size string.
func FormatSize(bytes int64) string {
	if bytes == 0 {
		return "unknown"
	}
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func statusEmoji(status string) string {
	if status == "success" {
		return "\u2705" // check mark
	}
	return "\u274c" // cross mark
}

func statusColor(status string) string {
	if status == "success" {
		return "#36a64f"
	}
	return "#dc3545"
}

func statusColorInt(status string) int {
	if status == "success" {
		return 0x36a64f
	}
	return 0xdc3545
}

// BuildSlackPayload creates a Slack webhook payload. Exported for testing.
func BuildSlackPayload(result Result) ([]byte, error) {
	return buildSlackPayload(result)
}

func buildSlackPayload(result Result) ([]byte, error) {
	fields := []map[string]interface{}{
		{"title": "Status", "value": fmt.Sprintf("%s %s", statusEmoji(result.Status), strings.ToUpper(result.Status)), "short": true},
		{"title": "Engine", "value": result.Engine, "short": true},
		{"title": "Database", "value": result.Database, "short": true},
		{"title": "Duration", "value": result.Duration.Round(time.Second).String(), "short": true},
		{"title": "File Size", "value": FormatSize(result.FileSize), "short": true},
		{"title": "Remote Path", "value": result.RemotePath, "short": false},
	}

	if result.Error != "" {
		fields = append(fields, map[string]interface{}{"title": "Error", "value": result.Error, "short": false})
	}

	payload := map[string]interface{}{
		"text": fmt.Sprintf("dbstash backup %s: %s/%s", result.Status, result.Engine, result.Database),
		"attachments": []map[string]interface{}{
			{
				"color":  statusColor(result.Status),
				"fields": fields,
				"ts":     time.Now().Unix(),
			},
		},
	}

	return json.Marshal(payload)
}

// BuildDiscordPayload creates a Discord webhook payload. Exported for testing.
func BuildDiscordPayload(result Result) ([]byte, error) {
	return buildDiscordPayload(result)
}

func buildDiscordPayload(result Result) ([]byte, error) {
	fields := []map[string]interface{}{
		{"name": "Status", "value": fmt.Sprintf("%s %s", statusEmoji(result.Status), strings.ToUpper(result.Status)), "inline": true},
		{"name": "Engine", "value": result.Engine, "inline": true},
		{"name": "Database", "value": result.Database, "inline": true},
		{"name": "Duration", "value": result.Duration.Round(time.Second).String(), "inline": true},
		{"name": "File Size", "value": FormatSize(result.FileSize), "inline": true},
		{"name": "Remote Path", "value": result.RemotePath, "inline": false},
	}

	if result.Error != "" {
		fields = append(fields, map[string]interface{}{"name": "Error", "value": result.Error, "inline": false})
	}

	payload := map[string]interface{}{
		"embeds": []map[string]interface{}{
			{
				"title":     fmt.Sprintf("dbstash backup %s", result.Status),
				"color":     statusColorInt(result.Status),
				"fields":    fields,
				"timestamp": time.Now().UTC().Format(time.RFC3339),
			},
		},
	}

	return json.Marshal(payload)
}
