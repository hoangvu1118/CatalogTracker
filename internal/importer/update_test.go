package importer

import "testing"

func TestStatusTransitions(t *testing.T) {
	all := []Status{StatusRunning, StatusCompleted, StatusFailed, StatusInterrupted}
	allowed := map[Status][]Status{
		StatusRunning:     {StatusCompleted, StatusFailed, StatusInterrupted},
		StatusFailed:      {StatusRunning},
		StatusInterrupted: {StatusRunning},
		StatusCompleted:   {},
	}

	for _, from := range all {
		for _, to := range all {
			want := false
			for _, ok := range allowed[from] {
				if ok == to {
					want = true
				}
			}
			if got := from.CanBecome(to); got != want {
				t.Errorf("%s → %s: got %v, want %v", from, to, got, want)
			}
		}
	}
}

func TestOnlyFailedAndInterruptedCanResume(t *testing.T) {
	cases := map[Status]bool{
		StatusRunning:     false,
		StatusCompleted:   false,
		StatusFailed:      true,
		StatusInterrupted: true,
	}
	for s, want := range cases {
		if got := s.CanResume(); got != want {
			t.Errorf("%s.CanResume() = %v, want %v", s, got, want)
		}
	}
}
