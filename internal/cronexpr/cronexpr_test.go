package cronexpr

import (
	"testing"
	"time"
)

func TestNextBasicAndStrictlyAfter(t *testing.T) {
	expr := mustParse(t, "* * * * * *")
	location := time.FixedZone("test", 5*60*60+30*60)
	input := time.Date(2026, time.July, 24, 12, 34, 56, 789, location)
	want := time.Date(2026, time.July, 24, 12, 34, 57, 0, location)
	assertNext(t, expr, input, want)

	expr = mustParse(t, "56 * * * * *")
	input = time.Date(2026, time.July, 24, 12, 34, 56, 0, location)
	want = time.Date(2026, time.July, 24, 12, 35, 56, 0, location)
	assertNext(t, expr, input, want)
}

func TestNextListsRangesStepsAndNames(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		input      time.Time
		want       time.Time
	}{
		{
			name:       "lists and ranges",
			expression: "10,20-24/2 5,35 9-10 * JAN,MAR MON-FRI",
			input:      utc(2026, time.January, 2, 9, 5, 10),
			want:       utc(2026, time.January, 2, 9, 5, 20),
		},
		{
			name:       "wildcard step",
			expression: "*/15 */20 * * * *",
			input:      utc(2026, time.July, 24, 12, 19, 59),
			want:       utc(2026, time.July, 24, 12, 20, 0),
		},
		{
			name:       "single value step extends to maximum",
			expression: "0 0 0 1 1/3 ?",
			input:      utc(2026, time.January, 1, 0, 0, 0),
			want:       utc(2026, time.April, 1, 0, 0, 0),
		},
		{
			name:       "case insensitive names",
			expression: "0 0 7 ? feb mon-fri",
			input:      utc(2026, time.January, 31, 23, 0, 0),
			want:       utc(2026, time.February, 2, 7, 0, 0),
		},
		{
			name:       "sunday seven",
			expression: "0 0 0 ? * 7",
			input:      utc(2026, time.July, 24, 0, 0, 0),
			want:       utc(2026, time.July, 26, 0, 0, 0),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertNext(t, mustParse(t, test.expression), test.input, test.want)
		})
	}
}

func TestDayOfMonthAndDayOfWeekUseAND(t *testing.T) {
	expr := mustParse(t, "0 0 0 13 * FRI")
	input := utc(2026, time.January, 1, 0, 0, 0)
	want := utc(2026, time.February, 13, 0, 0, 0)
	assertNext(t, expr, input, want)
}

func TestQuestionMarkIsDayWildcard(t *testing.T) {
	expr := mustParse(t, "0 0 7 ? * MON-FRI")
	input := utc(2026, time.July, 24, 7, 0, 0)
	want := utc(2026, time.July, 27, 7, 0, 0)
	assertNext(t, expr, input, want)
}

func TestNextSkipsNonexistentDSTTime(t *testing.T) {
	location := loadLocation(t, "America/New_York")
	expr := mustParse(t, "0 30 2 * * *")
	input := time.Date(2024, time.March, 10, 0, 0, 0, 0, location)
	want := time.Date(2024, time.March, 11, 2, 30, 0, 0, location)
	assertNext(t, expr, input, want)
}

func TestNextReturnsOnlyFirstInstantOfRepeatedDSTTime(t *testing.T) {
	location := loadLocation(t, "America/New_York")
	expr := mustParse(t, "0 30 1 * * *")
	input := time.Date(2024, time.November, 3, 0, 0, 0, 0, location)
	first := time.Date(2024, time.November, 3, 5, 30, 0, 0, time.UTC).In(location)
	assertNext(t, expr, input, first)

	want := time.Date(2024, time.November, 4, 1, 30, 0, 0, location)
	assertNext(t, expr, first, want)

	betweenOccurrences := time.Date(2024, time.November, 3, 6, 0, 0, 0, time.UTC).In(location)
	assertNext(t, expr, betweenOccurrences, want)
}

func TestNextRejectsImpossibleExpressionAfterGregorianCycle(t *testing.T) {
	expr := mustParse(t, "0 0 0 30 FEB *")
	if _, err := expr.Next(utc(2000, time.January, 1, 0, 0, 0)); err == nil {
		t.Fatal("Next succeeded for an impossible expression")
	}
}

func TestNextCoversLeapYears(t *testing.T) {
	expr := mustParse(t, "0 0 0 29 FEB *")
	assertNext(
		t,
		expr,
		utc(2096, time.February, 29, 0, 0, 0),
		utc(2104, time.February, 29, 0, 0, 0),
	)
}

func TestParseAcceptsSupportedForms(t *testing.T) {
	for _, value := range []string{
		"* * * * * *",
		"*/2 1/3 4-20/4 1,15 JAN-DEC/2 SUN-SAT",
		"0 0 0 ? * ?",
		"0 0 0 * * 0,7",
		"0 0 0 * * SUN",
	} {
		t.Run(value, func(t *testing.T) {
			if _, err := Parse(value); err != nil {
				t.Fatalf("Parse(%q) failed: %v", value, err)
			}
		})
	}
}

func TestParseRejectsInvalidExpressions(t *testing.T) {
	for _, value := range []string{
		"",
		"* * * * *",
		"* * * * * * *",
		"*/0 * * * * *",
		"*/-1 * * * * *",
		"*/2/3 * * * * *",
		"* * * * */999999999999999999999999999999999999999999999999999 *",
		"10-5 * * * * *",
		"* * * * DEC-JAN *",
		"60 * * * * *",
		"* 60 * * * *",
		"* * 24 * * *",
		"* * * 0 * *",
		"* * * 32 * *",
		"* * * * 0 *",
		"* * * * 13 *",
		"* * * * * 8",
		",1 * * * * *",
		"1,,2 * * * * *",
		"1, * * * * *",
		"? * * * * *",
		"* * * ?,1 * *",
		"* * * L * *",
		"* * * 1W * *",
		"* * * * * MON#2",
		"* * * * FOO *",
		"* * * * * MONDAY",
		"1- * * * * *",
		"*/ * * * * *",
	} {
		t.Run(value, func(t *testing.T) {
			if _, err := Parse(value); err == nil {
				t.Fatalf("Parse(%q) succeeded, want error", value)
			}
		})
	}
}

func TestNilExpression(t *testing.T) {
	var expr *Expression
	if _, err := expr.Next(time.Now()); err == nil {
		t.Fatal("Next on nil expression succeeded")
	}
}

func mustParse(t *testing.T, value string) *Expression {
	t.Helper()
	expr, err := Parse(value)
	if err != nil {
		t.Fatalf("Parse(%q): %v", value, err)
	}
	return expr
}

func assertNext(t *testing.T, expr *Expression, input, want time.Time) {
	t.Helper()
	got, err := expr.Next(input)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(want) {
		t.Fatalf("Next(%s) = %s, want %s", input.Format(time.RFC3339Nano), got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
	if got.Location() != input.Location() {
		t.Fatalf("Next returned location %q, want %q", got.Location(), input.Location())
	}
}

func utc(year int, month time.Month, day, hour, minute, second int) time.Time {
	return time.Date(year, month, day, hour, minute, second, 0, time.UTC)
}

func loadLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	location, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", name, err)
	}
	return location
}
