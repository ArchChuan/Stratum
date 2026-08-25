package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"
)

// options is the parsed command-line configuration shared by every kind.
type options struct {
	kind           string
	point          string
	output         string
	warnDelta      float64
	failOnWarn     bool
	recordBaseline bool
	confirmRecord  bool
	skip           string
	baseURL        string
	tenantID       string
	userID         string
	provider       string
	selfCheck      bool
}

// parseOptions parses and validates the CLI flags. Fail-closed: required flags
// and gate invariants are enforced here, before any network or dataset work.
func parseOptions(args []string) (options, error) {
	fs := flag.NewFlagSet("e2e-eval-check", flag.ContinueOnError)
	var o options
	fs.StringVar(&o.kind, "kind", "", "resource kind: skill|agent|mcp|knowledge")
	fs.StringVar(&o.point, "point", "", "point key under test/e2e/<kind>/points/<key>.yaml")
	fs.StringVar(&o.output, "output", "", "report JSON output path (default none)")
	fs.Float64Var(&o.warnDelta, "warn-delta", DefaultWarnDelta, "regression delta threshold in [0,1]")
	fs.BoolVar(&o.failOnWarn, "fail-on-warn", false, "exit 1 on any strong warning (CI gate)")
	fs.BoolVar(&o.recordBaseline, "record-baseline", false, "record this run as the new baseline")
	fs.BoolVar(&o.confirmRecord, "confirm-record", false, "explicit confirmation for baseline recording")
	fs.StringVar(&o.skip, "skip", "", "skip this point; reason required")
	fs.StringVar(&o.baseURL, "base-url", "", "Stratum server base URL")
	fs.StringVar(&o.tenantID, "tenant-id", "", "test tenant id")
	fs.StringVar(&o.userID, "user-id", "", "test user id")
	fs.StringVar(&o.provider, "provider", "", "embedding/provider declaration for drift detection")
	fs.BoolVar(&o.selfCheck, "self-check", false, "validate points/golden wiring without any network")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if o.selfCheck {
		// self-check needs neither kind nor point: it scans every kind's
		// points directory (or just --kind when given).
		return o, nil
	}
	if strings.TrimSpace(o.kind) == "" {
		return o, errors.New("--kind is required")
	}
	switch o.kind {
	case "skill", "agent", "mcp", "knowledge":
	default:
		return o, fmt.Errorf("unsupported kind %q (want skill|agent|mcp|knowledge)", o.kind)
	}
	if strings.TrimSpace(o.point) == "" {
		return o, errors.New("--point is required")
	}
	if o.warnDelta < 0 || o.warnDelta > 1 {
		return o, fmt.Errorf("--warn-delta must be in [0,1], got %g", o.warnDelta)
	}
	if o.recordBaseline && !o.confirmRecord {
		return o, errors.New("--record-baseline requires --confirm-record")
	}
	return o, nil
}
