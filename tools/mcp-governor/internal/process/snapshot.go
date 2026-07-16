package process

import (
	"sort"
	"time"
)

const SnapshotVersion = 1

func BuildSnapshot(capturedAt time.Time, processes []Process, registrations []Registration, live map[Identity]bool, warnings []string) Snapshot {
	registrationByIdentity := make(map[Identity]Registration, len(registrations))
	for _, registration := range registrations {
		if _, exists := registrationByIdentity[registration.Identity]; !exists {
			registrationByIdentity[registration.Identity] = registration
		}
	}

	reported := make([]Process, 0, len(processes))
	for _, input := range processes {
		if input.Service == "" {
			continue
		}
		process := input
		process.Args = append([]string(nil), input.Args...)
		process.Registered = false
		process.Orphan = false
		if registration, exists := registrationByIdentity[process.Identity]; exists {
			process.Registered = true
			process.Orphan = !live[registration.Client]
		}
		reported = append(reported, process)
	}
	sort.Slice(reported, func(i, j int) bool {
		if reported[i].Service != reported[j].Service {
			return reported[i].Service < reported[j].Service
		}
		if reported[i].PID != reported[j].PID {
			return reported[i].PID < reported[j].PID
		}
		return reported[i].StartTicks < reported[j].StartTicks
	})

	summaryByService := make(map[string]ServiceSummary)
	for _, process := range reported {
		summary := summaryByService[process.Service]
		summary.Service = process.Service
		summary.Processes++
		summary.RSSBytes += process.RSSBytes
		summary.PSSBytes += process.PSSBytes
		summary.USSBytes += process.USSBytes
		if process.Orphan {
			summary.Orphans++
		}
		summaryByService[process.Service] = summary
	}
	services := make([]ServiceSummary, 0, len(summaryByService))
	for _, summary := range summaryByService {
		services = append(services, summary)
	}
	sort.Slice(services, func(i, j int) bool { return services[i].Service < services[j].Service })

	sortedWarnings := append([]string(nil), warnings...)
	sort.Strings(sortedWarnings)
	return Snapshot{
		Version:    SnapshotVersion,
		CapturedAt: capturedAt,
		Mode:       "observe",
		Processes:  reported,
		Services:   services,
		Warnings:   sortedWarnings,
	}
}
