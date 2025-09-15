package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
)

var (
	postURL   = flag.String("url", "", "backend url")
	interval  = flag.Duration("interval", 0*time.Second, "collect freq: 30s / 5m / 1h")
	showJSON  = flag.Bool("print", false, "print json to stdout")
	httpCli   = &http.Client{Timeout: 10 * time.Second}
	startTime = time.Now()
)

// ------- 数据结构 -------

type HostInfo struct {
	CollectedAt time.Time `json:"collected_at"`

	OS      OSInfo      `json:"os"`
	CPU     CPUInfo     `json:"cpu"`
	Memory  MemInfo     `json:"memory"`
	Disk    []DiskInfo  `json:"disk"`
	GPU     []GPUInfo   `json:"gpu"`
	Uptime  uint64      `json:"uptime_sec"`
	LoadAvg LoadAverage `json:"load_average"`
}

type OSInfo struct {
	Platform string `json:"platform"`
	Family   string `json:"family"`
	Version  string `json:"version"`
	Kernel   string `json:"kernel"`
}

type CPUInfo struct {
	ModelName string  `json:"model_name"`
	Cores     int     `json:"cores"`
	Threads   int     `json:"threads"`
	Mhz       float64 `json:"mhz"`
	LoadPct   float64 `json:"load_pct"`
}

type MemInfo struct {
	Total uint64 `json:"total_bytes"`
	Used  uint64 `json:"used_bytes"`
	Free  uint64 `json:"free_bytes"`
}

type DiskInfo struct {
	Mount   string  `json:"mount"`
	FsType  string  `json:"fs_type"`
	Total   uint64  `json:"total_bytes"`
	Used    uint64  `json:"used_bytes"`
	Free    uint64  `json:"free_bytes"`
	UsedPct float64 `json:"used_pct"`
}

type GPUInfo struct {
	Index       int     `json:"index"`
	UUID        string  `json:"uuid"`
	Name        string  `json:"name"`
	Temp        int     `json:"temperature_c"`
	PowerDrawW  float64 `json:"power_draw_w"`
	PowerLimitW float64 `json:"power_limit_w"`
	MemTotalMB  int     `json:"memory_total_mb"`
	MemUsedMB   int     `json:"memory_used_mb"`
	MemFreeMB   int     `json:"memory_free_mb"`
	UtilPct     int     `json:"util_pct"`

	Processes []GPUProc `json:"processes,omitempty"`
	Driver    string    `json:"driver_version,omitempty"`
	Cuda      string    `json:"cuda_version,omitempty"`
}

type GPUProc struct {
	PID     int    `json:"pid"`
	Name    string `json:"process_name"`
	UsedMB  int    `json:"used_memory_mb"`
	GPUUUID string `json:"gpu_uuid"`
}

type LoadAverage struct {
	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`
}

func collect() (*HostInfo, error) {
	now := time.Now()

	// OS
	hInfo, err := host.Info()
	if err != nil {
		return nil, err
	}
	osInfo := OSInfo{
		Platform: hInfo.Platform,
		Family:   hInfo.PlatformFamily,
		Version:  hInfo.PlatformVersion,
		Kernel:   hInfo.KernelVersion,
	}

	// CPU
	cpuInfos, err := cpu.Info()
	if err != nil || len(cpuInfos) == 0 {
		return nil, fmt.Errorf("cpu.Info: %v", err)
	}
	logicalCnt, _ := cpu.Counts(true)
	cpuPercent, _ := cpu.Percent(0, false)
	cpuInfo := CPUInfo{
		ModelName: cpuInfos[0].ModelName,
		Cores:     int(cpuInfos[0].Cores),
		Threads:   logicalCnt,
		Mhz:       cpuInfos[0].Mhz,
	}
	if len(cpuPercent) > 0 {
		cpuInfo.LoadPct = cpuPercent[0]
	}

	// Load average
	la, _ := load.Avg()
	loadAvg := LoadAverage{Load1: la.Load1, Load5: la.Load5, Load15: la.Load15}

	// Mem
	vm, _ := mem.VirtualMemory()
	memInfo := MemInfo{
		Total: vm.Total,
		Used:  vm.Used,
		Free:  vm.Free,
	}

	// Disk
	var disks []DiskInfo
	partitions, _ := disk.Partitions(false)
	for _, p := range partitions {
		if p.Fstype == "" {
			continue
		}
		du, err := disk.Usage(p.Mountpoint)
		if err != nil {
			continue
		}
		disks = append(disks, DiskInfo{
			Mount:   p.Mountpoint,
			FsType:  p.Fstype,
			Total:   du.Total,
			Used:    du.Used,
			Free:    du.Free,
			UsedPct: du.UsedPercent,
		})
	}

	// GPU
	gpus, _ := queryGPU()

	return &HostInfo{
		CollectedAt: now,
		OS:          osInfo,
		CPU:         cpuInfo,
		Memory:      memInfo,
		Disk:        disks,
		GPU:         gpus,
		Uptime:      hInfo.Uptime,
		LoadAvg:     loadAvg,
	}, nil
}

func queryGPU() ([]GPUInfo, error) {
	if _, err := exec.LookPath("nvidia-smi"); err != nil {
		return nil, nil
	}

	verOut, _ := exec.Command("nvidia-smi", "--version").Output()
	driver, cuda := parseVersion(string(verOut))

	queryArgs := []string{
		"--query-gpu=index,gpu_uuid,name,temperature.gpu,power.draw,power.limit,memory.total,memory.used,memory.free,utilization.gpu",
		"--format=csv,noheader,nounits",
	}
	mainOut, err := exec.Command("nvidia-smi", queryArgs...).Output()
	if err != nil {
		return nil, err
	}

	procArgs := []string{
		"--query-compute-apps=gpu_uuid,gpu_name,pid,process_name,used_memory",
		"--format=csv,noheader,nounits",
	}
	procOut, _ := exec.Command("nvidia-smi", procArgs...).Output()
	procMap := parseProc(string(procOut))

	var gpus []GPUInfo
	r := csv.NewReader(bytes.NewReader(mainOut))
	r.TrimLeadingSpace = true
	records, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	for _, rec := range records {
		if len(rec) != 10 {
			continue
		}
		g := GPUInfo{
			Index:       atoi(rec[0]),
			UUID:        strings.TrimSpace(rec[1]),
			Name:        rec[2],
			Temp:        atoi(rec[3]),
			PowerDrawW:  atof(rec[4]),
			PowerLimitW: atof(rec[5]),
			MemTotalMB:  atoi(rec[6]),
			MemUsedMB:   atoi(rec[7]),
			MemFreeMB:   atoi(rec[8]),
			UtilPct:     atoi(rec[9]),
			Driver:      driver,
			Cuda:        cuda,
		}
		if procs, ok := procMap[g.UUID]; ok {
			g.Processes = procs
		}
		gpus = append(gpus, g)
	}
	return gpus, nil
}

func parseVersion(out string) (driver, cuda string) {
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "Driver version") || strings.Contains(ln, "DRIVER version") {
			f := strings.Fields(ln)
			driver = f[len(f)-1]
		} else if strings.HasPrefix(ln, "CUDA Version") || strings.Contains(ln, "CUDA Version") {
			f := strings.Fields(ln)
			cuda = f[len(f)-1]
		}
	}
	return
}

func parseProc(out string) map[string][]GPUProc {
	res := map[string][]GPUProc{}
	if len(out) == 0 {
		return res
	}
	r := csv.NewReader(strings.NewReader(out))
	r.TrimLeadingSpace = true
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil || len(rec) != 5 {
			continue
		}
		uuid := strings.TrimSpace(rec[0])
		proc := GPUProc{
			GPUUUID: uuid,
			Name:    strings.TrimSpace(rec[3]),
			PID:     atoi(rec[2]),
			UsedMB:  atoi(rec[4]),
		}
		res[uuid] = append(res[uuid], proc)
	}
	return res
}

func atoi(s string) int {
	v, _ := strconv.Atoi(strings.TrimSpace(s))
	return v
}
func atof(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

func send(info *HostInfo) error {
	body, _ := json.Marshal(info)
	if *showJSON || *postURL == "" {
		fmt.Printf("%s\n", body)
		return nil
	}
	req, _ := http.NewRequest(http.MethodPost, *postURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpCli.Do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("http status %d", resp.StatusCode)
	}
	return nil
}

func main() {
	flag.Parse()
	if !*showJSON && *postURL == "" {
		fmt.Fprintln(os.Stderr, "Usage Flag: -url or -print")
		os.Exit(1)
	}

	fmt.Printf("HostInfo Collector started (Go %s), interval=%s, target=%s\n",
		runtime.Version(), *interval, *postURL)

	for {
		info, err := collect()
		if err != nil {
			fmt.Fprintf(os.Stderr, "collect error: %v\n", err)
		} else if err := send(info); err != nil {
			fmt.Fprintf(os.Stderr, "send error: %v\n", err)
		}

		if interval == nil || *interval <= 0 {
			break
		}
		time.Sleep(*interval)
	}
}
