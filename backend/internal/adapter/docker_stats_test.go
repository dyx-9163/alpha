package adapter

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aifar-deployment/backend/internal/store"
)

func TestDockerContainerStatsForServerUsesEngineAPIWithoutLocalCLI(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{
			"id":"container-1","name":"/pod-1",
			"cpu_stats":{"cpu_usage":{"total_usage":1200},"system_cpu_usage":5000,"online_cpus":1},
			"precpu_stats":{"cpu_usage":{"total_usage":1000},"system_cpu_usage":4000},
			"memory_stats":{"usage":500,"limit":1000,"stats":{}}
		}`))
	}))
	defer server.Close()

	stats, err := DockerContainerStatsForServer(context.Background(), store.Server{DockerHost: server.URL}, []string{"pod-1"})
	if err != nil || len(stats) != 1 || calls.Load() != 1 {
		t.Fatalf("stats=%+v calls=%d err=%v", stats, calls.Load(), err)
	}
}

func TestDockerAPIContainerStatsBatchKeepsSuccessfulRows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/missing/") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{
			"id":"good-id","name":"/good",
			"cpu_stats":{"cpu_usage":{"total_usage":2},"system_cpu_usage":2,"online_cpus":1},
			"precpu_stats":{"cpu_usage":{"total_usage":1},"system_cpu_usage":1},
			"memory_stats":{"usage":1,"limit":2,"stats":{}}
		}`))
	}))
	defer server.Close()

	stats, err := dockerAPIContainerStatsBatch(context.Background(), server.URL, []string{"", "good", "missing", "good"})
	if len(stats) != 1 || stats[0].Name != "good" {
		t.Fatalf("partial stats = %+v", stats)
	}
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("partial error = %v", err)
	}
}

func TestDockerAPIContainerStatsBatchLimitsConcurrencyToFour(t *testing.T) {
	entered := make(chan struct{}, 5)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		id := strings.Split(strings.Trim(r.URL.Path, "/"), "/")[1]
		_, _ = fmt.Fprintf(w, `{"id":%q,"name":%q,"memory_stats":{"usage":1,"limit":2,"stats":{}}}`, id, "/"+id)
	}))
	defer server.Close()

	type batchResult struct {
		stats []DockerContainerStat
		err   error
	}
	done := make(chan batchResult, 1)
	go func() {
		stats, err := dockerAPIContainerStatsBatch(context.Background(), server.URL, []string{"p1", "p2", "p3", "p4", "p5"})
		done <- batchResult{stats: stats, err: err}
	}()
	for index := 0; index < 4; index++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("four workers did not enter")
		}
	}
	select {
	case <-entered:
		t.Fatal("fifth request started before a worker was released")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	result := <-done
	if result.err != nil || len(result.stats) != 5 {
		t.Fatalf("stats=%+v err=%v", result.stats, result.err)
	}
}

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
