// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Summary renders the Markdown summary of a result directory.
func Summary(dir string) (string, error) {
	var h Host
	if err := readJSON(filepath.Join(dir, "host.json"), &h); err != nil {
		return "", err
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "# kb-bench results: %s\n\n", h.Hostname)
	sb.WriteString("Synthetic text only. Latencies are client-side, in milliseconds, after warm-up.\n\n## Host\n\n")
	fmt.Fprintf(&sb, "- Time: %s\n- Architecture: %s (%s)\n- CPU: %s\n- Logical cores: %d\n- RAM: %.1f GiB\n- CPU flags of interest: %s\n- Workload version: %s\n",
		h.Time, h.UnameM, h.OS, h.CPUModel, h.Cores, float64(h.RAMBytes)/(1<<30), strings.Join(h.Flags, " "), h.Workload)
	if len(h.Docker) > 0 {
		fmt.Fprintf(&sb, "- Docker: %s (server %s, %s cpus, %s)\n", h.Docker["serverVersion"], h.Docker["serverArch"], h.Docker["ncpu"], h.Docker["serverOS"])
	}
	for img, d := range h.Images {
		fmt.Fprintf(&sb, "- Image `%s`: %s\n", img, d)
	}
	for _, n := range h.Notes {
		fmt.Fprintf(&sb, "- Note: %s\n", n)
	}
	for _, kind := range []string{"embed", "rerank"} {
		files, _ := filepath.Glob(filepath.Join(dir, kind+"-*.json"))
		sort.Strings(files)
		for _, f := range files {
			var r PhaseResult
			if err := readJSON(f, &r); err != nil {
				return "", err
			}
			fmt.Fprintf(&sb, "\n## %s: %s\n\n", kind, r.Model)
			fmt.Fprintf(&sb, "Startup until /info: %.1f s, warm-up: %.2f s", r.StartupSec, r.WarmupSec)
			if r.Image != "" {
				fmt.Fprintf(&sb, ", image `%s`", r.Image)
			}
			sb.WriteString("\n\n| series | n | p50 | p90 | p99 | max | mean | req/s |\n|---|---|---|---|---|---|---|---|\n")
			keys := make([]string, 0, len(r.Stats))
			for k := range r.Stats {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				s := r.Stats[k]
				tp := ""
				if v, ok := r.Throughput[k]; ok {
					tp = fmt.Sprintf("%.2f", v)
				}
				fmt.Fprintf(&sb, "| %s | %d | %.1f | %.1f | %.1f | %.1f | %.1f | %s |\n", k, s.N, s.P50, s.P90, s.P99, s.Max, s.Mean, tp)
			}
		}
	}
	return sb.String(), nil
}
