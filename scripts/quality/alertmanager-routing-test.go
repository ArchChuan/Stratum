//go:build ignore

package main

import (
	"fmt"
	"io"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type config struct {
	Route             route           `yaml:"route"`
	TimeIntervals     []namedInterval `yaml:"time_intervals"`
	MuteTimeIntervals yaml.Node       `yaml:"mute_time_intervals"`
	InhibitRules      []inhibitRule   `yaml:"inhibit_rules"`
}

type route struct {
	Receiver            string    `yaml:"receiver"`
	Matchers            []string  `yaml:"matchers"`
	ActiveTimeIntervals []string  `yaml:"active_time_intervals"`
	MuteTimeIntervals   []string  `yaml:"mute_time_intervals"`
	LegacyMatch         yaml.Node `yaml:"match"`
	LegacyMatchRE       yaml.Node `yaml:"match_re"`
	Continue            bool      `yaml:"continue"`
	Routes              []route   `yaml:"routes"`
}

type namedInterval struct {
	Name      string         `yaml:"name"`
	Intervals []timeInterval `yaml:"time_intervals"`
}

type timeInterval struct {
	Weekdays  []string    `yaml:"weekdays"`
	Times     []timeRange `yaml:"times"`
	Location  string      `yaml:"location"`
	Months    yaml.Node   `yaml:"months"`
	MonthDays yaml.Node   `yaml:"days_of_month"`
	Years     yaml.Node   `yaml:"years"`
}

type timeRange struct {
	Start string `yaml:"start_time"`
	End   string `yaml:"end_time"`
}

type inhibitRule struct {
	SourceMatchers []string `yaml:"source_matchers"`
	TargetMatchers []string `yaml:"target_matchers"`
	Equal          []string `yaml:"equal"`
}

type fixture struct {
	RoutingTests    []routingTest    `yaml:"routing_tests"`
	InhibitionTests []inhibitionTest `yaml:"inhibition_tests"`
}

type routingTest struct {
	Name     string            `yaml:"name"`
	Input    map[string]string `yaml:"input"`
	At       string            `yaml:"time"`
	Expected []string          `yaml:"expected"`
}

type inhibitionTest struct {
	Name     string            `yaml:"name"`
	Source   map[string]string `yaml:"source"`
	Target   map[string]string `yaml:"target"`
	Expected bool              `yaml:"expected"`
}

type kubernetesDocument struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Data map[string]string `yaml:"data"`
}

var matcherPattern = regexp.MustCompile(`^\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*(=~|!~|=|!=)\s*"(.*)"\s*$`)

func main() {
	if len(os.Args) == 4 && os.Args[1] == "extract-secret" {
		extractAlertmanagerSecret(os.Args[2], os.Args[3])
		return
	}
	if len(os.Args) == 4 && os.Args[1] == "compare" {
		compareYAML(os.Args[2], os.Args[3])
		return
	}
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr,
			"usage: alertmanager-routing-test.go CONFIG FIXTURE | compare LEFT RIGHT | extract-secret RENDER NAME")
		os.Exit(2)
	}
	var cfg config
	readYAML(os.Args[1], &cfg)
	if cfg.MuteTimeIntervals.Kind != 0 {
		failf("mute_time_intervals are outside the supported contract subset")
	}
	var tests fixture
	readYAML(os.Args[2], &tests)
	if len(tests.RoutingTests) == 0 || len(tests.InhibitionTests) == 0 {
		failf("fixture must contain routing_tests and inhibition_tests")
	}
	intervals := make(map[string][]timeInterval, len(cfg.TimeIntervals))
	for _, interval := range cfg.TimeIntervals {
		if interval.Name == "" || len(interval.Intervals) == 0 {
			failf("invalid empty time interval")
		}
		intervals[interval.Name] = interval.Intervals
		for _, definition := range interval.Intervals {
			if err := validateInterval(definition); err != nil {
				failf("time interval %q: %v", interval.Name, err)
			}
		}
	}
	if err := validateRoute(cfg.Route, intervals); err != nil {
		failf("route config: %v", err)
	}
	for index, rule := range cfg.InhibitRules {
		if err := validateMatchers(rule.SourceMatchers); err != nil {
			failf("inhibition rule %d source: %v", index, err)
		}
		if err := validateMatchers(rule.TargetMatchers); err != nil {
			failf("inhibition rule %d target: %v", index, err)
		}
	}
	for _, test := range tests.RoutingTests {
		at := time.Now()
		if test.At != "" {
			var err error
			at, err = time.Parse(time.RFC3339, test.At)
			if err != nil {
				failf("route contract %q has invalid time: %v", test.Name, err)
			}
		}
		actual, err := resolveRoute(cfg.Route, test.Input, at, intervals)
		if err != nil {
			failf("route contract %q: %v", test.Name, err)
		}
		if !slices.Equal(actual, test.Expected) {
			failf("route contract %q: expected %v, got %v", test.Name, test.Expected, actual)
		}
	}
	for _, test := range tests.InhibitionTests {
		actual, err := isInhibited(cfg.InhibitRules, test.Source, test.Target)
		if err != nil {
			failf("inhibition contract %q: %v", test.Name, err)
		}
		if actual != test.Expected {
			failf("inhibition contract %q: expected %t, got %t", test.Name, test.Expected, actual)
		}
	}
}

func extractAlertmanagerSecret(renderPath, expectedName string) {
	var reader io.Reader = os.Stdin
	if renderPath != "-" {
		file, err := os.Open(renderPath)
		if err != nil {
			failf("open rendered chart: %v", err)
		}
		defer file.Close()
		reader = file
	}

	decoder := yaml.NewDecoder(reader)
	var payloads []string
	for {
		var document kubernetesDocument
		err := decoder.Decode(&document)
		if err == io.EOF {
			break
		}
		if err != nil {
			failf("parse rendered chart: %v", err)
		}
		if document.Kind != "Secret" || document.Metadata.Name != expectedName {
			continue
		}
		payload := document.Data["alertmanager.yaml"]
		if payload == "" {
			failf("rendered Alertmanager Secret has empty alertmanager.yaml")
		}
		payloads = append(payloads, payload)
	}
	if len(payloads) != 1 {
		failf("expected exactly one rendered Alertmanager Secret %q, found %d", expectedName, len(payloads))
	}
	fmt.Print(payloads[0])
}

func compareYAML(leftPath, rightPath string) {
	var left, right any
	readYAML(leftPath, &left)
	readYAML(rightPath, &right)
	if !reflect.DeepEqual(left, right) {
		failf("Alertmanager chart config differs from standalone config")
	}
}

func readYAML(path string, target any) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		failf("read %s: %v", path, err)
	}
	if err := yaml.Unmarshal(data, target); err != nil {
		failf("parse %s: %v", path, err)
	}
}

func resolveRoute(current route, labels map[string]string, at time.Time,
	intervals map[string][]timeInterval) ([]string, error) {
	matchedChild := false
	var receivers []string
	for _, child := range current.Routes {
		matches, err := routeMatches(child, labels, at, intervals)
		if err != nil {
			return nil, err
		}
		if !matches {
			continue
		}
		matchedChild = true
		resolved, err := resolveRoute(child, labels, at, intervals)
		if err != nil {
			return nil, err
		}
		receivers = append(receivers, resolved...)
		if !child.Continue {
			break
		}
	}
	if matchedChild {
		return receivers, nil
	}
	if current.Receiver == "" {
		return nil, fmt.Errorf("matched route has no receiver")
	}
	return []string{current.Receiver}, nil
}

func routeMatches(candidate route, labels map[string]string, at time.Time,
	intervals map[string][]timeInterval) (bool, error) {
	matches, err := matchAll(candidate.Matchers, labels)
	if err != nil || !matches {
		return matches, err
	}
	if len(candidate.ActiveTimeIntervals) == 0 {
		return true, nil
	}
	for _, name := range candidate.ActiveTimeIntervals {
		definitions, ok := intervals[name]
		if !ok {
			return false, fmt.Errorf("route references unknown time interval %q", name)
		}
		for _, definition := range definitions {
			active, err := intervalContains(definition, at)
			if err != nil {
				return false, err
			}
			if active {
				return true, nil
			}
		}
	}
	return false, nil
}

func validateRoute(candidate route, intervals map[string][]timeInterval) error {
	if candidate.LegacyMatch.Kind != 0 || candidate.LegacyMatchRE.Kind != 0 {
		return fmt.Errorf("legacy match/match_re routes are unsupported")
	}
	if len(candidate.MuteTimeIntervals) > 0 {
		return fmt.Errorf("mute_time_intervals routes are unsupported")
	}
	if err := validateMatchers(candidate.Matchers); err != nil {
		return err
	}
	for _, name := range candidate.ActiveTimeIntervals {
		if _, ok := intervals[name]; !ok {
			return fmt.Errorf("route references unknown time interval %q", name)
		}
	}
	for _, child := range candidate.Routes {
		if err := validateRoute(child, intervals); err != nil {
			return err
		}
	}
	return nil
}

func validateInterval(interval timeInterval) error {
	if interval.Months.Kind != 0 || interval.MonthDays.Kind != 0 || interval.Years.Kind != 0 {
		return fmt.Errorf("calendar fields months/days_of_month/years are unsupported")
	}
	if interval.Location == "" {
		return fmt.Errorf("location is required")
	}
	if _, err := time.LoadLocation(interval.Location); err != nil {
		return fmt.Errorf("load location %q: %w", interval.Location, err)
	}
	for _, value := range interval.Weekdays {
		parts := strings.Split(value, ":")
		if len(parts) > 2 {
			return fmt.Errorf("invalid weekday range %q", value)
		}
		for _, part := range parts {
			if _, ok := parseWeekday(part); !ok {
				return fmt.Errorf("invalid weekday %q", part)
			}
		}
	}
	for _, candidate := range interval.Times {
		start, err := parseClock(candidate.Start)
		if err != nil {
			return err
		}
		end, err := parseClock(candidate.End)
		if err != nil {
			return err
		}
		if end <= start {
			return fmt.Errorf("overnight or empty range %q-%q is unsupported", candidate.Start, candidate.End)
		}
	}
	return nil
}

func validateMatchers(matchers []string) error {
	for _, raw := range matchers {
		parts := matcherPattern.FindStringSubmatch(raw)
		if parts == nil {
			return fmt.Errorf("unsupported matcher %q", raw)
		}
		if parts[2] == "=~" || parts[2] == "!~" {
			if _, err := regexp.Compile("^(?:" + parts[3] + ")$"); err != nil {
				return fmt.Errorf("compile matcher %q: %w", raw, err)
			}
		}
	}
	return nil
}

func intervalContains(interval timeInterval, at time.Time) (bool, error) {
	location, err := time.LoadLocation(interval.Location)
	if err != nil {
		return false, fmt.Errorf("load interval location %q: %w", interval.Location, err)
	}
	local := at.In(location)
	if len(interval.Weekdays) > 0 && !weekdayMatches(interval.Weekdays, local.Weekday()) {
		return false, nil
	}
	if len(interval.Times) == 0 {
		return true, nil
	}
	seconds := local.Hour()*3600 + local.Minute()*60 + local.Second()
	for _, candidate := range interval.Times {
		start, err := parseClock(candidate.Start)
		if err != nil {
			return false, err
		}
		end, err := parseClock(candidate.End)
		if err != nil {
			return false, err
		}
		if seconds >= start && seconds < end {
			return true, nil
		}
	}
	return false, nil
}

func weekdayMatches(ranges []string, weekday time.Weekday) bool {
	for _, value := range ranges {
		parts := strings.Split(value, ":")
		start, ok := parseWeekday(parts[0])
		if !ok {
			continue
		}
		end := start
		if len(parts) == 2 {
			end, ok = parseWeekday(parts[1])
			if !ok {
				continue
			}
		}
		if start <= end && weekday >= start && weekday <= end {
			return true
		}
		if start > end && (weekday >= start || weekday <= end) {
			return true
		}
	}
	return false
}

func parseWeekday(value string) (time.Weekday, bool) {
	weekdays := map[string]time.Weekday{
		"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday,
		"wednesday": time.Wednesday, "thursday": time.Thursday, "friday": time.Friday,
		"saturday": time.Saturday,
	}
	weekday, ok := weekdays[strings.ToLower(value)]
	return weekday, ok
}

func parseClock(value string) (int, error) {
	parsed, err := time.Parse("15:04", value)
	if err != nil {
		return 0, fmt.Errorf("parse interval time %q: %w", value, err)
	}
	return parsed.Hour()*3600 + parsed.Minute()*60, nil
}

func isInhibited(rules []inhibitRule, source, target map[string]string) (bool, error) {
	for _, rule := range rules {
		sourceMatches, err := matchAll(rule.SourceMatchers, source)
		if err != nil {
			return false, err
		}
		targetMatches, err := matchAll(rule.TargetMatchers, target)
		if err != nil {
			return false, err
		}
		if !sourceMatches || !targetMatches {
			continue
		}
		sourceIsTarget, err := matchAll(rule.TargetMatchers, source)
		if err != nil {
			return false, err
		}
		targetIsSource, err := matchAll(rule.SourceMatchers, target)
		if err != nil {
			return false, err
		}
		if sourceIsTarget && targetIsSource {
			continue
		}
		equal := true
		for _, label := range rule.Equal {
			if source[label] != target[label] {
				equal = false
				break
			}
		}
		if equal {
			return true, nil
		}
	}
	return false, nil
}

func matchAll(matchers []string, labels map[string]string) (bool, error) {
	for _, raw := range matchers {
		parts := matcherPattern.FindStringSubmatch(raw)
		if parts == nil {
			return false, fmt.Errorf("unsupported matcher %q", raw)
		}
		actual := labels[parts[1]]
		switch parts[2] {
		case "=":
			if actual != parts[3] {
				return false, nil
			}
		case "!=":
			if actual == parts[3] {
				return false, nil
			}
		case "=~", "!~":
			expression, err := regexp.Compile("^(?:" + parts[3] + ")$")
			if err != nil {
				return false, fmt.Errorf("compile matcher %q: %w", raw, err)
			}
			matched := expression.MatchString(actual)
			if (parts[2] == "=~" && !matched) || (parts[2] == "!~" && matched) {
				return false, nil
			}
		}
	}
	return true, nil
}

func failf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
