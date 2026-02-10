package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	defaultRefreshInterval = 30
	idlePowerW             = 100
	idleMemMB              = 1024
	idleUtilPct            = 5
	yellowSpareMemGB       = 40
	yellowPowerW           = 250
)

const (
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
	colorReset  = "\033[0m"
)

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
  }

  spare_mem_gpu = (mem_total - mem_used) / 1024
  if (spare_mem_gpu > yellow_spare || power < yellow_power) {
    yellow[gpu_index] = 1
  }
}
END {
  if (gpu_count == 0) {
    exit 1
  }

  avg_util = total_util / gpu_count
  avg_power = total_power / gpu_count
  mem_used_gb = (total_mem_used / 1024) / gpu_count
  mem_total_gb = (total_mem_total / 1024) / gpu_count

  yellow_str = ""
  range_start = -1
  range_end = -1
  for (i = 0; i < gpu_count; i++) {
    if (yellow[i] == 1) {
      if (range_start == -1) {
        range_start = i
        range_end = i
      } else if (i == range_end + 1) {
        range_end = i
      } else {
        if (yellow_str != "") yellow_str = yellow_str ","
        if (range_start == range_end) {
          yellow_str = yellow_str range_start
        } else {
          yellow_str = yellow_str range_start "-" range_end
        }
        range_start = i
        range_end = i
      }
    }
  }

  if (range_start != -1) {
    if (yellow_str != "") yellow_str = yellow_str ","
    if (range_start == range_end) {
      yellow_str = yellow_str range_start
    } else {
      yellow_str = yellow_str range_start "-" range_end
    }
  }

  printf "%d\t%d\t%.0f\t%.1f\t%.1f\t%.0f\t%s", idle_count, gpu_count, avg_util, avg_power, mem_used_gb, mem_total_gb, yellow_str
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
      [[ $check_kk -eq 1 ]] && process_tag="KK"
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
	RawTag         string
	CPUPct         string
	HostMemUsedTB  string
	HostMemTotalTB string
}

func printUsage() {
	fmt.Println("Usage: cgpus [--cpu] [-f [INTERVAL]] GROUP")
	fmt.Println("  --cpu         Show CPU load and host memory usage")
	fmt.Println("  -f            Enable refresh mode (default: 30s interval)")
	fmt.Println("  -f INTERVAL   Enable refresh mode with custom interval (seconds)")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  cgpus zyphra")
	fmt.Println("  cgpus --cpu zyphra")
	fmt.Println("  cgpus -f zyphra")
	fmt.Println("  cgpus -f 5 zyphra")
	fmt.Println("  cgpus --cpu -f 5 zyphra")
}

func parseArgs(args []string) (options, error) {
	opts := options{Interval: defaultRefreshInterval}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--cpu":
			opts.CPU = true
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

	return opts, nil
}

func loadGroups() (map[string][]string, error) {
	script := `source "$HOME/.ssh/ssh_key_groups.sh" >/dev/null 2>&1 || exit 1; for k v in ${(kv)GROUPS}; do print -r -- "$k"$'\t'"$v"; done`
	cmd := exec.Command("zsh", "-lc", script)
	out, err := cmd.Output()
	if err != nil {
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
	case "*", "KK", "RL", "CM", "AU", "DA", "AR":
		return true
	default:
		return false
	}
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
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

	cmd := exec.Command("ssh", sshArgs...)
	cmd.Stdin = strings.NewReader(remoteProbeScript)
	output, err := cmd.Output()
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

	for len(parts) < 12 {
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
		RawTag:         parts[8],
		CPUPct:         parts[9],
		HostMemUsedTB:  parts[10],
		HostMemTotalTB: parts[11],
	}
}

func calculateSparklineSize(enableCPU bool) int {
	termWidth := terminalWidth()
	fixedWidth := 61
	if enableCPU {
		// CPU+host-mem section estimate: "  %3s%%  used/total TB" ~= 19 chars
		fixedWidth += 19
	}
	maxSize := termWidth - fixedWidth - 4
	if maxSize > 20 {
		maxSize = 20
	}
	if maxSize < 5 {
		maxSize = 5
	}
	return maxSize
}

func terminalWidth() int {
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

func stdoutIsTTY() bool {
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

func enterAlternateScreen() bool {
	if !stdoutIsTTY() {
		return false
	}
	if strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return false
	}
	fmt.Print("\033[?1049h\033[?25l")
	return true
}

func leaveAlternateScreen(enabled bool) {
	if !enabled {
		return
	}
	fmt.Print("\033[?25h\033[?1049l")
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

func renderSnapshot(opts options, nodes []string, state *runtimeState, enableHistory bool, maxHistory int) string {
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
	tagOrder := []string{"*", "KK", "RL", "CM", "AU", "DA", "AR"}

	var out strings.Builder
	for i, rec := range results {
		host := nodes[i]
		if rec.State != "OK" {
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

		processTag := applyTagSemantics(host, rec.RawTag, state, tagCounts)
		availInfo := fmt.Sprintf("%d/%d", rec.Idle, rec.Total)
		if processTag != "" {
			availInfo += " " + processTag
		}

		formatted := fmt.Sprintf("%-7s %4.0f%% %7.1fW  %5.1f/%2.0f GB", availInfo, rec.Util, rec.Power, rec.MemUsed, rec.MemTotal)
		yellowSection := ""
		if rec.Yellow != "" {
			yellowSection = "(" + rec.Yellow + ")"
		}
		formatted += "  " + fmt.Sprintf("%-*s", maxYellowLen, yellowSection)

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

		rowColor := ""
		histColor := "none"
		if rec.Idle > 0 {
			rowColor = colorRed
			histColor = "red"
		} else if rec.Yellow != "" {
			rowColor = colorYellow
			histColor = "yellow"
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

func runOnce(opts options, nodes []string, state *runtimeState) {
	sparklineSize := calculateSparklineSize(opts.CPU)
	snapshot := renderSnapshot(opts, nodes, state, false, sparklineSize)
	_ = saveLastTagCache(state.LastTag)
	fmt.Print(snapshot)
}

func runRefresh(opts options, nodes []string, state *runtimeState) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	useAltScreen := enterAlternateScreen()
	defer leaveAlternateScreen(useAltScreen)

	iteration := 0
	firstRun := true
	var startTime time.Time

	for {
		select {
		case <-sigCh:
			if !useAltScreen {
				fmt.Println()
			}
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
					if !useAltScreen {
						fmt.Println()
					}
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
	}

	if cachedTags, err := loadLastTagCache(); err == nil {
		for host, tag := range cachedTags {
			state.LastTag[host] = tag
		}
	}

	if opts.Refresh {
		runRefresh(opts, nodes, state)
	} else {
		runOnce(opts, nodes, state)
	}
}
