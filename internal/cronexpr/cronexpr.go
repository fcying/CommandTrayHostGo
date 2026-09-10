package cronexpr

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const daysInGregorianCycle = 146097

var (
	monthNames = map[string]int{
		"JAN": 1, "FEB": 2, "MAR": 3, "APR": 4, "MAY": 5, "JUN": 6,
		"JUL": 7, "AUG": 8, "SEP": 9, "OCT": 10, "NOV": 11, "DEC": 12,
	}
	weekdayNames = map[string]int{
		"SUN": 0, "MON": 1, "TUE": 2, "WED": 3,
		"THU": 4, "FRI": 5, "SAT": 6,
	}
)

// Expression is a parsed six-field cron expression.
type Expression struct {
	seconds  []int
	minutes  []int
	hours    []int
	days     [32]bool
	months   [13]bool
	weekdays [7]bool
}

type fieldSpec struct {
	name         string
	min          int
	max          int
	names        map[string]int
	questionMark bool
	normalize    func(int) int
}

// Parse parses second, minute, hour, day-of-month, month, and day-of-week.
func Parse(value string) (*Expression, error) {
	fields := strings.Fields(value)
	if len(fields) != 6 {
		return nil, fmt.Errorf("cron expression must contain 6 fields, got %d", len(fields))
	}

	specs := []fieldSpec{
		{name: "second", min: 0, max: 59},
		{name: "minute", min: 0, max: 59},
		{name: "hour", min: 0, max: 23},
		{name: "day-of-month", min: 1, max: 31, questionMark: true},
		{name: "month", min: 1, max: 12, names: monthNames},
		{
			name: "day-of-week", min: 0, max: 7, names: weekdayNames, questionMark: true,
			normalize: func(value int) int {
				if value == 7 {
					return 0
				}
				return value
			},
		},
	}

	sets := make([][]bool, len(fields))
	for i := range fields {
		set, err := parseField(fields[i], specs[i])
		if err != nil {
			return nil, err
		}
		sets[i] = set
	}

	expr := &Expression{
		seconds: selectedValues(sets[0]),
		minutes: selectedValues(sets[1]),
		hours:   selectedValues(sets[2]),
	}
	for day := 1; day <= 31; day++ {
		expr.days[day] = sets[3][day]
	}
	for month := 1; month <= 12; month++ {
		expr.months[month] = sets[4][month]
	}
	for weekday := 0; weekday <= 6; weekday++ {
		expr.weekdays[weekday] = sets[5][weekday]
	}
	return expr, nil
}

// Next returns the first matching instant strictly after input.
func (e *Expression) Next(input time.Time) (time.Time, error) {
	if e == nil {
		return time.Time{}, errors.New("nil cron expression")
	}

	location := input.Location()
	local := input.In(location)
	firstDate := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	inputWallSecond := local.Hour()*3600 + local.Minute()*60 + local.Second()

	for dayOffset := 0; dayOffset <= daysInGregorianCycle; dayOffset++ {
		date := firstDate.AddDate(0, 0, dayOffset)
		if !e.matchesDate(date) {
			continue
		}

		minimumWallSecond := -1
		if dayOffset == 0 {
			minimumWallSecond = inputWallSecond
		}
		for _, hour := range e.hours {
			for _, minute := range e.minutes {
				for _, second := range e.seconds {
					wallSecond := hour*3600 + minute*60 + second
					if wallSecond <= minimumWallSecond {
						continue
					}
					candidate, ok := firstLocalInstant(
						date.Year(), date.Month(), date.Day(), hour, minute, second, location,
					)
					if ok && candidate.After(input) {
						return candidate, nil
					}
				}
			}
		}
	}

	return time.Time{}, errors.New("cron expression has no matching time within 400 years")
}

func (e *Expression) matchesDate(date time.Time) bool {
	return e.months[date.Month()] && e.days[date.Day()] && e.weekdays[date.Weekday()]
}

func parseField(value string, spec fieldSpec) ([]bool, error) {
	set := make([]bool, spec.max+1)
	if value == "?" {
		if !spec.questionMark {
			return nil, fmt.Errorf("%s field does not support ?", spec.name)
		}
		value = "*"
	} else if strings.Contains(value, "?") {
		return nil, fmt.Errorf("%s field must use ? by itself", spec.name)
	}

	parts := strings.Split(value, ",")
	for _, part := range parts {
		if part == "" {
			return nil, fmt.Errorf("%s field contains an empty list item", spec.name)
		}
		if err := addFieldPart(set, part, spec); err != nil {
			return nil, err
		}
	}

	for _, selected := range set {
		if selected {
			return set, nil
		}
	}
	return nil, fmt.Errorf("%s field selects no values", spec.name)
}

func addFieldPart(set []bool, part string, spec fieldSpec) error {
	if strings.Count(part, "/") > 1 {
		return fmt.Errorf("invalid %s field item %q", spec.name, part)
	}

	base := part
	step := 1
	if slash := strings.IndexByte(part, '/'); slash >= 0 {
		base = part[:slash]
		var err error
		step, err = parsePositiveInteger(part[slash+1:])
		if err != nil {
			return fmt.Errorf("invalid %s step in %q: %w", spec.name, part, err)
		}
	}

	start, end, err := parseRange(base, spec, strings.Contains(part, "/"))
	if err != nil {
		return err
	}
	for current := start; ; current += step {
		value := current
		if spec.normalize != nil {
			value = spec.normalize(value)
		}
		set[value] = true
		if step > end-current {
			break
		}
	}
	return nil
}

func parseRange(value string, spec fieldSpec, stepped bool) (int, int, error) {
	if value == "*" {
		return spec.min, spec.max, nil
	}
	if strings.Count(value, "-") > 1 {
		return 0, 0, fmt.Errorf("invalid %s range %q", spec.name, value)
	}
	if dash := strings.IndexByte(value, '-'); dash >= 0 {
		start, err := parseFieldValue(value[:dash], spec)
		if err != nil {
			return 0, 0, err
		}
		end, err := parseFieldValue(value[dash+1:], spec)
		if err != nil {
			return 0, 0, err
		}
		if start > end {
			return 0, 0, fmt.Errorf("%s range %q is reversed", spec.name, value)
		}
		return start, end, nil
	}

	start, err := parseFieldValue(value, spec)
	if err != nil {
		return 0, 0, err
	}
	if stepped {
		return start, spec.max, nil
	}
	return start, start, nil
}

func parseFieldValue(value string, spec fieldSpec) (int, error) {
	upper := strings.ToUpper(value)
	if named, ok := spec.names[upper]; ok {
		return named, nil
	}
	if value == "" {
		return 0, fmt.Errorf("missing %s value", spec.name)
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, fmt.Errorf("invalid %s value %q", spec.name, value)
		}
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < spec.min || number > spec.max {
		return 0, fmt.Errorf("%s value %q is outside %d-%d", spec.name, value, spec.min, spec.max)
	}
	return number, nil
}

func parsePositiveInteger(value string) (int, error) {
	if value == "" {
		return 0, errors.New("missing step")
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, errors.New("step must be a positive integer")
		}
	}
	number, err := strconv.Atoi(value)
	if err != nil || number <= 0 {
		return 0, errors.New("step must be a positive integer")
	}
	return number, nil
}

func selectedValues(set []bool) []int {
	values := make([]int, 0, len(set))
	for value, selected := range set {
		if selected {
			values = append(values, value)
		}
	}
	return values
}

// firstLocalInstant rejects DST gaps and canonicalizes repeated wall times to
// the earlier instant.
func firstLocalInstant(year int, month time.Month, day, hour, minute, second int, location *time.Location) (time.Time, bool) {
	nominal := time.Date(year, month, day, hour, minute, second, 0, location)
	offsets := make(map[int]struct{})
	windowStart := nominal.Add(-72 * time.Hour)
	windowEnd := nominal.Add(72 * time.Hour)
	for current := windowStart; !current.After(windowEnd); {
		_, offset := current.Zone()
		offsets[offset] = struct{}{}
		_, end := current.ZoneBounds()
		if end.IsZero() || end.After(windowEnd) || !end.After(current) {
			break
		}
		current = end
	}

	wallUnix := time.Date(year, month, day, hour, minute, second, 0, time.UTC).Unix()
	var first time.Time
	for offset := range offsets {
		candidate := time.Unix(wallUnix-int64(offset), 0).In(location)
		if candidate.Year() != year || candidate.Month() != month || candidate.Day() != day ||
			candidate.Hour() != hour || candidate.Minute() != minute || candidate.Second() != second {
			continue
		}
		if first.IsZero() || candidate.Before(first) {
			first = candidate
		}
	}
	return first, !first.IsZero()
}
