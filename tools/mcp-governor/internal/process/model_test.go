package process

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSnapshotJSONContract(t *testing.T) {
	snapshot := Snapshot{
		Version:    1,
		CapturedAt: time.Unix(0, 0).UTC(),
		Mode:       "observe",
		Processes: []Process{
			{
				Identity: Identity{
					PID:        42,
					StartTicks: 99,
				},
				Service:  "chroma",
				RSSBytes: 4096,
				PSSBytes: 3072,
				USSBytes: 2048,
			},
		},
		Services: []ServiceSummary{
			{
				Service:   "chroma",
				Processes: 1,
				RSSBytes:  8192,
				PSSBytes:  6144,
				USSBytes:  5120,
			},
		},
	}

	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}

	var contract struct {
		Version   int    `json:"version"`
		Mode      string `json:"mode"`
		Processes []struct {
			PID        int    `json:"pid"`
			StartTicks uint64 `json:"start_ticks"`
			RSSBytes   uint64 `json:"rss_bytes"`
			PSSBytes   uint64 `json:"pss_bytes"`
			USSBytes   uint64 `json:"uss_bytes"`
		} `json:"processes"`
		Services []struct {
			RSSBytes uint64 `json:"rss_bytes"`
			PSSBytes uint64 `json:"pss_bytes"`
			USSBytes uint64 `json:"uss_bytes"`
		} `json:"services"`
	}
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatalf("unmarshal snapshot contract: %v", err)
	}

	if contract.Version != 1 || contract.Mode != "observe" {
		t.Errorf("snapshot metadata = version %d, mode %q; want version 1, mode observe", contract.Version, contract.Mode)
	}
	if len(contract.Processes) != 1 {
		t.Fatalf("process count = %d; want 1", len(contract.Processes))
	}
	process := contract.Processes[0]
	if process.PID != 42 || process.StartTicks != 99 {
		t.Errorf("process identity = pid %d, start_ticks %d; want pid 42, start_ticks 99", process.PID, process.StartTicks)
	}
	if process.RSSBytes != 4096 || process.PSSBytes != 3072 || process.USSBytes != 2048 {
		t.Errorf("process memory = rss %d, pss %d, uss %d; want rss 4096, pss 3072, uss 2048", process.RSSBytes, process.PSSBytes, process.USSBytes)
	}
	if len(contract.Services) != 1 {
		t.Fatalf("service count = %d; want 1", len(contract.Services))
	}
	service := contract.Services[0]
	if service.RSSBytes != 8192 || service.PSSBytes != 6144 || service.USSBytes != 5120 {
		t.Errorf("service memory = rss %d, pss %d, uss %d; want rss 8192, pss 6144, uss 5120", service.RSSBytes, service.PSSBytes, service.USSBytes)
	}
}
