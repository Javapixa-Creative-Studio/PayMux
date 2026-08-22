package ids

import (
	"sort"
	"testing"
	"time"
)

func TestNewHasPrefixAndValidates(t *testing.T) {
	id := New(Payment)
	if err := Validate(Payment, id); err != nil {
		t.Fatalf("Validate(%q) = %v", id, err)
	}
	if err := Validate(Application, id); err == nil {
		t.Fatalf("Validate accepted a payment id under the application prefix")
	}
	if got := PrefixOf(id); got != "pay" {
		t.Fatalf("PrefixOf(%q) = %q, want %q", id, got, "pay")
	}
}

func TestIDsAreLexicographicallySortableByTime(t *testing.T) {
	var got []string
	for i := 0; i < 5; i++ {
		got = append(got, newULID(time.UnixMilli(int64(1_700_000_000_000+i*1000))))
	}
	want := append([]string(nil), got...)
	sort.Strings(want)
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("ids are not sorted by time: %v", got)
		}
	}
}

func TestTimeRoundTrip(t *testing.T) {
	want := time.UnixMilli(1_763_000_123_456).UTC()
	id := string(Payment) + "_" + newULID(want)
	got, err := Time(id)
	if err != nil {
		t.Fatalf("Time(%q) error: %v", id, err)
	}
	if !got.Equal(want) {
		t.Fatalf("Time(%q) = %v, want %v", id, got, want)
	}
}

func TestValidateRejectsMalformed(t *testing.T) {
	for _, id := range []string{"", "pay", "pay_", "pay_short", "payx_01ARZ3NDEKTSV4RRFFQ69G5FAV", "pay_01ARZ3NDEKTSV4RRFFQ69G5FAU!"} {
		if err := Validate(Payment, id); err == nil {
			t.Errorf("Validate(%q) = nil, want error", id)
		}
	}
}

func TestUniqueness(t *testing.T) {
	seen := make(map[string]bool, 10000)
	for i := 0; i < 10000; i++ {
		id := New(Event)
		if seen[id] {
			t.Fatalf("duplicate id generated: %s", id)
		}
		seen[id] = true
	}
}
