package main

import (
	"context"
	"os"
	"os/exec"
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
		{name: "capture", args: []string{"--capture", "cluster"}},
		{name: "capture with cpu", args: []string{"--capture", "--cpu", "cluster"}},
		{name: "multiple groups", args: []string{"--cpu", "cluster-a", "cluster-b"}},
		{name: "capture and refresh conflict", args: []string{"--capture", "-f", "cluster"}, wantErr: true},
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

func TestParseArgsMultipleGroups(t *testing.T) {
	opts, err := parseArgs([]string{"--cpu", "-f", "5", "cluster-a", "cluster-b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !opts.CPU || !opts.Refresh || opts.Interval != 5 {
		t.Fatalf("unexpected options: %+v", opts)
	}
	if got := strings.Join(opts.Groups, ","); got != "cluster-a,cluster-b" {
		t.Fatalf("unexpected groups: %q", got)
	}
}

func TestExpandGroupsDedupesInOrder(t *testing.T) {
	groups := map[string][]string{
		"a": {"h1", "h2", "h3"},
		"b": {"h2", "h4"},
		"c": {"h1", "h5"},
	}

	nodes, err := expandGroups(groups, []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := strings.Join(nodes, ","); got != "h1,h2,h3,h4,h5" {
		t.Fatalf("unexpected nodes: %q", got)
	}
}

func TestExpandGroupsMissingGroup(t *testing.T) {
	_, err := expandGroups(map[string][]string{"a": {"h1"}}, []string{"a", "missing"})
	if err == nil {
		t.Fatalf("expected missing group error")
	}
	if !strings.Contains(err.Error(), "Group 'missing' is not defined.") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProbeTimeoutFromEnv(t *testing.T) {
	t.Setenv("CGPUS_PROBE_TIMEOUT", "250ms")
	if got := probeTimeout(); got != 250*time.Millisecond {
		t.Fatalf("expected 250ms timeout, got %v", got)
	}

	t.Setenv("CGPUS_PROBE_TIMEOUT", "2")
	if got := probeTimeout(); got != 2*time.Second {
		t.Fatalf("expected 2s timeout, got %v", got)
	}

	t.Setenv("CGPUS_PROBE_TIMEOUT", "0")
	if got := probeTimeout(); got != defaultProbeTimeout {
		t.Fatalf("expected default timeout for zero value, got %v", got)
	}

	t.Setenv("CGPUS_PROBE_TIMEOUT", "not-a-duration")
	if got := probeTimeout(); got != defaultProbeTimeout {
		t.Fatalf("expected default timeout for invalid value, got %v", got)
	}
}

func TestProbeHostTimesOut(t *testing.T) {
	t.Setenv("CGPUS_PROBE_TIMEOUT", "100ms")

	oldCommandContext := commandContext
	commandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestHelperSleepCommand", "--")
		cmd.Env = append(os.Environ(), "CGPUS_HELPER_SLEEP=1")
		return cmd
	}
	defer func() {
		commandContext = oldCommandContext
	}()

	start := time.Now()
	rec := probeHost("vp55", true, false)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("probeHost took too long after timeout: %v", elapsed)
	}

	if rec.Host != "vp55" || rec.State != "ERR" || rec.Reason != "ssh_timeout" {
		t.Fatalf("expected vp55 ssh_timeout, got %+v", rec)
	}
}

func TestProbeHostDoesNotWaitForDetachedStdoutHolder(t *testing.T) {
	t.Setenv("CGPUS_PROBE_TIMEOUT", "2s")

	oldCommandContext := commandContext
	commandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestHelperHoldStdoutCommand", "--")
		cmd.Env = append(os.Environ(), "CGPUS_HELPER_HOLD_STDOUT=1")
		return cmd
	}
	defer func() {
		commandContext = oldCommandContext
	}()

	start := time.Now()
	rec := probeHost("mux-host", true, true)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("probeHost waited for detached stdout holder: %v", elapsed)
	}

	if rec.Host != "mux-host" || rec.State != "OK" || rec.Idle != 1 || rec.Total != 2 || rec.RawTag != "RL" {
		t.Fatalf("unexpected probe result: %+v", rec)
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

func TestIdleSinceCacheRoundTrip(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)

	t0 := time.Unix(1_700_000_000, 0)
	in := map[string]map[int]time.Time{
		"host-a": {0: t0, 3: t0.Add(5 * time.Minute)},
		"host-b": {7: t0.Add(time.Hour)},
	}
	if err := saveIdleSinceCache(in); err != nil {
		t.Fatalf("saveIdleSinceCache failed: %v", err)
	}

	cacheFile := filepath.Join(cacheRoot, "cgpus", "idle_since.tsv")
	if _, err := os.Stat(cacheFile); err != nil {
		t.Fatalf("expected cache file to exist: %v", err)
	}

	loaded, err := loadIdleSinceCache()
	if err != nil {
		t.Fatalf("loadIdleSinceCache failed: %v", err)
	}
	if loaded["host-a"][0] != t0 {
		t.Fatalf("expected host-a[0]=t0, got %v", loaded["host-a"][0])
	}
	if loaded["host-a"][3] != t0.Add(5*time.Minute) {
		t.Fatalf("expected host-a[3]=t0+5m, got %v", loaded["host-a"][3])
	}
	if loaded["host-b"][7] != t0.Add(time.Hour) {
		t.Fatalf("expected host-b[7]=t0+1h, got %v", loaded["host-b"][7])
	}
}

func TestRenderPlain(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	state := &runtimeState{
		PowerHistory: map[string][]historyPoint{},
		LastTag:      map[string]string{},
		IdleSince: map[string]map[int]time.Time{
			"vp11": {5: t0, 6: t0.Add(-30 * time.Minute)},
		},
	}
	records := []hostRecord{
		{Host: "vp11", State: "OK", Idle: 2, Total: 8, Power: 262, Yellow: "2-6", IdleIDs: "5-6", RawTag: "RL"},
		{Host: "vp12", State: "ERR", Reason: "ssh_fail"},
	}
	now := t0.Add(45 * time.Minute)

	out := renderPlain(options{}, records, state, now)
	if !strings.Contains(out, "vp11:") || !strings.Contains(out, "OK") {
		t.Fatalf("expected vp11 OK line, got:\n%s", out)
	}
	if !strings.Contains(out, "idle=2/8") || !strings.Contains(out, "ids=5-6") {
		t.Fatalf("expected idle fields, got:\n%s", out)
	}
	// min idle duration is 45m (gpu 5 stamped at t0, seen again at now).
	if !strings.Contains(out, "for=45m") {
		t.Fatalf("expected for=45m (min of 45m,75m), got:\n%s", out)
	}
	if !strings.Contains(out, "tag=RL") || !strings.Contains(out, "spare=2-6") {
		t.Fatalf("expected tag/spare fields, got:\n%s", out)
	}
	if !strings.Contains(out, "vp12:") || !strings.Contains(out, "ERR") || !strings.Contains(out, "ssh_fail") {
		t.Fatalf("expected vp12 ERR line, got:\n%s", out)
	}
	// No ANSI escapes in capture output.
	if strings.Contains(out, "\033[") {
		t.Fatalf("expected no ANSI escapes in plain output, got:\n%s", out)
	}
}

func TestFormatIdleDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{59 * time.Minute, "59m"},
		{time.Hour, "1h"},
		{90 * time.Minute, "1h30m"},
		{-time.Second, "0s"},
	}
	for _, tc := range cases {
		if got := formatIdleDuration(tc.d); got != tc.want {
			t.Fatalf("formatIdleDuration(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestLastTagCacheRoundTrip(t *testing.T) {
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)

	tags := map[string]string{
		"host-a": "RL",
		"host-b": "",
		"host-c": "†",
		"host-d": "‡",
		"host-e": "BI",
		"host-f": "n",
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
	if loaded["host-d"] != "‡" {
		t.Fatalf("expected host-d ‡, got %q", loaded["host-d"])
	}
	if loaded["host-e"] != "BI" {
		t.Fatalf("expected host-e BI, got %q", loaded["host-e"])
	}
	if loaded["host-f"] != "n" {
		t.Fatalf("expected host-f n, got %q", loaded["host-f"])
	}
	if _, ok := loaded["host-b"]; ok {
		t.Fatalf("expected host-b to be omitted for empty tag")
	}
}

func TestHelperSleepCommand(t *testing.T) {
	if os.Getenv("CGPUS_HELPER_SLEEP") != "1" {
		return
	}
	time.Sleep(10 * time.Second)
	os.Exit(0)
}

func TestHelperHoldStdoutCommand(t *testing.T) {
	if os.Getenv("CGPUS_HELPER_HOLD_STDOUT") != "1" {
		return
	}
	_, _ = os.Stdout.WriteString("OK\t1\t2\t3\t4\t5\t6\t0\t0\tRL\t\t\t\n")

	cmd := exec.Command("sleep", "5")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
