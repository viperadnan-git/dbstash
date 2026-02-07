package retention

import (
	"testing"
	"time"
)

func TestSelectDeletions_MaxFiles(t *testing.T) {
	now := time.Now()
	entries := []RemoteEntry{
		{Path: "backup-1.sql", ModTime: now.Add(-4 * time.Hour)},
		{Path: "backup-2.sql", ModTime: now.Add(-3 * time.Hour)},
		{Path: "backup-3.sql", ModTime: now.Add(-2 * time.Hour)},
		{Path: "backup-4.sql", ModTime: now.Add(-1 * time.Hour)},
		{Path: "backup-5.sql", ModTime: now},
	}

	tests := []struct {
		name          string
		maxFiles      int
		maxDays       int
		expectDeleted int
	}{
		{"keep all", 0, 0, 0},
		{"keep 5", 5, 0, 0},
		{"keep 3", 3, 0, 2},
		{"keep 1", 1, 0, 4},
		{"keep 10", 10, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SelectDeletions(entries, tt.maxFiles, tt.maxDays)
			if len(result) != tt.expectDeleted {
				t.Errorf("expected %d deletions, got %d", tt.expectDeleted, len(result))
			}
		})
	}
}

func TestSelectDeletions_MaxDays(t *testing.T) {
	now := time.Now()
	entries := []RemoteEntry{
		{Path: "old-1.sql", ModTime: now.Add(-10 * 24 * time.Hour)},
		{Path: "old-2.sql", ModTime: now.Add(-5 * 24 * time.Hour)},
		{Path: "recent-1.sql", ModTime: now.Add(-2 * 24 * time.Hour)},
		{Path: "recent-2.sql", ModTime: now.Add(-12 * time.Hour)},
		{Path: "today.sql", ModTime: now},
	}

	tests := []struct {
		name          string
		maxFiles      int
		maxDays       int
		expectDeleted int
	}{
		{"no constraint", 0, 0, 0},
		{"7 days", 0, 7, 1},
		{"3 days", 0, 3, 2},
		{"1 day", 0, 1, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SelectDeletions(entries, tt.maxFiles, tt.maxDays)
			if len(result) != tt.expectDeleted {
				t.Errorf("expected %d deletions, got %d", tt.expectDeleted, len(result))
			}
		})
	}
}

func TestSelectDeletions_BothConstraints(t *testing.T) {
	now := time.Now()
	entries := []RemoteEntry{
		{Path: "old-1.sql", ModTime: now.Add(-10 * 24 * time.Hour)},
		{Path: "old-2.sql", ModTime: now.Add(-8 * 24 * time.Hour)},
		{Path: "mid-1.sql", ModTime: now.Add(-3 * 24 * time.Hour)},
		{Path: "recent-1.sql", ModTime: now.Add(-1 * 24 * time.Hour)},
		{Path: "today.sql", ModTime: now},
	}

	// maxFiles=3 would delete old-1, old-2 (2 oldest)
	// maxDays=7 would delete old-1, old-2 (older than 7 days)
	// Union: old-1, old-2
	result := SelectDeletions(entries, 3, 7)
	if len(result) != 2 {
		t.Errorf("expected 2 deletions, got %d", len(result))
	}

	// maxFiles=4 would delete old-1 (1 oldest)
	// maxDays=5 would delete old-1, old-2 (older than 5 days)
	// Union: old-1, old-2
	result = SelectDeletions(entries, 4, 5)
	if len(result) != 2 {
		t.Errorf("expected 2 deletions, got %d", len(result))
	}
}

func TestSelectDeletions_Empty(t *testing.T) {
	result := SelectDeletions(nil, 5, 7)
	if len(result) != 0 {
		t.Errorf("expected 0 deletions for empty input, got %d", len(result))
	}
}

func TestSelectDeletions_SortOrder(t *testing.T) {
	now := time.Now()
	// Intentionally unsorted input
	entries := []RemoteEntry{
		{Path: "c.sql", ModTime: now.Add(-1 * time.Hour)},
		{Path: "a.sql", ModTime: now.Add(-3 * time.Hour)},
		{Path: "b.sql", ModTime: now.Add(-2 * time.Hour)},
	}

	// Keep 1 → should delete the 2 oldest (a.sql, b.sql)
	result := SelectDeletions(entries, 1, 0)
	if len(result) != 2 {
		t.Fatalf("expected 2 deletions, got %d", len(result))
	}

	paths := map[string]bool{}
	for _, r := range result {
		paths[r.Path] = true
	}
	if !paths["a.sql"] || !paths["b.sql"] {
		t.Errorf("expected a.sql and b.sql to be deleted, got %v", result)
	}
}
