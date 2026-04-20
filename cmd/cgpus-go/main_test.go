package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	line := "OK\t2\t8\t25\t262.5\t36.3\t80\t2-6\t5-6\tRL\t8\t0.4\t2"
	rec := parseProbeOutput("vp11", line)
	if rec.State != "OK" {
		t.Fatalf("expected OK state, got %s", rec.State)
	}
	if rec.Idle != 2 || rec.Total != 8 {
		t.Fatalf("unexpected gpu counts: %d/%d", rec.Idle, rec.Total)
	}
	if rec.Yellow != "2-6" || rec.IdleIDs != "5-6" || rec.RawTag != "RL" {
		t.Fatalf("unexpected fields: yellow=%q idle=%q tag=%q", rec.Yellow, rec.IdleIDs, rec.RawTag)
	}
	if rec.CPUPct != "8" || rec.HostMemUsedTB != "0.4" || rec.HostMemTotalTB != "2" {
		t.Fatalf("unexpected cpu/mem fields: cpu=%q used=%q total=%q", rec.CPUPct, rec.HostMemUsedTB, rec.HostMemTotalTB)
	}
}

func TestParseRangeList(t *testing.T) {
	cases := []struct {
		in   string
		want []int
	}{
		{"", nil},
		{"3", []int{3}},
		{"0-1,3,6-7", []int{0, 1, 3, 6, 7}},
		{"2,2", []int{2, 2}},
	}
	for _, tc := range cases {
		got := parseRangeList(tc.in)
		if len(got) != len(tc.want) {
			t.Fatalf("parseRangeList(%q) = %v, want %v", tc.in, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("parseRangeList(%q)[%d] = %d, want %d", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}

func TestUpdateIdleSinceAndGreenGPUs(t *testing.T) {
	state := &runtimeState{IdleSince: map[string]map[int]time.Time{}}
	t0 := time.Unix(1_700_000_000, 0)
	updateIdleSince(state, "h", []int{0, 1, 3}, t0)

	// Before threshold elapses, nothing is green.
	if g := greenGPUs(state, "h", t0.Add(5*time.Minute), idleGreenDuration); len(g) != 0 {
		t.Fatalf("expected empty green set, got %v", g)
	}

	// After threshold, all three are green.
	g := greenGPUs(state, "h", t0.Add(idleGreenDuration), idleGreenDuration)
	for _, id := range []int{0, 1, 3} {
		if !g[id] {
			t.Fatalf("expected gpu %d to be green", id)
		}
	}

	// GPU 1 becomes active later; its timer must reset to "not tracked" so a
	// later idle spell has to accumulate the full threshold again.
	t1 := t0.Add(idleGreenDuration + time.Minute)
	updateIdleSince(state, "h", []int{0, 3}, t1)
	if _, stillTracked := state.IdleSince["h"][1]; stillTracked {
		t.Fatalf("expected gpu 1 to be dropped from idle_since after going active")
	}
	// GPU 0 and 3 kept their original timestamp.
	if state.IdleSince["h"][0] != t0 {
		t.Fatalf("expected gpu 0 to retain original since=t0, got %v", state.IdleSince["h"][0])
	}
}

func TestRenderYellowSection(t *testing.T) {
	// No green → unchanged.
	if got := renderYellowSection("0-1,3", map[int]bool{}, colorRed); got != "(0-1,3)" {
		t.Fatalf("unexpected: %q", got)
	}

	// Single green id inside a range expands the range, green wraps only the id,
	// and the surrounding row color is re-entered after green (not a reset).
	got := renderYellowSection("0-1,3,6-7", map[int]bool{3: true}, colorRed)
	wantContains := colorGreen + "3" + colorRed
	if !strings.Contains(got, wantContains) {
		t.Fatalf("expected %q embedded in %q", wantContains, got)
	}
	if !strings.Contains(got, "0-1") || !strings.Contains(got, "6-7") {
		t.Fatalf("expected untouched ranges to remain compressed: %q", got)
	}

	// Entire range green → range stays compressed, wrapped once.
	got = renderYellowSection("6-7", map[int]bool{6: true, 7: true}, colorRed)
	if got != "("+colorGreen+"6-7"+colorRed+")" {
		t.Fatalf("expected compressed green range, got %q", got)
	}

	// No surrounding color → green wrappers close with colorReset.
	got = renderYellowSection("3", map[int]bool{3: true}, "")
	if got != "("+colorGreen+"3"+colorReset+")" {
		t.Fatalf("expected green+reset with no outer color, got %q", got)
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
		"host-c": "†",
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
	if loaded["host-c"] != "†" {
		t.Fatalf("expected host-c †, got %q", loaded["host-c"])
	}
	if _, ok := loaded["host-b"]; ok {
		t.Fatalf("expected host-b to be omitted for empty tag")
	}
}
