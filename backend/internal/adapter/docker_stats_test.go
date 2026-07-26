package adapter

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDockerAPIContainerStatsCalculatesCPUAndCgroupV1Memory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/containers/aifar-pod%2Fpermission/stats" {
			t.Fatalf("path = %q", r.URL.EscapedPath())
		}
		if got := r.URL.Query().Get("stream"); got != "false" {
			t.Fatalf("stream = %q, want false", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"container-1","name":"/aifar-pod-permission",
			"cpu_stats":{"cpu_usage":{"total_usage":1200,"percpu_usage":[600,600]},"system_cpu_usage":5000,"online_cpus":2},
			"precpu_stats":{"cpu_usage":{"total_usage":1000},"system_cpu_usage":4000},
			"memory_stats":{"usage":1000,"limit":2048,"stats":{"total_inactive_file":200,"inactive_file":50}}
		}`))
	}))
	defer server.Close()

	stat, err := dockerAPIContainerStats(context.Background(), server.URL, "aifar-pod/permission")
	if err != nil {
		t.Fatal(err)
	}
	if stat.ID != "container-1" || stat.Name != "aifar-pod-permission" {
		t.Fatalf("identity = %+v", stat)
	}
	if math.Abs(stat.CPUPerc-40) > 0.001 || math.Abs(stat.MemPerc-39.0625) > 0.001 {
		t.Fatalf("percentages = cpu %.4f memory %.4f", stat.CPUPerc, stat.MemPerc)
	}
	if stat.MemUsage != "800 B / 2.0 KiB" || stat.RawCPUPerc != "40.00%" || stat.RawMemPercent != "39.06%" {
		t.Fatalf("formatted stats = %+v", stat)
	}
}

func TestDockerAPIStatsCalculations(t *testing.T) {
	tests := []struct {
		name      string
		payload   dockerAPIContainerStatsPayload
		wantCPU   float64
		wantUsage uint64
		wantLimit uint64
		wantMem   float64
	}{
		{
			name:    "online cpu fallback and cgroup v2 cache",
			payload: statsPayload(1200, 1000, 5000, 4000, 0, []uint64{1, 1}, 1000, 2000, map[string]uint64{"inactive_file": 250}),
			wantCPU: 40, wantUsage: 750, wantLimit: 2000, wantMem: 37.5,
		},
		{
			name:    "counter rollback and zero limit",
			payload: statsPayload(900, 1000, 3000, 4000, 2, nil, 100, 0, nil),
			wantCPU: 0, wantUsage: 100, wantLimit: 0, wantMem: 0,
		},
		{
			name:    "cache larger than usage",
			payload: statsPayload(0, 0, 0, 0, 0, nil, 100, 1000, map[string]uint64{"total_inactive_file": 200}),
			wantCPU: 0, wantUsage: 0, wantLimit: 1000, wantMem: 0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := dockerAPIStatsCPUPercent(test.payload); math.Abs(got-test.wantCPU) > 0.001 {
				t.Fatalf("CPU = %.4f, want %.4f", got, test.wantCPU)
			}
			usage, limit, memory := dockerAPIStatsMemory(test.payload)
			if usage != test.wantUsage || limit != test.wantLimit || math.Abs(memory-test.wantMem) > 0.001 {
				t.Fatalf("memory = (%d, %d, %.4f), want (%d, %d, %.4f)", usage, limit, memory, test.wantUsage, test.wantLimit, test.wantMem)
			}
		})
	}
}

func statsPayload(currentCPU, previousCPU, currentSystem, previousSystem, onlineCPUs uint64, perCPU []uint64, usage, limit uint64, memory map[string]uint64) dockerAPIContainerStatsPayload {
	var payload dockerAPIContainerStatsPayload
	payload.CPUStats.CPUUsage.TotalUsage = currentCPU
	payload.CPUStats.CPUUsage.PercpuUsage = perCPU
	payload.CPUStats.SystemCPUUsage = currentSystem
	payload.CPUStats.OnlineCPUs = onlineCPUs
	payload.PreCPUStats.CPUUsage.TotalUsage = previousCPU
	payload.PreCPUStats.SystemCPUUsage = previousSystem
	payload.MemoryStats.Usage = usage
	payload.MemoryStats.Limit = limit
	payload.MemoryStats.Stats = memory
	return payload
}
