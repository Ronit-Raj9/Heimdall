package schedule

import (
	"strings"
	"testing"
	"time"
)

func TestParseCronErrors(t *testing.T) {
	tests := []struct {
		spec, want string
	}{
		{"* * * *", "want 5 fields"},
		{"* * * * * *", "want 5 fields"},
		{"60 * * * *", "minute: 60 is outside 0-59"},
		{"* 24 * * *", "hour: 24 is outside 0-23"},
		{"* * 0 * *", "day of month: 0 is outside 1-31"},
		{"* * * 13 *", "month: 13 is outside 1-12"},
		{"* * * * 8", "day of week: 8 is outside 0-7"},
		{"*/0 * * * *", "step \"0\" is not a positive integer"},
		{"*/x * * * *", "is not a positive integer"},
		{"5-1 * * * *", "runs backwards"},
		{"1,,2 * * * *", "empty list item"},
		{"a * * * *", "\"a\" is not a number"},
		{"* * * foo *", "\"foo\" is not a number"},
		{"1- * * * *", "empty value"},
	}
	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			_, err := ParseCron(tt.spec)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ParseCron(%q) = %v, want %q", tt.spec, err, tt.want)
			}
		})
	}
}

func TestCronNext(t *testing.T) {
	utc := func(y int, mo time.Month, d, h, mi int) time.Time { return time.Date(y, mo, d, h, mi, 0, 0, time.UTC) }
	// 2026-11-15 is a Sunday.
	from := time.Date(2026, 11, 15, 10, 7, 30, 0, time.UTC)
	tests := []struct {
		spec string
		from time.Time
		want time.Time
	}{
		{"* * * * *", from, utc(2026, 11, 15, 10, 8)},
		{"*/15 * * * *", from, utc(2026, 11, 15, 10, 15)},
		{"0 * * * *", from, utc(2026, 11, 15, 11, 0)},
		{"30 2 * * *", from, utc(2026, 11, 16, 2, 30)},
		{"0 9-17/4 * * *", from, utc(2026, 11, 15, 13, 0)},
		{"5,10 10 * * *", from, utc(2026, 11, 15, 10, 10)},
		{"0 0 1 * *", from, utc(2026, 12, 1, 0, 0)},
		{"0 0 1 jan *", from, utc(2027, 1, 1, 0, 0)},
		{"0 6 * * mon-fri", from, utc(2026, 11, 16, 6, 0)},
		{"0 6 * * 7", from, utc(2026, 11, 22, 6, 0)},   // 7 is Sunday; today's 06:00 has passed
		{"0 12 * * 0", from, utc(2026, 11, 15, 12, 0)}, // Sunday, later today
		{"0 0 13 * 5", from, utc(2026, 11, 20, 0, 0)},  // day 13 OR Friday: Friday the 20th comes first
		{"0 0 */10 * *", from, utc(2026, 11, 21, 0, 0)},
		{"0 0 29 2 *", from, utc(2028, 2, 29, 0, 0)},
		{"10/20 * * * *", from, utc(2026, 11, 15, 10, 10)},
		{"0 0 30 2 *", from, time.Time{}},
		{"7 10 * * *", from, utc(2026, 11, 16, 10, 7)}, // strictly after: the current minute does not count
	}
	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			c, err := ParseCron(tt.spec)
			if err != nil {
				t.Fatal(err)
			}
			if got := c.Next(tt.from); !got.Equal(tt.want) {
				t.Fatalf("Next(%s) = %s, want %s", tt.from, got, tt.want)
			}
		})
	}
}

func TestCronNextDaylightSaving(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("no tz database: %v", err)
	}
	c, err := ParseCron("30 2 * * *")
	if err != nil {
		t.Fatal(err)
	}
	// 2026-03-08 02:30 does not exist in New York; the next match is a day later.
	got := c.Next(time.Date(2026, 3, 8, 0, 0, 0, 0, ny))
	if want := time.Date(2026, 3, 9, 2, 30, 0, 0, ny); !got.Equal(want) {
		t.Fatalf("spring forward: Next = %s, want %s", got, want)
	}
	hourly, err := ParseCron("0 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	// Fall back on 2026-11-01: times strictly increase through the repeated hour.
	prev := time.Date(2026, 11, 1, 0, 0, 0, 0, ny)
	for range 5 {
		next := hourly.Next(prev)
		if !next.After(prev) {
			t.Fatalf("Next(%s) = %s does not move forward", prev, next)
		}
		prev = next
	}
}

func TestCronString(t *testing.T) {
	c, err := ParseCron("  0   6 * *  1-5 ")
	if err != nil {
		t.Fatal(err)
	}
	if c.String() != "0 6 * * 1-5" {
		t.Fatalf("String = %q", c.String())
	}
}
