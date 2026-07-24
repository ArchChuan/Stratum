package report

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/observe"
	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/process"
)

const Version = 1

var ErrBudgetExceeded = fmt.Errorf("report budget exceeded")

// Budget bounds report work and the cardinality of exact distributions. Zero
// fields select conservative defaults; callers can provide smaller limits in
// tests or controlled report jobs.
type Budget struct {
	MaxEventBytes              int64
	MaxRecords                 int
	MaxToolCardinality         int
	MaxSessionCardinality      int
	MaxServiceCardinality      int
	MaxDistributionCardinality int
	MaxWorkUnits               int64
}

const (
	defaultMaxEventBytes              int64 = 256 << 20
	defaultMaxRecords                       = 1_000_000
	defaultMaxToolCardinality               = 10_000
	defaultMaxSessionCardinality            = 10_000
	defaultMaxServiceCardinality            = 10_000
	defaultMaxDistributionCardinality       = 10_000
	defaultMaxWorkUnits               int64 = 2_000_000
)

func normalizeBudget(b Budget) Budget {
	if b.MaxEventBytes <= 0 {
		b.MaxEventBytes = defaultMaxEventBytes
	}
	if b.MaxRecords <= 0 {
		b.MaxRecords = defaultMaxRecords
	}
	if b.MaxToolCardinality <= 0 {
		b.MaxToolCardinality = defaultMaxToolCardinality
	}
	if b.MaxSessionCardinality <= 0 {
		b.MaxSessionCardinality = defaultMaxSessionCardinality
	}
	if b.MaxServiceCardinality <= 0 {
		b.MaxServiceCardinality = defaultMaxServiceCardinality
	}
	if b.MaxDistributionCardinality <= 0 {
		b.MaxDistributionCardinality = defaultMaxDistributionCardinality
	}
	if b.MaxWorkUnits <= 0 {
		b.MaxWorkUnits = defaultMaxWorkUnits
	}
	return b
}

type Completeness struct {
	Complete        bool     `json:"complete"`
	Degraded        bool     `json:"degraded"`
	RecordsRead     int      `json:"records_read"`
	RecordsDropped  int      `json:"records_dropped"`
	BytesRead       int64    `json:"bytes_read"`
	WorkUnits       int64    `json:"work_units"`
	OverflowReasons []string `json:"overflow_reasons,omitempty"`
}

type Report struct {
	Version      int          `json:"version"`
	Start        time.Time    `json:"start"`
	End          time.Time    `json:"end"`
	Tools        []ToolRow    `json:"tools"`
	Services     []ServiceRow `json:"services"`
	Completeness Completeness `json:"completeness"`
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
	durations                   map[int64]int
	responseBytes               map[int]int
}

type serviceAggregate struct {
	identities       map[process.Identity]struct{}
	peakPSS, peakUSS uint64
	maxConcurrent    int
	coldStarts       map[int64]int
}

type Accumulator struct {
	start, end time.Time
	tools      map[toolKey]*toolAggregate
	services   map[string]*serviceAggregate
	budget     Budget
	status     Completeness
	reasons    map[string]struct{}
}

func NewAccumulator(start, end time.Time) (*Accumulator, error) {
	return NewAccumulatorWithBudget(start, end, Budget{})
}

func NewAccumulatorWithBudget(start, end time.Time, budget Budget) (*Accumulator, error) {
	if start.IsZero() || end.IsZero() || !start.Before(end) {
		return nil, fmt.Errorf("report window must have start before end")
	}
	return &Accumulator{start: start, end: end, tools: make(map[toolKey]*toolAggregate),
		services: make(map[string]*serviceAggregate), budget: normalizeBudget(budget),
		status: Completeness{Complete: true}, reasons: make(map[string]struct{})}, nil
}

func (a *Accumulator) mark(reason string, dropped bool) {
	a.status.Complete = false
	a.status.Degraded = true
	if dropped {
		a.status.RecordsDropped++
	}
	if _, exists := a.reasons[reason]; !exists {
		a.reasons[reason] = struct{}{}
		a.status.OverflowReasons = append(a.status.OverflowReasons, reason)
	}
}

func (a *Accumulator) consumeWork(units int64) bool {
	if units <= 0 {
		return true
	}
	if a.status.WorkUnits > a.budget.MaxWorkUnits-units {
		a.mark("work_units", true)
		return false
	}
	a.status.WorkUnits += units
	return true
}

// StopReading reports whether continuing to decode raw records cannot improve
// the report because a hard input/work budget has already been reached.
func (a *Accumulator) StopReading() bool {
	for _, reason := range a.status.OverflowReasons {
		switch reason {
		case "event_bytes", "records", "work_units":
			return true
		}
	}
	return false
}

func (a *Accumulator) serviceFor(name string) *serviceAggregate {
	item := a.services[name]
	if item == nil {
		if len(a.services) >= a.budget.MaxServiceCardinality {
			a.mark("service_cardinality", true)
			return nil
		}
		item = &serviceAggregate{identities: make(map[process.Identity]struct{}), coldStarts: make(map[int64]int)}
		a.services[name] = item
	}
	return item
}

func (a *Accumulator) AddEvent(event observe.Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event for budget: %w", err)
	}
	return a.AddEventBytes(event, int64(len(data)))
}

// AddEventBytes records the physical input size when decoding JSONL. It
// validates every record but refuses to allocate new aggregate state after a
// configured budget is reached, leaving an explicit degraded marker.
func (a *Accumulator) AddEventBytes(event observe.Event, bytesRead int64) error {
	if err := event.Validate(); err != nil {
		return err
	}
	a.status.RecordsRead++
	if bytesRead > 0 {
		if a.status.BytesRead > a.budget.MaxEventBytes-bytesRead {
			a.mark("event_bytes", true)
			return nil
		}
		a.status.BytesRead += bytesRead
	}
	if a.status.RecordsRead > a.budget.MaxRecords {
		a.mark("records", true)
		return nil
	}
	if !a.consumeWork(1) {
		return nil
	}
	if event.At.Before(a.start) || !event.At.Before(a.end) {
		return nil
	}
	service := a.serviceFor(event.Service)
	if event.Kind == observe.KindSessionReady {
		if service != nil {
			service.coldStarts[event.DurationMS]++
		}
		return nil
	}
	if service == nil {
		return nil
	}
	if isHealthCheck(event.Tool) {
		return nil
	}
	service.maxConcurrent = max(service.maxConcurrent, event.ConcurrentCalls)
	key := toolKey{client: event.Client, service: event.Service, tool: event.Tool}
	item := a.tools[key]
	if item == nil {
		if len(a.tools) >= a.budget.MaxToolCardinality {
			a.mark("tool_cardinality", true)
			return nil
		}
		item = &toolAggregate{days: make(map[string]struct{}), sessions: make(map[string]struct{}),
			durations: make(map[int64]int), responseBytes: make(map[int]int)}
		a.tools[key] = item
	}
	item.calls++
	if event.Effective {
		item.effective++
	}
	if event.Outcome == observe.OutcomeSuccess {
		item.successes++
	}
	item.days[event.At.In(a.start.Location()).Format("2006-01-02")] = struct{}{}
	if _, exists := item.sessions[event.SessionHash]; !exists {
		if len(item.sessions) >= a.budget.MaxSessionCardinality {
			a.mark("session_cardinality", true)
		} else {
			item.sessions[event.SessionHash] = struct{}{}
		}
	}
	if _, exists := item.durations[event.DurationMS]; !exists {
		if len(item.durations) >= a.budget.MaxDistributionCardinality {
			a.mark("duration_cardinality", true)
		} else {
			item.durations[event.DurationMS] = 0
		}
	}
	if _, tracked := item.durations[event.DurationMS]; tracked {
		item.durations[event.DurationMS]++
	}
	if _, exists := item.responseBytes[event.ResponseBytes]; !exists {
		if len(item.responseBytes) >= a.budget.MaxDistributionCardinality {
			a.mark("response_bytes_cardinality", true)
		} else {
			item.responseBytes[event.ResponseBytes] = 0
		}
	}
	if _, tracked := item.responseBytes[event.ResponseBytes]; tracked {
		item.responseBytes[event.ResponseBytes]++
	}
	return nil
}

func (a *Accumulator) AddSnapshot(snapshot process.Snapshot) error {
	if snapshot.Version != 1 || snapshot.Mode != "observe" || snapshot.CapturedAt.IsZero() {
		return fmt.Errorf("version 1, observe mode, and captured_at are required")
	}
	inWindow := !snapshot.CapturedAt.Before(a.start) && snapshot.CapturedAt.Before(a.end)
	identities := make(map[process.Identity]struct{}, len(snapshot.Processes))
	memory := make(map[string]struct{ pss, uss uint64 })
	for i, item := range snapshot.Processes {
		if !a.consumeWork(1) {
			break
		}
		if item.PID <= 0 || item.StartTicks == 0 {
			return fmt.Errorf("process %d has invalid identity", i)
		}
		if _, exists := identities[item.Identity]; exists {
			return fmt.Errorf("duplicate process identity")
		}
		identities[item.Identity] = struct{}{}
		if strings.TrimSpace(item.Service) == "" {
			continue
		}
		if inWindow {
			service := a.serviceFor(item.Service)
			if service != nil {
				service.identities[item.Identity] = struct{}{}
			}
		}
		if !inWindow {
			continue
		}
		if _, exists := memory[item.Service]; !exists && len(memory) >= a.budget.MaxServiceCardinality {
			a.mark("service_cardinality", true)
			continue
		}
		current := memory[item.Service]
		if math.MaxUint64-current.pss < item.PSSBytes || math.MaxUint64-current.uss < item.USSBytes {
			return fmt.Errorf("service memory overflow")
		}
		current.pss += item.PSSBytes
		current.uss += item.USSBytes
		memory[item.Service] = current
	}
	if len(snapshot.Services) > 0 {
		clear(memory)
		for i, item := range snapshot.Services {
			if strings.TrimSpace(item.Service) == "" || item.Processes < 0 || item.Orphans < 0 {
				return fmt.Errorf("service summary %d is invalid", i)
			}
			if _, exists := memory[item.Service]; exists {
				return fmt.Errorf("duplicate service summary")
			}
			if inWindow && len(memory) >= a.budget.MaxServiceCardinality {
				a.mark("service_cardinality", true)
				continue
			}
			memory[item.Service] = struct{ pss, uss uint64 }{item.PSSBytes, item.USSBytes}
		}
	}
	for name, value := range memory {
		if !a.consumeWork(1) {
			break
		}
		if !inWindow {
			continue
		}
		service := a.serviceFor(name)
		if service == nil {
			continue
		}
		service.peakPSS = max(service.peakPSS, value.pss)
		service.peakUSS = max(service.peakUSS, value.uss)
	}
	return nil
}

func Aggregate(start, end time.Time, events []observe.Event, snapshots []process.Snapshot) (Report, error) {
	acc, err := NewAccumulator(start, end)
	if err != nil {
		return Report{}, err
	}
	for i, event := range events {
		if err := acc.AddEvent(event); err != nil {
			return Report{}, fmt.Errorf("event %d: %w", i, err)
		}
	}
	for i, snapshot := range snapshots {
		if err := acc.AddSnapshot(snapshot); err != nil {
			return Report{}, fmt.Errorf("snapshot %d: %w", i, err)
		}
	}
	return acc.Report(), nil
}

func (a *Accumulator) Report() Report {
	result := Report{Version: Version, Start: a.start, End: a.end, Tools: make([]ToolRow, 0, len(a.tools)),
		Services: make([]ServiceRow, 0, len(a.services)), Completeness: a.status}
	for key, item := range a.tools {
		result.Tools = append(result.Tools, ToolRow{Client: key.client, Service: key.service, Tool: key.tool,
			Calls: item.calls, EffectiveHits: item.effective, ActiveDays: len(item.days), DistinctSessions: len(item.sessions),
			SuccessRate: float64(item.successes) / float64(item.calls), P50DurationMS: percentileCounts(item.durations, 50),
			P95DurationMS: percentileCounts(item.durations, 95), P95ResponseBytes: percentileCounts(item.responseBytes, 95)})
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
	for name, item := range a.services {
		result.Services = append(result.Services, ServiceRow{Service: name, ProcessStarts: len(item.identities),
			PeakPSSBytes: item.peakPSS, PeakUSSBytes: item.peakUSS, MaxConcurrentCalls: item.maxConcurrent,
			P50ColdStartMS: percentileCounts(item.coldStarts, 50), P95ColdStartMS: percentileCounts(item.coldStarts, 95)})
	}
	sort.Slice(result.Services, func(i, j int) bool { return result.Services[i].Service < result.Services[j].Service })
	return result
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

func percentileCounts[T ~int | ~int64](counts map[T]int, percent int) T {
	if len(counts) == 0 {
		return 0
	}
	keys := make([]T, 0, len(counts))
	total := 0
	for value, count := range counts {
		keys = append(keys, value)
		total += count
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	rank := (percent*total + 99) / 100
	seen := 0
	for _, value := range keys {
		seen += counts[value]
		if seen >= rank {
			return value
		}
	}
	return keys[len(keys)-1]
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
