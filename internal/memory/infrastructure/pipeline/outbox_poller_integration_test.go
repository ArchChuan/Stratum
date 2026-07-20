package pipeline

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

func TestPollTenantQuarantinesMalformedPayloadWithRealPostgres(t *testing.T) {
	pgURL := os.Getenv("STRATUM_TEST_POSTGRES_URL")
	if pgURL == "" {
		t.Skip("STRATUM_TEST_POSTGRES_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, pgURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	schema := fmt.Sprintf("tmp_outbox_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE")
	})
	_, err = pool.Exec(ctx, "CREATE TABLE "+identifier+`.memory_outbox (
		id BIGSERIAL PRIMARY KEY, payload JSONB NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, "CREATE TABLE "+identifier+`.memory_outbox_quarantine (
		outbox_id BIGINT PRIMARY KEY, payload_hash TEXT NOT NULL,
		error_class TEXT NOT NULL, quarantined_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO "+identifier+".memory_outbox(payload) VALUES ($1::jsonb)", `"broken-event"`); err != nil {
		t.Fatal(err)
	}

	poller := NewOutboxPoller(pool, nil, zap.NewNop(), Config{BatchSize: 10})
	if err := poller.pollTenant(ctx, schema); err != nil {
		t.Fatalf("pollTenant failed against real pgx transaction: %v", err)
	}
	var outboxCount, quarantineCount int
	err = pool.QueryRow(ctx, "SELECT count(*) FROM "+identifier+".memory_outbox").Scan(&outboxCount)
	if err != nil {
		t.Fatal(err)
	}
	err = pool.QueryRow(ctx, "SELECT count(*) FROM "+identifier+".memory_outbox_quarantine").Scan(&quarantineCount)
	if err != nil {
		t.Fatal(err)
	}
	if outboxCount != 0 || quarantineCount != 1 {
		t.Fatalf("outbox=%d quarantine=%d, want 0/1", outboxCount, quarantineCount)
	}
}
