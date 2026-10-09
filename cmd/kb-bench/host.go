// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Host records the facts needed to interpret a result.
type Host struct {
	Hostname string            `json:"hostname"`
	Time     string            `json:"time"`
	Workload string            `json:"workload"`
	UnameM   string            `json:"unameM"`
	OS       string            `json:"os"`
	CPUModel string            `json:"cpuModel"`
	Cores    int               `json:"logicalCores"`
	Flags    []string          `json:"cpuFlags"`
	RAMBytes int64             `json:"ramBytes"`
	Docker   map[string]string `json:"docker"`
	Images   map[string]string `json:"images"`
	Notes    []string          `json:"notes,omitempty"`
}

// flagOfInterest selects the CPU flags that decide TEI's CPU kernels.
func flagOfInterest(f string) bool {
	f = strings.ToLower(f)
	for _, p := range []string{"avx", "fma", "amx", "f16c", "bmi", "vnni", "sse4", "neon", "asimd", "sve", "i8mm", "bf16", "dotprod", "fphp", "asimdhp"} {
		if strings.HasPrefix(f, p) {
			return true
		}
	}
	return false
}

// ParseCPUInfo extracts the model, the logical core count and the flags of
// interest from /proc/cpuinfo text.
func ParseCPUInfo(text string) (model string, cores int, flags []string) {
	set := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "processor":
			cores++
		case "model name":
			if model == "" {
				model = v
			}
		case "flags", "Features":
			for _, f := range strings.Fields(v) {
				if flagOfInterest(f) {
					set[strings.ToLower(f)] = true
				}
			}
		}
	}
	for f := range set {
		flags = append(flags, f)
	}
	sort.Strings(flags)
	return
}

func run(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// CollectHost gathers the host facts. Missing facts stay empty; none is fatal.
func CollectHost(images []string) Host {
	h := Host{Time: time.Now().UTC().Format(time.RFC3339), Workload: WorkloadVersion, OS: runtime.GOOS,
		Cores: runtime.NumCPU(), Docker: map[string]string{}, Images: map[string]string{}}
	h.Hostname, _ = os.Hostname()
	h.Hostname = strings.SplitN(h.Hostname, ".", 2)[0]
	h.UnameM = run("uname", "-m")
	switch runtime.GOOS {
	case "linux":
		if b, err := os.ReadFile("/proc/cpuinfo"); err == nil {
			model, cores, flags := ParseCPUInfo(string(b))
			h.CPUModel, h.Flags = model, flags
			if cores > 0 {
				h.Cores = cores
			}
		}
		if b, err := os.ReadFile("/proc/meminfo"); err == nil {
			for _, l := range strings.Split(string(b), "\n") {
				if strings.HasPrefix(l, "MemTotal:") {
					f := strings.Fields(l)
					if len(f) >= 2 {
						kb, _ := strconv.ParseInt(f[1], 10, 64)
						h.RAMBytes = kb * 1024
					}
				}
			}
		}
		if h.CPUModel == "" {
			h.CPUModel = run("lscpu")
		}
	case "darwin":
		h.CPUModel = run("sysctl", "-n", "machdep.cpu.brand_string")
		h.RAMBytes, _ = strconv.ParseInt(run("sysctl", "-n", "hw.memsize"), 10, 64)
		var feats []string
		for _, k := range []string{"machdep.cpu.features", "machdep.cpu.leaf7_features"} {
			feats = append(feats, strings.Fields(run("sysctl", "-n", k))...)
		}
		set := map[string]bool{}
		for _, f := range feats {
			if flagOfInterest(f) {
				set[strings.ToLower(f)] = true
			}
		}
		for _, k := range []string{"FEAT_BF16", "FEAT_I8MM", "FEAT_DotProd", "FEAT_FP16", "AdvSIMD"} {
			if run("sysctl", "-n", "hw.optional.arm."+k) == "1" || run("sysctl", "-n", "hw.optional."+strings.ToLower(k)) == "1" {
				set[strings.ToLower(k)] = true
			}
		}
		for f := range set {
			h.Flags = append(h.Flags, f)
		}
		sort.Strings(h.Flags)
	}
	if v := run("docker", "version", "--format", "{{.Server.Version}}"); v != "" {
		h.Docker["serverVersion"] = v
		h.Docker["serverArch"] = run("docker", "info", "--format", "{{.Architecture}}")
		h.Docker["serverOS"] = run("docker", "info", "--format", "{{.OperatingSystem}}")
		h.Docker["ncpu"] = run("docker", "info", "--format", "{{.NCPU}}")
		h.Docker["memTotalBytes"] = run("docker", "info", "--format", "{{.MemTotal}}")
		if h.Docker["serverOS"] != "" && runtime.GOOS == "darwin" {
			h.Notes = append(h.Notes, "Docker runs in a Linux VM on this host; its CPU and memory are the VM's, not the host's.")
		}
	}
	for _, img := range images {
		if img == "" {
			continue
		}
		d := run("docker", "image", "inspect", "--format", "{{.Id}} {{join .RepoDigests \",\"}}", img)
		h.Images[img] = d
	}
	return h
}
