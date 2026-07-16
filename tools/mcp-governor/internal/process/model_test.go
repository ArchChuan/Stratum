package process

import (
	"encoding/json"
	"strings"
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
				PSSBytes: 4096,
			},
		},
	}

	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}

	jsonText := string(data)
	for _, field := range []string{
		`"version":1`,
		`"mode":"observe"`,
		`"pid":42`,
		`"start_ticks":99`,
		`"pss_bytes":4096`,
	} {
		if !strings.Contains(jsonText, field) {
			t.Errorf("snapshot JSON %s does not contain %s", jsonText, field)
		}
	}
}
