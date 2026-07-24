package fileee

import (
	"math/rand"
	"net/http"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestExponentialBackoffShouldRetry(t *testing.T) {
	b := &ExponentialBackoff{MaxAttempts: 3}
	cases := []struct {
		name    string
		attempt int
		resp    *http.Response
		err     error
		want    bool
	}{
		{"network error unter MaxAttempts", 0, nil, context_DeadlineExceededForTest(), true},
		{"429 unter MaxAttempts", 0, &http.Response{StatusCode: 429}, nil, true},
		{"500 unter MaxAttempts", 1, &http.Response{StatusCode: 500}, nil, true},
		{"200 kein Retry", 0, &http.Response{StatusCode: 200}, nil, false},
		{"404 kein Retry", 0, &http.Response{StatusCode: 404}, nil, false},
		{"MaxAttempts erreicht -> kein Retry mehr", 3, &http.Response{StatusCode: 500}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := b.ShouldRetry(tc.attempt, tc.resp, tc.err)
			if got != tc.want {
				t.Errorf("ShouldRetry(%d, %v, %v) = %v, erwartet %v", tc.attempt, tc.resp, tc.err, got, tc.want)
			}
		})
	}
}

func TestExponentialBackoffNextDelayDeterministisch(t *testing.T) {
	b := &ExponentialBackoff{BaseDelay: 10 * time.Millisecond, MaxDelay: 100 * time.Millisecond, Rand: rand.New(rand.NewSource(1))}
	d0 := b.NextDelay(0)
	d1 := b.NextDelay(1)
	if d0 <= 0 || d1 <= 0 {
		t.Fatalf("NextDelay lieferte nicht-positive Dauer: d0=%v d1=%v", d0, d1)
	}
	if d0 > b.MaxDelay || d1 > b.MaxDelay {
		t.Fatalf("NextDelay überschreitet MaxDelay: d0=%v d1=%v MaxDelay=%v", d0, d1, b.MaxDelay)
	}
	// gleicher Rand-Seed + gleicher attempt -> gleiches Ergebnis (Determinismus für Tests)
	b2 := &ExponentialBackoff{BaseDelay: 10 * time.Millisecond, MaxDelay: 100 * time.Millisecond, Rand: rand.New(rand.NewSource(1))}
	if b2.NextDelay(0) != d0 {
		t.Fatalf("NextDelay ist mit gleichem Seed nicht deterministisch")
	}
}

func TestNewExponentialBackoffDefaults(t *testing.T) {
	b := NewExponentialBackoff()
	if b.MaxAttempts <= 0 {
		t.Fatalf("MaxAttempts default = %d, erwartet > 0", b.MaxAttempts)
	}
	if b.BaseDelay <= 0 || b.MaxDelay <= 0 {
		t.Fatalf("BaseDelay/MaxDelay default nicht gesetzt: %v / %v", b.BaseDelay, b.MaxDelay)
	}
}

func TestNewLimiterDefaults(t *testing.T) {
	l := newLimiter(0, 0)
	if l.Limit() <= 0 {
		t.Fatalf("Limit() = %v, erwartet > 0 (Default greift bei rps<=0)", l.Limit())
	}
	if l.Burst() <= 0 {
		t.Fatalf("Burst() = %d, erwartet > 0 (Default greift bei burst<=0)", l.Burst())
	}

	l2 := newLimiter(2.5, 5)
	if l2.Limit() != rate.Limit(2.5) {
		t.Fatalf("Limit() = %v, erwartet 2.5 (explizit gesetzt)", l2.Limit())
	}
	if l2.Burst() != 5 {
		t.Fatalf("Burst() = %d, erwartet 5 (explizit gesetzt)", l2.Burst())
	}
}

// context_DeadlineExceededForTest vermeidet einen zusätzlichen "context"-Import nur für einen
// Test-Fehlerwert.
func context_DeadlineExceededForTest() error {
	return &testTimeoutErr{}
}

type testTimeoutErr struct{}

func (e *testTimeoutErr) Error() string { return "test: simulierter Netzwerkfehler" }
