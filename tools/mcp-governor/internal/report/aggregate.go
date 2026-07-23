package report

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/observe"
	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/process"
)

const Version = 1

type Report struct {
	Version  int          `json:"version"`
	Start    time.Time    `json:"start"`
	End      time.Time    `json:"end"`
	Tools    []ToolRow    `json:"tools"`
	Services []ServiceRow `json:"services"`
}

type ToolRow struct {
	Client           string  `json:"client"`
	Service          string  `json:"service"`
	Tool             string  `json:"tool"`
	Calls            int     `json:"calls"`
	EffectiveHits    int     `json:"effective_hits"`
	ActiveDays       int     `json:"active_days"`
	DistinctSessions int     `json:"distinct_sessions"`
	SuccessRate      float64 `json:"success_rate"`
	P50DurationMS    int64   `json:"p50_duration_ms"`
	P95DurationMS    int64   `json:"p95_duration_ms"`
	P95ResponseBytes int     `json:"p95_response_bytes"`
}

type ServiceRow struct {
	Service            string `json:"service"`
	ProcessStarts      int    `json:"process_starts"`
	PeakPSSBytes       uint64 `json:"peak_pss_bytes"`
	PeakUSSBytes       uint64 `json:"peak_uss_bytes"`
	MaxConcurrentCalls int    `json:"max_concurrent_calls"`
	P50ColdStartMS     int64  `json:"p50_cold_start_ms"`
	P95ColdStartMS     int64  `json:"p95_cold_start_ms"`
}

type toolKey struct{ client, service, tool string }

type toolAggregate struct {
	calls, effective, successes int
	days, sessions              map[string]struct{}
	durations                   []int64
	responseBytes               []int
}

type serviceAggregate struct {
	identities       map[process.Identity]struct{}
	peakPSS, peakUSS uint64
	maxConcurrent    int
	coldStarts       []int64
}

func Aggregate(start, end time.Time, events []observe.Event, snapshots []process.Snapshot) (Report, error) {
	if start.IsZero() || end.IsZero() || !start.Before(end) {
		return Report{}, fmt.Errorf("report window must have start before end")
	}
	tools := make(map[toolKey]*toolAggregate)
	services := make(map[string]*serviceAggregate)
	serviceFor := func(name string) *serviceAggregate {
		item := services[name]
		if item == nil {
			item = &serviceAggregate{identities: make(map[process.Identity]struct{})}
			services[name] = item
		}
		return item
	}
	for i, event := range events {
		if err := event.Validate(); err != nil {
			return Report{}, fmt.Errorf("event %d: %w", i, err)
		}
		if event.At.Before(start) || !event.At.Before(end) {
			continue
		}
		service := serviceFor(event.Service)
		if event.Kind == observe.KindSessionReady {
			service.coldStarts = append(service.coldStarts, event.DurationMS)
			continue
		}
		if isHealthCheck(event.Tool) {
			continue
		}
		if event.ConcurrentCalls > service.maxConcurrent {
			service.maxConcurrent = event.ConcurrentCalls
		}
		key := toolKey{client: event.Client, service: event.Service, tool: event.Tool}
		item := tools[key]
		if item == nil {
			item = &toolAggregate{days: make(map[string]struct{}), sessions: make(map[string]struct{})}
			tools[key] = item
		}
		item.calls++
		if event.Effective {
			item.effective++
		}
		if event.Outcome == observe.OutcomeSuccess {
			item.successes++
		}
		item.days[event.At.In(start.Location()).Format("2006-01-02")] = struct{}{}
		item.sessions[event.SessionHash] = struct{}{}
		item.durations = append(item.durations, event.DurationMS)
		item.responseBytes = append(item.responseBytes, event.ResponseBytes)
	}

	for _, snapshot := range snapshots {
		if snapshot.CapturedAt.Before(start) || !snapshot.CapturedAt.Before(end) {
			continue
		}
		memory := make(map[string]struct{ pss, uss uint64 })
		for _, item := range snapshot.Processes {
			if item.Service == "" {
				continue
			}
			serviceFor(item.Service).identities[item.Identity] = struct{}{}
			current := memory[item.Service]
			current.pss += item.PSSBytes
			current.uss += item.USSBytes
			memory[item.Service] = current
		}
		if len(snapshot.Services) > 0 {
			clear(memory)
			for _, item := range snapshot.Services {
				memory[item.Service] = struct{ pss, uss uint64 }{item.PSSBytes, item.USSBytes}
			}
		}
		for name, value := range memory {
			service := serviceFor(name)
			service.peakPSS = max(service.peakPSS, value.pss)
			service.peakUSS = max(service.peakUSS, value.uss)
		}
	}

	result := Report{Version: Version, Start: start, End: end, Tools: make([]ToolRow, 0, len(tools)),
		Services: make([]ServiceRow, 0, len(services))}
	for key, item := range tools {
		result.Tools = append(result.Tools, ToolRow{Client: key.client, Service: key.service, Tool: key.tool,
			Calls: item.calls, EffectiveHits: item.effective, ActiveDays: len(item.days), DistinctSessions: len(item.sessions),
			SuccessRate: float64(item.successes) / float64(item.calls), P50DurationMS: percentile(item.durations, 50),
			P95DurationMS: percentile(item.durations, 95), P95ResponseBytes: percentile(item.responseBytes, 95)})
	}
	sort.Slice(result.Tools, func(i, j int) bool {
		a, b := result.Tools[i], result.Tools[j]
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		if a.Tool != b.Tool {
			return a.Tool < b.Tool
		}
		return a.Client < b.Client
	})
	for name, item := range services {
		result.Services = append(result.Services, ServiceRow{Service: name, ProcessStarts: len(item.identities),
			PeakPSSBytes: item.peakPSS, PeakUSSBytes: item.peakUSS, MaxConcurrentCalls: item.maxConcurrent,
			P50ColdStartMS: percentile(item.coldStarts, 50), P95ColdStartMS: percentile(item.coldStarts, 95)})
	}
	sort.Slice(result.Services, func(i, j int) bool { return result.Services[i].Service < result.Services[j].Service })
	return result, nil
}

func percentile[T ~int | ~int64](values []T, percent int) T {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]T(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	index := (percent*len(sorted)+99)/100 - 1
	return sorted[index]
}

func isHealthCheck(tool string) bool {
	normalized := strings.ToLower(strings.TrimSpace(tool))
	switch normalized {
	case "health", "health_check", "healthcheck", "ping":
		return true
	default:
		return false
	}
}
