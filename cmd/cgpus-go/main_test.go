package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{name: "group only", args: []string{"cluster"}},
		{name: "cpu and refresh", args: []string{"--cpu", "-f", "5", "cluster"}},
		{name: "refresh default interval", args: []string{"-f", "cluster"}},
		{name: "missing group", args: []string{"--cpu"}, wantErr: true},
		{name: "unknown flag", args: []string{"--wat", "cluster"}, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseArgs(tc.args)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestParseProbeOutputOK(t *testing.T) {
	line := "OK\t2\t8\t25\t262.5\t36.3\t80\t2-6\tRL\t8\t0.4\t2"
	rec := parseProbeOutput("vp11", line)
	if rec.State != "OK" {
		t.Fatalf("expected OK state, got %s", rec.State)
	}
	if rec.Idle != 2 || rec.Total != 8 {
		t.Fatalf("unexpected gpu counts: %d/%d", rec.Idle, rec.Total)
	}
	if rec.Yellow != "2-6" || rec.RawTag != "RL" {
		t.Fatalf("unexpected fields: yellow=%q tag=%q", rec.Yellow, rec.RawTag)
	}
}

func TestApplyTagSemantics(t *testing.T) {
	state := &runtimeState{
		PowerHistory: map[string][]historyPoint{},
		LastTag:      map[string]string{},
	}
	counts := map[string]int{}

	tag := applyTagSemantics("host1", "RL", state, counts)
	if tag != "RL" || counts["RL"] != 1 {
		t.Fatalf("expected RL tag and count update")
	}

	tag = applyTagSemantics("host1", "", state, counts)
	if tag != "RL" {
		t.Fatalf("expected persisted RL tag, got %q", tag)
	}

	tag = applyTagSemantics("host1", "MIXED", state, counts)
	if tag != "" {
		t.Fatalf("expected cleared tag for MIXED")
	}
	if state.LastTag["host1"] != "" {
		t.Fatalf("expected last tag cleared")
	}
}

func TestGenerateSparkline(t *testing.T) {
	history := []historyPoint{
		{Power: 100, Color: "none"},
		{Power: 300, Color: "yellow"},
		{Power: 50, Color: "red"},
		{Missing: true},
	}
	spark := generateSparkline(history)
	if spark == "" {
		t.Fatalf("expected non-empty sparkline")
	}
	if !strings.Contains(spark, "\033[33m") || !strings.Contains(spark, "\033[31m") {
		t.Fatalf("expected colorized sparkline segments")
	}
}

func TestLastTagCacheRoundTrip(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)

	tags := map[string]string{
		"host-a": "RL",
		"host-b": "",
		"host-c": "KK",
	}
	if err := saveLastTagCache(tags); err != nil {
		t.Fatalf("saveLastTagCache failed: %v", err)
	}

	cacheFile := filepath.Join(cacheRoot, "cgpus", "last_tags.tsv")
	if _, err := os.Stat(cacheFile); err != nil {
		t.Fatalf("expected cache file to exist: %v", err)
	}

	loaded, err := loadLastTagCache()
	if err != nil {
		t.Fatalf("loadLastTagCache failed: %v", err)
	}
	if loaded["host-a"] != "RL" {
		t.Fatalf("expected host-a RL, got %q", loaded["host-a"])
	}
	if loaded["host-c"] != "KK" {
		t.Fatalf("expected host-c KK, got %q", loaded["host-c"])
	}
	if _, ok := loaded["host-b"]; ok {
		t.Fatalf("expected host-b to be omitted for empty tag")
	}
}
