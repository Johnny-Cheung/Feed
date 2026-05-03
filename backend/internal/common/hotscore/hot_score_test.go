package hotscore

import (
	"testing"
	"time"
)

func TestCalculate(t *testing.T) {
	publishedAt := time.Now().UTC().Add(-25 * time.Hour)

	got := Calculate(publishedAt, 10, 2, 4)
	want := 12.0
	if got != want {
		t.Fatalf("Calculate() = %v, want %v", got, want)
	}
}

func TestCalculateWithFuturePublishTime(t *testing.T) {
	publishedAt := time.Now().UTC().Add(time.Hour)

	got := Calculate(publishedAt, 1, 1, 1)
	want := 6.0
	if got != want {
		t.Fatalf("Calculate() = %v, want %v", got, want)
	}
}

func TestCalculateWithZeroPublishTime(t *testing.T) {
	if got := Calculate(time.Time{}, 1, 1, 1); got != 0 {
		t.Fatalf("Calculate() = %v, want 0", got)
	}
}
