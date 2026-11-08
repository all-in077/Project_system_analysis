package domain

import "testing"

func TestTransitions(t *testing.T) {
	cases := []struct {
		from, to Status
		ok       bool
	}{
		{Created, Sending, true},
		{Sending, Unconfirmed, true},
		{Unconfirmed, New, true},
		{Unconfirmed, Filled, true},     // правило 3: события не по порядку
		{CancelRequested, Filled, true}, // правило 2: исполнение сильнее отмены
		{Filled, Cancelled, false},      // правило 1: терминальное не меняется
		{Cancelled, Filled, false},
		{Rejected, New, false},
		{New, Created, false},
	}
	for _, c := range cases {
		if got := CanTransition(c.from, c.to); got != c.ok {
			t.Errorf("%s -> %s: got %v, want %v", c.from, c.to, got, c.ok)
		}
	}
}

func TestTerminal(t *testing.T) {
	for _, s := range []Status{Filled, Cancelled, Rejected} {
		if !s.IsTerminal() {
			t.Errorf("%s должно быть терминальным", s)
		}
	}
	for _, s := range []Status{Created, Sending, Unconfirmed, New, PartiallyFilled, CancelRequested} {
		if s.IsTerminal() {
			t.Errorf("%s не должно быть терминальным", s)
		}
	}
}
