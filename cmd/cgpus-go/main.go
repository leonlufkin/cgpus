package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	defaultRefreshInterval = 30
	idlePowerW             = 100
	idleMemMB              = 1024
	idleUtilPct            = 5
	yellowSpareMemGB       = 40
	yellowPowerW           = 250
	idleGreenDuration      = 30 * time.Minute
	defaultProbeTimeout    = 10 * time.Second
)

const (
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
	colorGreen  = "\033[32m"
	colorReset  = "\033[0m"
)

var commandContext = exec.CommandContext

var remoteProbeScript = `set -o pipefail

if ! command -v nvidia-smi >/dev/null 2>&1; then
  printf 'ERR\tnvidia_missing\n'
  exit 0
fi

gpu_stats="$(nvidia-smi --query-gpu=index,memory.used,memory.total,utilization.gpu,power.draw --format=csv,noheader,nounits 2>/dev/null | awk -F',' -v idle_power="${IDLE_POWER_W:-100}" -v idle_mem="${IDLE_MEM_MB:-1024}" -v idle_util="${IDLE_UTIL_PCT:-5}" -v yellow_spare="${YELLOW_SPARE_MEM_GB:-40}" -v yellow_power="${YELLOW_POWER_W:-250}" '
BEGIN {
  idle_count=0
  total_util=0
  total_power=0
  total_mem_used=0
  total_mem_total=0
  gpu_count=0
}
NF >= 5 {
  gpu_count++
  gpu_index=int($1)
  mem_used=$2+0
  mem_total=$3+0
  util=$4+0
  power=$5+0

  total_util += util
  total_power += power
  total_mem_used += mem_used
  total_mem_total += mem_total

  if (power < idle_power && mem_used < idle_mem && util < idle_util) {
    idle_count++
    idle[gpu_index] = 1
  }

  spare_mem_gpu = (mem_total - mem_used) / 1024
  if (spare_mem_gpu > yellow_spare || power < yellow_power) {
    yellow[gpu_index] = 1
  }
}
function compress_ranges(set, n,    out, rs, re, i) {
  out = ""
  rs = -1
  re = -1
  for (i = 0; i < n; i++) {
    if (set[i] == 1) {
      if (rs == -1) { rs = i; re = i }
      else if (i == re + 1) { re = i }
      else {
        if (out != "") out = out ","
        if (rs == re) out = out rs
        else out = out rs "-" re
        rs = i; re = i
      }
    }
  }
  if (rs != -1) {
    if (out != "") out = out ","
    if (rs == re) out = out rs
    else out = out rs "-" re
  }
  return out
}
END {
  if (gpu_count == 0) {
    exit 1
  }

  avg_util = total_util / gpu_count
  avg_power = total_power / gpu_count
  mem_used_gb = (total_mem_used / 1024) / gpu_count
  mem_total_gb = (total_mem_total / 1024) / gpu_count

  yellow_str = compress_ranges(yellow, gpu_count)
  idle_str = compress_ranges(idle, gpu_count)

  printf "%d\t%d\t%.0f\t%.1f\t%.1f\t%.0f\t%s\t%s", idle_count, gpu_count, avg_util, avg_power, mem_used_gb, mem_total_gb, yellow_str, idle_str
}')"

if [[ -z "$gpu_stats" ]]; then
  printf 'ERR\tparse_error\n'
  exit 0
fi

process_tag=""
process_data="$(nvidia-smi --query-compute-apps=pid,process_name --format=csv,noheader,nounits 2>/dev/null || true)"

if [[ -n "$process_data" ]]; then
  check_leon=1
  check_ray=1
  check_cmoe=1
  check_au=1
  check_da=1
  check_ar=1
  check_kk=1
  num_processes=0

  while IFS=',' read -r pid proc_name; do
    pid="${pid//[[:space:]]/}"
    [[ -z "$pid" ]] && continue

    num_processes=$((num_processes + 1))

    ps_line="$(ps -o user=,args= -p "$pid" 2>/dev/null)"
    owner="${ps_line%% *}"
    cmd_line="${ps_line#* }"
    if [[ -z "$ps_line" ]]; then
      owner=""
      cmd_line=""
    fi

    [[ "$owner" != "leon" ]] && check_leon=0
    [[ "$proc_name" != *"ray::WorkerDict"* && "$owner" != "pritish" ]] && check_ray=0
    [[ "$cmd_line" != *"cmoe"* ]] && check_cmoe=0
    [[ "$cmd_line" != *"lisan.al_gaib"* && "$cmd_line" != *"zonos"* ]] && check_au=0
    [[ "$owner" != "xiao" && "$cmd_line" != *"dataInfra"* ]] && check_da=0
    [[ "$cmd_line" != *"pretrain_gpt"* ]] && check_ar=0
    [[ "$owner" != "kamesh" ]] && check_kk=0
  done <<< "$process_data"

  if [[ $num_processes -gt 0 ]]; then
    tag_count=$((check_leon + check_ray + check_cmoe + check_au + check_da + check_ar + check_kk))
    if [[ $tag_count -eq 1 ]]; then
      [[ $check_leon -eq 1 ]] && process_tag="*"
      [[ $check_ray -eq 1 ]] && process_tag="RL"
      [[ $check_cmoe -eq 1 ]] && process_tag="CM"
      [[ $check_au -eq 1 ]] && process_tag="AU"
      [[ $check_da -eq 1 ]] && process_tag="DA"
      [[ $check_ar -eq 1 ]] && process_tag="AR"
      [[ $check_kk -eq 1 ]] && process_tag="†"
    elif [[ $tag_count -gt 1 ]]; then
      process_tag="MIXED"
    else
      process_tag="NONE"
    fi
  fi
fi

cpu_pct=""
mem_used_tb=""
mem_total_tb=""
if [[ "${ENABLE_CPU:-0}" -eq 1 ]]; then
  read -r u1 n1 s1 i1 w1 irq1 sirq1 <<<"$(awk '/^cpu / {print $2, $3, $4, $5, $6, $7, $8}' /proc/stat 2>/dev/null)"
  sleep 0.1
  read -r u2 n2 s2 i2 w2 irq2 sirq2 <<<"$(awk '/^cpu / {print $2, $3, $4, $5, $6, $7, $8}' /proc/stat 2>/dev/null)"

  idle1=$((i1 + w1))
  idle2=$((i2 + w2))
  non1=$((u1 + n1 + s1 + irq1 + sirq1))
  non2=$((u2 + n2 + s2 + irq2 + sirq2))
  total1=$((idle1 + non1))
  total2=$((idle2 + non2))
  delta_total=$((total2 - total1))
  delta_idle=$((idle2 - idle1))

  if [[ $delta_total -gt 0 ]]; then
    cpu_pct=$(((100 * (delta_total - delta_idle) + delta_total / 2) / delta_total))
  else
    cpu_pct=0
  fi

  mem_total_kb="$(awk '/^MemTotal:/ {print $2}' /proc/meminfo 2>/dev/null)"
  mem_avail_kb="$(awk '/^MemAvailable:/ {print $2}' /proc/meminfo 2>/dev/null)"
  if [[ -z "$mem_avail_kb" ]]; then
    mem_avail_kb="$(awk '/^MemFree:/ {print $2}' /proc/meminfo 2>/dev/null)"
  fi

  if [[ -z "$mem_total_kb" || -z "$mem_avail_kb" ]]; then
    mem_total_kb=0
    mem_avail_kb=0
  fi

  mem_used_kb=$((mem_total_kb - mem_avail_kb))

  # /proc/meminfo is in KiB. Convert KiB -> TiB by dividing by 1024^3.
  mem_total_tb="$(awk -v kb="$mem_total_kb" 'BEGIN {tb=kb/1073741824; if (tb < 1) printf "%.1f", tb; else printf "%.0f", tb}')"
  mem_used_tb="$(awk -v kb="$mem_used_kb" 'BEGIN {tb=kb/1073741824; if (tb < 1) printf "%.1f", tb; else printf "%.0f", tb}')"
fi

printf 'OK\t%s\t%s\t%s\t%s\t%s\n' "$gpu_stats" "$process_tag" "$cpu_pct" "$mem_used_tb" "$mem_total_tb"
`

type options struct {
	CPU      bool
	Refresh  bool
	Capture  bool
	Interval int
	Group    string
}

type historyPoint struct {
	Missing bool
	Power   float64
	Color   string
}

type runtimeState struct {
	PowerHistory map[string][]historyPoint
	LastTag      map[string]string
	// IdleSince[host][gpuIdx] = first time that GPU was observed idle in the
	// current run. Cleared when the GPU stops being idle or the host errors.
	IdleSince map[string]map[int]time.Time
}

type hostRecord struct {
	Host           string
	State          string
	Reason         string
	Idle           int
	Total          int
	Util           float64
	Power          float64
	MemUsed        float64
	MemTotal       float64
	Yellow         string
	IdleIDs        string
	RawTag         string
	CPUPct         string
	HostMemUsedTB  string
	HostMemTotalTB string
}

func printUsage() {
	fmt.Println("Usage: cgpus [--cpu] [-f [INTERVAL] | --capture] GROUP")
	fmt.Println("  --cpu         Show CPU load and host memory usage")
	fmt.Println("  -f            Enable refresh mode (default: 30s interval)")
	fmt.Println("  -f INTERVAL   Enable refresh mode with custom interval (seconds)")
	fmt.Println("  --capture     Print a single plain-text snapshot and exit")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  cgpus zyphra")
	fmt.Println("  cgpus --cpu zyphra")
	fmt.Println("  cgpus -f zyphra")
	fmt.Println("  cgpus -f 5 zyphra")
	fmt.Println("  cgpus --cpu -f 5 zyphra")
	fmt.Println("  cgpus --capture zyphra")
}

func parseArgs(args []string) (options, error) {
	opts := options{Interval: defaultRefreshInterval}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--cpu":
			opts.CPU = true
		case "--capture":
			opts.Capture = true
		case "-f":
			opts.Refresh = true
			if i+1 < len(args) {
				nextArg := args[i+1]
				if _, err := strconv.Atoi(nextArg); err == nil {
					opts.Interval, _ = strconv.Atoi(nextArg)
					i++
				}
			}
		default:
			if strings.HasPrefix(arg, "-") {
				return opts, fmt.Errorf("unknown option: %s", arg)
			}
			if opts.Group != "" {
				return opts, fmt.Errorf("unexpected argument: %s", arg)
			}
			opts.Group = arg
		}
	}

	if opts.Group == "" {
		return opts, errors.New("missing group")
	}
	if opts.Interval < 1 {
		return opts, errors.New("refresh interval must be at least 1 second")
	}
	if opts.Capture && opts.Refresh {
		return opts, errors.New("--capture cannot be combined with -f")
	}

	return opts, nil
}

func loadGroups() (map[string][]string, error) {
	// zsh emits pairs via ${(kv)GROUPS}; bash 4+ via "${!GROUPS[@]}".
	// Try zsh first (matches the original syntax users wrote their groups file
	// in) and fall back to bash so the tool works on clusters without zsh.
	attempts := []struct {
		shell, script string
	}{
		{"zsh", `source "$HOME/.ssh/ssh_key_groups.sh" >/dev/null 2>&1 || exit 1; for k v in ${(kv)GROUPS}; do print -r -- "$k"$'\t'"$v"; done`},
		{"bash", `source "$HOME/.ssh/ssh_key_groups.sh" >/dev/null 2>&1 || exit 1; for k in "${!GROUPS[@]}"; do printf '%s\t%s\n' "$k" "${GROUPS[$k]}"; done`},
	}
	var out []byte
	var lastErr error
	for _, a := range attempts {
		if _, err := exec.LookPath(a.shell); err != nil {
			continue
		}
		cmd := exec.Command(a.shell, "-lc", a.script)
		o, err := cmd.Output()
		if err == nil && len(strings.TrimSpace(string(o))) > 0 {
			out = o
			lastErr = nil
			break
		}
		lastErr = err
	}
	if len(out) == 0 {
		if lastErr != nil {
			return nil, fmt.Errorf("failed to load groups from ~/.ssh/ssh_key_groups.sh: %v", lastErr)
		}
		return nil, fmt.Errorf("failed to load groups from ~/.ssh/ssh_key_groups.sh")
	}

	groups := make(map[string][]string)
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		if key == "" {
			continue
		}
		hosts := strings.Fields(parts[1])
		groups[key] = hosts
	}

	if len(groups) == 0 {
		return nil, fmt.Errorf("no groups found in ~/.ssh/ssh_key_groups.sh")
	}

	return groups, nil
}

func lastTagCacheFile() string {
	if cacheHome := os.Getenv("XDG_CACHE_HOME"); cacheHome != "" {
		return filepath.Join(cacheHome, "cgpus", "last_tags.tsv")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "last_tags.tsv"
	}
	return filepath.Join(home, ".cache", "cgpus", "last_tags.tsv")
}

func loadLastTagCache() (map[string]string, error) {
	cacheFile := lastTagCacheFile()
	f, err := os.Open(cacheFile)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	defer f.Close()

	tags := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		host := strings.TrimSpace(parts[0])
		if host == "" {
			continue
		}
		tag := strings.TrimSpace(parts[1])
		if !isValidCachedTag(tag) {
			continue
		}
		tags[host] = tag
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return tags, nil
}

func saveLastTagCache(tags map[string]string) error {
	cacheFile := lastTagCacheFile()
	cacheDir := filepath.Dir(cacheFile)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(cacheDir, "last_tags.*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)

	keys := make([]string, 0, len(tags))
	for host, tag := range tags {
		tag = strings.TrimSpace(tag)
		if !isValidCachedTag(tag) {
			continue
		}
		keys = append(keys, host)
	}
	sort.Strings(keys)

	w := bufio.NewWriter(tmpFile)
	for _, host := range keys {
		if _, err := fmt.Fprintf(w, "%s\t%s\n", host, tags[host]); err != nil {
			_ = tmpFile.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}

	return os.Rename(tmpName, cacheFile)
}

func isValidCachedTag(tag string) bool {
	switch tag {
	case "*", "†", "RL", "CM", "AU", "DA", "AR":
		return true
	default:
		return false
	}
}

func idleSinceCacheFile() string {
	if cacheHome := os.Getenv("XDG_CACHE_HOME"); cacheHome != "" {
		return filepath.Join(cacheHome, "cgpus", "idle_since.tsv")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "idle_since.tsv"
	}
	return filepath.Join(home, ".cache", "cgpus", "idle_since.tsv")
}

func loadIdleSinceCache() (map[string]map[int]time.Time, error) {
	f, err := os.Open(idleSinceCacheFile())
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]map[int]time.Time{}, nil
		}
		return nil, err
	}
	defer f.Close()

	result := map[string]map[int]time.Time{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		parts := strings.Split(strings.TrimSpace(scanner.Text()), "\t")
		if len(parts) != 3 {
			continue
		}
		host := strings.TrimSpace(parts[0])
		gpu, err1 := strconv.Atoi(parts[1])
		unix, err2 := strconv.ParseInt(parts[2], 10, 64)
		if host == "" || err1 != nil || err2 != nil {
			continue
		}
		if result[host] == nil {
			result[host] = map[int]time.Time{}
		}
		result[host][gpu] = time.Unix(unix, 0)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func saveIdleSinceCache(since map[string]map[int]time.Time) error {
	cacheFile := idleSinceCacheFile()
	cacheDir := filepath.Dir(cacheFile)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(cacheDir, "idle_since.*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)

	hosts := make([]string, 0, len(since))
	for host := range since {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)

	w := bufio.NewWriter(tmpFile)
	for _, host := range hosts {
		gpus := make([]int, 0, len(since[host]))
		for gpu := range since[host] {
			gpus = append(gpus, gpu)
		}
		sort.Ints(gpus)
		for _, gpu := range gpus {
			if _, err := fmt.Fprintf(w, "%s\t%d\t%d\n", host, gpu, since[host][gpu].Unix()); err != nil {
				_ = tmpFile.Close()
				return err
			}
		}
	}
	if err := w.Flush(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, cacheFile)
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func probeTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("CGPUS_PROBE_TIMEOUT"))
	if raw == "" {
		return defaultProbeTimeout
	}

	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}
	if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}

	return defaultProbeTimeout
}

func buildSSHArgs(refresh bool) []string {
	args := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=5"}
	if refresh {
		home, err := os.UserHomeDir()
		if err == nil {
			controlPath := filepath.Join(home, ".ssh", "cgpus-%C")
			args = append(args,
				"-o", "ControlMaster=auto",
				"-o", "ControlPersist=600",
				"-o", fmt.Sprintf("ControlPath=%s", controlPath),
			)
		}
	}
	return args
}

func probeHost(host string, enableCPU bool, refresh bool) hostRecord {
	sshArgs := buildSSHArgs(refresh)
	remoteCmd := fmt.Sprintf(
		"ENABLE_CPU=%d IDLE_POWER_W=%d IDLE_MEM_MB=%d IDLE_UTIL_PCT=%d YELLOW_SPARE_MEM_GB=%d YELLOW_POWER_W=%d bash -s",
		boolToInt(enableCPU),
		idlePowerW,
		idleMemMB,
		idleUtilPct,
		yellowSpareMemGB,
		yellowPowerW,
	)
	sshArgs = append(sshArgs, host, remoteCmd)

	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout())
	defer cancel()

	cmd := commandContext(ctx, "ssh", sshArgs...)
	cmd.Stdin = strings.NewReader(remoteProbeScript)
	output, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return hostRecord{Host: host, State: "ERR", Reason: "ssh_timeout"}
	}
	if err != nil {
		return hostRecord{Host: host, State: "ERR", Reason: "ssh_fail"}
	}

	line := strings.TrimSpace(string(output))
	if line == "" {
		return hostRecord{Host: host, State: "ERR", Reason: "empty"}
	}

	rec := parseProbeOutput(host, line)
	if rec.State == "" {
		rec.State = "ERR"
		rec.Reason = "parse_error"
	}
	return rec
}

func parseProbeOutput(host string, line string) hostRecord {
	parts := strings.Split(line, "\t")
	if len(parts) < 2 {
		return hostRecord{Host: host, State: "ERR", Reason: "parse_error"}
	}

	if parts[0] == "ERR" {
		reason := "unknown"
		if len(parts) > 1 && parts[1] != "" {
			reason = parts[1]
		}
		return hostRecord{Host: host, State: "ERR", Reason: reason}
	}

	if parts[0] != "OK" {
		return hostRecord{Host: host, State: "ERR", Reason: "parse_error"}
	}

	// Layout (tab-separated): OK, idle_count, total, util, power, mem_used,
	// mem_total, yellow_str, idle_str, raw_tag, cpu_pct, host_mem_used,
	// host_mem_total. idle_str is newer; older probes that omit it are still
	// accepted (IdleIDs is then empty and no GPU ever goes green).
	for len(parts) < 13 {
		parts = append(parts, "")
	}

	idle, err := strconv.Atoi(parts[1])
	if err != nil {
		return hostRecord{Host: host, State: "ERR", Reason: "parse_error"}
	}
	total, err := strconv.Atoi(parts[2])
	if err != nil {
		return hostRecord{Host: host, State: "ERR", Reason: "parse_error"}
	}
	util, err := strconv.ParseFloat(parts[3], 64)
	if err != nil {
		return hostRecord{Host: host, State: "ERR", Reason: "parse_error"}
	}
	power, err := strconv.ParseFloat(parts[4], 64)
	if err != nil {
		return hostRecord{Host: host, State: "ERR", Reason: "parse_error"}
	}
	memUsed, err := strconv.ParseFloat(parts[5], 64)
	if err != nil {
		return hostRecord{Host: host, State: "ERR", Reason: "parse_error"}
	}
	memTotal, err := strconv.ParseFloat(parts[6], 64)
	if err != nil {
		return hostRecord{Host: host, State: "ERR", Reason: "parse_error"}
	}

	return hostRecord{
		Host:           host,
		State:          "OK",
		Idle:           idle,
		Total:          total,
		Util:           util,
		Power:          power,
		MemUsed:        memUsed,
		MemTotal:       memTotal,
		Yellow:         parts[7],
		IdleIDs:        parts[8],
		RawTag:         parts[9],
		CPUPct:         parts[10],
		HostMemUsedTB:  parts[11],
		HostMemTotalTB: parts[12],
	}
}

func calculateSparklineSize(enableCPU bool) int {
	termWidth := terminalWidth()
	// Row body width: host prefix (7) + formatted GPU cols (35) + yellow section (12) + leading space before sparkline (1) = 55
	fixedWidth := 55
	if enableCPU {
		// CPU+host-mem section: "  %3s%%  %-*s" with maxCPUMemLen=8 → 16 chars
		fixedWidth += 16
	}
	maxSize := termWidth - fixedWidth - 1
	if maxSize > 20 {
		maxSize = 20
	}
	if maxSize < 5 {
		maxSize = 5
	}
	return maxSize
}

func terminalWidth() int {
	if n := ttyWidth(); n > 0 {
		return n
	}
	if cols := os.Getenv("COLUMNS"); cols != "" {
		if n, err := strconv.Atoi(cols); err == nil && n > 0 {
			return n
		}
	}
	cmd := exec.Command("tput", "cols")
	out, err := cmd.Output()
	if err == nil {
		if n, parseErr := strconv.Atoi(strings.TrimSpace(string(out))); parseErr == nil && n > 0 {
			return n
		}
	}
	return 120
}

// ttyWidth asks the controlling terminal for its current column count via
// TIOCGWINSZ. Works regardless of how stdout is redirected and reflects the
// live resized size, which env/tput can miss.
func ttyWidth() int {
	var ioctlReq uintptr
	switch runtime.GOOS {
	case "linux":
		ioctlReq = 0x5413
	case "darwin", "freebsd", "netbsd", "openbsd", "dragonfly":
		ioctlReq = 0x40087468
	default:
		return 0
	}

	type winsize struct {
		Row, Col, Xpixel, Ypixel uint16
	}

	query := func(fd uintptr) int {
		var ws winsize
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, ioctlReq, uintptr(unsafe.Pointer(&ws)))
		if errno != 0 || ws.Col == 0 {
			return 0
		}
		return int(ws.Col)
	}

	if f, err := os.Open("/dev/tty"); err == nil {
		defer f.Close()
		if n := query(f.Fd()); n > 0 {
			return n
		}
	}
	for _, fd := range []uintptr{uintptr(syscall.Stderr), uintptr(syscall.Stdout), uintptr(syscall.Stdin)} {
		if n := query(fd); n > 0 {
			return n
		}
	}
	return 0
}

// parseRangeList expands "0-1,3,6-7" into [0,1,3,6,7]. Returns nil for empty.
func parseRangeList(s string) []int {
	if s == "" {
		return nil
	}
	var ids []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if dash := strings.Index(part, "-"); dash >= 0 {
			start, err1 := strconv.Atoi(part[:dash])
			end, err2 := strconv.Atoi(part[dash+1:])
			if err1 != nil || err2 != nil || start > end {
				continue
			}
			for i := start; i <= end; i++ {
				ids = append(ids, i)
			}
		} else {
			n, err := strconv.Atoi(part)
			if err != nil {
				continue
			}
			ids = append(ids, n)
		}
	}
	return ids
}

// updateIdleSince stamps newly-idle GPUs with `now` and drops any that are no
// longer idle, so state.IdleSince[host] always matches the current idle set.
func updateIdleSince(state *runtimeState, host string, idleIDs []int, now time.Time) {
	if state.IdleSince == nil {
		state.IdleSince = map[string]map[int]time.Time{}
	}
	tracked, ok := state.IdleSince[host]
	if !ok {
		tracked = map[int]time.Time{}
		state.IdleSince[host] = tracked
	}
	current := make(map[int]bool, len(idleIDs))
	for _, id := range idleIDs {
		current[id] = true
		if _, seen := tracked[id]; !seen {
			tracked[id] = now
		}
	}
	for id := range tracked {
		if !current[id] {
			delete(tracked, id)
		}
	}
}

// greenGPUs returns the set of GPUs on host that have been idle for at least
// threshold. Callers should have called updateIdleSince with the current idle
// set for this tick first.
func greenGPUs(state *runtimeState, host string, now time.Time, threshold time.Duration) map[int]bool {
	tracked := state.IdleSince[host]
	if len(tracked) == 0 {
		return nil
	}
	green := map[int]bool{}
	for id, since := range tracked {
		if now.Sub(since) >= threshold {
			green[id] = true
		}
	}
	return green
}

func appendHistory(history map[string][]historyPoint, host string, point historyPoint, maxHistory int) {
	entries := append(history[host], point)
	if len(entries) > maxHistory {
		entries = entries[len(entries)-maxHistory:]
	}
	history[host] = entries
}

func generateSparkline(entries []historyPoint) string {
	blocks := []rune{' ', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	var b strings.Builder
	for _, entry := range entries {
		if entry.Missing {
			b.WriteRune(' ')
			continue
		}
		index := int((entry.Power * 7) / 700)
		if index < 0 {
			index = 0
		}
		if index > 7 {
			index = 7
		}
		block := string(blocks[index])
		switch entry.Color {
		case "red":
			b.WriteString(colorRed)
			b.WriteString(block)
			b.WriteString(colorReset)
		case "yellow":
			b.WriteString(colorYellow)
			b.WriteString(block)
			b.WriteString(colorReset)
		case "green":
			b.WriteString(colorGreen)
			b.WriteString(block)
			b.WriteString(colorReset)
		default:
			b.WriteString(block)
		}
	}
	return b.String()
}

func applyTagSemantics(host string, rawTag string, state *runtimeState, tagCounts map[string]int) string {
	switch rawTag {
	case "MIXED", "NONE":
		state.LastTag[host] = ""
		return ""
	case "":
		return state.LastTag[host]
	default:
		state.LastTag[host] = rawTag
		tagCounts[rawTag]++
		return rawTag
	}
}

func probeAll(opts options, nodes []string) []hostRecord {
	type indexed struct {
		Index  int
		Record hostRecord
	}
	results := make([]hostRecord, len(nodes))
	ch := make(chan indexed, len(nodes))
	for i, host := range nodes {
		go func(idx int, h string) {
			ch <- indexed{Index: idx, Record: probeHost(h, opts.CPU, opts.Refresh)}
		}(i, host)
	}
	for i := 0; i < len(nodes); i++ {
		item := <-ch
		results[item.Index] = item.Record
	}
	close(ch)
	return results
}

func renderSnapshot(opts options, nodes []string, state *runtimeState, enableHistory bool, maxHistory int) string {
	results := probeAll(opts, nodes)

	maxYellowLen := 10
	maxCPUMemLen := 8
	for _, rec := range results {
		if rec.State != "OK" || rec.Yellow == "" {
			// Keep scanning for CPU memory width even when yellow section is empty.
		} else {
			length := len(rec.Yellow) + 2
			if length > maxYellowLen {
				maxYellowLen = length
			}
		}

		if opts.CPU && rec.State == "OK" {
			hostMemUsed := rec.HostMemUsedTB
			hostMemTotal := rec.HostMemTotalTB
			if hostMemUsed == "" {
				hostMemUsed = "0.0"
			}
			if hostMemTotal == "" {
				hostMemTotal = "0.0"
			}
			cpuMem := fmt.Sprintf("%s/%s TB", hostMemUsed, hostMemTotal)
			if len(cpuMem) > maxCPUMemLen {
				maxCPUMemLen = len(cpuMem)
			}
		}
	}

	tagCounts := map[string]int{}
	tagOrder := []string{"*", "†", "RL", "CM", "AU", "DA", "AR"}

	now := time.Now()
	var out strings.Builder
	for i, rec := range results {
		host := nodes[i]
		if rec.State != "OK" {
			// Probe failed; idle observations become untrustworthy, so drop any
			// accumulated idle_since for this host and restart on recovery.
			delete(state.IdleSince, host)
			if enableHistory {
				appendHistory(state.PowerHistory, host, historyPoint{Missing: true}, maxHistory)
			}

			display := "-"
			if enableHistory {
				if hist, ok := state.PowerHistory[host]; ok && len(hist) > 0 {
					baseWidth := 38 + 2 + maxYellowLen
					if opts.CPU {
						baseWidth += 8 + maxCPUMemLen
					}
					display = fmt.Sprintf("%-*s", baseWidth, display) + generateSparkline(hist)
				}
			}
			out.WriteString(fmt.Sprintf("%-6s %s\n", host+":", display))
			continue
		}

		idleIDs := parseRangeList(rec.IdleIDs)
		updateIdleSince(state, host, idleIDs, now)
		hasGreen := len(greenGPUs(state, host, now, idleGreenDuration)) > 0

		processTag := applyTagSemantics(host, rec.RawTag, state, tagCounts)
		availInfo := fmt.Sprintf("%d/%d", rec.Idle, rec.Total)
		if processTag != "" {
			availInfo += " " + processTag
		}

		formatted := fmt.Sprintf("%-7s %4.0f%% %7.1fW  %5.1f/%2.0f GB", availInfo, rec.Util, rec.Power, rec.MemUsed, rec.MemTotal)

		rowColor := ""
		histColor := "none"
		switch {
		case hasGreen:
			rowColor = colorGreen
			histColor = "green"
		case rec.Idle > 0:
			rowColor = colorRed
			histColor = "red"
		case rec.Yellow != "":
			rowColor = colorYellow
			histColor = "yellow"
		}

		yellowSection := ""
		if rec.Yellow != "" {
			yellowSection = "(" + rec.Yellow + ")"
		}
		visibleYellowLen := 0
		if rec.Yellow != "" {
			visibleYellowLen = len(rec.Yellow) + 2
		}
		yellowPad := maxYellowLen - visibleYellowLen
		if yellowPad < 0 {
			yellowPad = 0
		}
		formatted += "  " + yellowSection + strings.Repeat(" ", yellowPad)

		if opts.CPU {
			cpu := rec.CPUPct
			if cpu == "" {
				cpu = "0"
			}
			hostMemUsed := rec.HostMemUsedTB
			hostMemTotal := rec.HostMemTotalTB
			if hostMemUsed == "" {
				hostMemUsed = "0.0"
			}
			if hostMemTotal == "" {
				hostMemTotal = "0.0"
			}
			cpuMem := fmt.Sprintf("%s/%s TB", hostMemUsed, hostMemTotal)
			formatted += fmt.Sprintf("  %3s%%  %-*s", cpu, maxCPUMemLen, cpuMem)
		}

		sparkline := ""
		if enableHistory {
			appendHistory(state.PowerHistory, host, historyPoint{Power: rec.Power, Color: histColor}, maxHistory)
			sparkline = generateSparkline(state.PowerHistory[host])
			sparkline = " " + sparkline
		}

		if rowColor != "" {
			out.WriteString(fmt.Sprintf("%-6s %s%s%s%s\n", host+":", rowColor, formatted, colorReset, sparkline))
		} else {
			out.WriteString(fmt.Sprintf("%-6s %s%s\n", host+":", formatted, sparkline))
		}
	}

	hasTags := false
	for _, tag := range tagOrder {
		if tagCounts[tag] > 0 {
			hasTags = true
			break
		}
	}

	if hasTags {
		out.WriteString("\n")
		for _, tag := range tagOrder {
			if tagCounts[tag] > 0 {
				out.WriteString(fmt.Sprintf("%s:%d  ", tag, tagCounts[tag]))
			}
		}
		out.WriteString("\n")
	}

	return out.String()
}

func formatIdleDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	hours := int(d.Hours())
	mins := int(d.Minutes()) % 60
	if mins == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh%dm", hours, mins)
}

// renderPlain emits one unaligned-padded line per host: plain ASCII,
// key=value fields, suitable for piping into agents or logs. Reuses the same
// state-update path (parseRangeList, updateIdleSince, applyTagSemantics) as
// the TUI so cache semantics stay identical.
func renderPlain(opts options, records []hostRecord, state *runtimeState, now time.Time) string {
	type rowData struct {
		Host    string
		OK      bool
		Reason  string
		Idle    string
		IdleIDs string
		IdleFor string
		Power   string
		CPU     string
		Mem     string
		Spare   string
		Tag     string
	}

	tagCounts := map[string]int{}
	rows := make([]rowData, 0, len(records))

	for _, rec := range records {
		r := rowData{Host: rec.Host}
		if rec.State != "OK" {
			r.Reason = rec.Reason
			if r.Reason == "" {
				r.Reason = "unknown"
			}
			delete(state.IdleSince, rec.Host)
			rows = append(rows, r)
			continue
		}

		idleIDs := parseRangeList(rec.IdleIDs)
		updateIdleSince(state, rec.Host, idleIDs, now)
		tracked := state.IdleSince[rec.Host]

		var minDur time.Duration = -1
		for _, id := range idleIDs {
			if since, ok := tracked[id]; ok {
				d := now.Sub(since)
				if minDur < 0 || d < minDur {
					minDur = d
				}
			}
		}

		r.OK = true
		r.Idle = fmt.Sprintf("%d/%d", rec.Idle, rec.Total)
		r.IdleIDs = rec.IdleIDs
		if len(idleIDs) > 0 && minDur >= 0 {
			r.IdleFor = formatIdleDuration(minDur)
		}
		r.Power = fmt.Sprintf("%.0fW", rec.Power)
		r.Spare = rec.Yellow
		r.Tag = applyTagSemantics(rec.Host, rec.RawTag, state, tagCounts)
		if opts.CPU {
			cpu := rec.CPUPct
			if cpu == "" {
				cpu = "0"
			}
			r.CPU = cpu + "%"
			memUsed := rec.HostMemUsedTB
			if memUsed == "" {
				memUsed = "0.0"
			}
			memTotal := rec.HostMemTotalTB
			if memTotal == "" {
				memTotal = "0.0"
			}
			r.Mem = memUsed + "/" + memTotal + "TB"
		}
		rows = append(rows, r)
	}

	dash := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}

	wHost, wIdle, wIDs, wFor, wPow, wCPU, wMem, wSpare, wTag := 0, 5, 4, 4, 6, 4, 4, 6, 4
	for _, r := range rows {
		if l := len(r.Host) + 1; l > wHost {
			wHost = l
		}
		if !r.OK {
			continue
		}
		if l := len("idle=") + len(r.Idle); l > wIdle {
			wIdle = l
		}
		if l := len("ids=") + len(dash(r.IdleIDs)); l > wIDs {
			wIDs = l
		}
		if l := len("for=") + len(dash(r.IdleFor)); l > wFor {
			wFor = l
		}
		if l := len("power=") + len(r.Power); l > wPow {
			wPow = l
		}
		if opts.CPU {
			if l := len("cpu=") + len(r.CPU); l > wCPU {
				wCPU = l
			}
			if l := len("mem=") + len(r.Mem); l > wMem {
				wMem = l
			}
		}
		if l := len("spare=") + len(dash(r.Spare)); l > wSpare {
			wSpare = l
		}
		if l := len("tag=") + len(dash(r.Tag)); l > wTag {
			wTag = l
		}
	}

	var out strings.Builder
	for _, r := range rows {
		hostCol := fmt.Sprintf("%-*s", wHost, r.Host+":")
		if !r.OK {
			out.WriteString(fmt.Sprintf("%s  ERR   %s\n", hostCol, r.Reason))
			continue
		}
		parts := []string{
			hostCol,
			"OK ",
			fmt.Sprintf("%-*s", wIdle, "idle="+r.Idle),
			fmt.Sprintf("%-*s", wIDs, "ids="+dash(r.IdleIDs)),
			fmt.Sprintf("%-*s", wFor, "for="+dash(r.IdleFor)),
			fmt.Sprintf("%-*s", wPow, "power="+r.Power),
		}
		if opts.CPU {
			parts = append(parts,
				fmt.Sprintf("%-*s", wCPU, "cpu="+r.CPU),
				fmt.Sprintf("%-*s", wMem, "mem="+r.Mem),
			)
		}
		parts = append(parts,
			fmt.Sprintf("%-*s", wSpare, "spare="+dash(r.Spare)),
			fmt.Sprintf("%-*s", wTag, "tag="+dash(r.Tag)),
		)
		out.WriteString(strings.TrimRight(strings.Join(parts, "  "), " "))
		out.WriteString("\n")
	}
	return out.String()
}

func runOnce(opts options, nodes []string, state *runtimeState) {
	sparklineSize := calculateSparklineSize(opts.CPU)
	snapshot := renderSnapshot(opts, nodes, state, false, sparklineSize)
	_ = saveLastTagCache(state.LastTag)
	_ = saveIdleSinceCache(state.IdleSince)
	fmt.Print(snapshot)
}

func runCapture(opts options, nodes []string, state *runtimeState) {
	records := probeAll(opts, nodes)
	snapshot := renderPlain(opts, records, state, time.Now())
	_ = saveLastTagCache(state.LastTag)
	_ = saveIdleSinceCache(state.IdleSince)
	fmt.Print(snapshot)
}

func runRefresh(opts options, nodes []string, state *runtimeState) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	iteration := 0
	firstRun := true
	var startTime time.Time

	for {
		select {
		case <-sigCh:
			fmt.Println()
			return
		default:
		}

		if !firstRun {
			remaining := time.Duration(opts.Interval)*time.Second - time.Since(startTime)
			if remaining > 0 {
				timer := time.NewTimer(remaining)
				select {
				case <-sigCh:
					timer.Stop()
					fmt.Println()
					return
				case <-timer.C:
				}
			}
		}
		firstRun = false

		iteration++
		startTime = time.Now()

		sparklineSize := calculateSparklineSize(opts.CPU)
		snapshot := renderSnapshot(opts, nodes, state, true, sparklineSize)
		_ = saveLastTagCache(state.LastTag)
		_ = saveIdleSinceCache(state.IdleSince)

		fmt.Print("\033[2J\033[H")
		fmt.Print(snapshot)
		fmt.Printf("\nRefresh: %ds | Iteration: %d | Time: %s | Press Ctrl+C to exit", opts.Interval, iteration, time.Now().Format("15:04:05"))
	}
}

func main() {
	opts, err := parseArgs(os.Args[1:])
	if err != nil {
		if err.Error() != "missing group" {
			fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		}
		printUsage()
		os.Exit(1)
	}

	groups, err := loadGroups()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}

	nodes, ok := groups[opts.Group]
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: Group '%s' is not defined.\n", opts.Group)
		os.Exit(1)
	}

	state := &runtimeState{
		PowerHistory: map[string][]historyPoint{},
		LastTag:      map[string]string{},
		IdleSince:    map[string]map[int]time.Time{},
	}

	if cachedTags, err := loadLastTagCache(); err == nil {
		for host, tag := range cachedTags {
			state.LastTag[host] = tag
		}
	}
	if cachedIdle, err := loadIdleSinceCache(); err == nil {
		state.IdleSince = cachedIdle
	}

	switch {
	case opts.Capture:
		runCapture(opts, nodes, state)
	case opts.Refresh:
		runRefresh(opts, nodes, state)
	default:
		runOnce(opts, nodes, state)
	}
}
