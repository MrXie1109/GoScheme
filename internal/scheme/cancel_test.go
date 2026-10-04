package scheme

import (
	"errors"
	"testing"
	"time"
)

// Closing the cancel channel stops an evaluation at its next step, with
// ErrInterrupted rather than a Scheme condition: the program did not fail, it
// was told to stop.
func TestCancelStopsAnEvaluation(t *testing.T) {
	m := NewMachine()
	cancel := make(chan struct{})
	m.SetCancel(cancel)

	forms, err := NewStringReader(`
		(define (loop i acc) (if (= i 0) acc (loop (- i 1) (+ acc i))))
		(loop 100000000 0)`).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for _, f := range forms {
			if _, err := m.Run(f, m.Global); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	time.Sleep(20 * time.Millisecond)
	close(cancel)
	select {
	case err := <-done:
		if !errors.Is(err, ErrInterrupted) {
			t.Errorf("err = %v, want ErrInterrupted", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the evaluation did not stop when cancelled")
	}
}

// The important case, and the one that was wrong: a send that was cancelled
// must not deliver.  It used to stay parked on the channel, so the value showed
// up in whoever received next.
func TestCancelledSendDeliversNothing(t *testing.T) {
	m := NewMachine()
	ch := mustEval(t, m, `(make-channel)`)
	send := mustEval(t, m, `chan-send!`)

	cancel := make(chan struct{})
	m.SetCancel(cancel)
	done := make(chan error, 1)
	go func() {
		_, err := m.RunApply(send, []Value{ch, NewString("hello")}, m.Global)
		done <- err
	}()

	time.Sleep(20 * time.Millisecond)
	close(cancel)
	select {
	case err := <-done:
		if !errors.Is(err, ErrInterrupted) {
			t.Fatalf("send err = %v, want ErrInterrupted", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled send is still parked on the channel")
	}

	// Nothing was delivered: a receive on a fresh machine must not return at
	// once with the abandoned value.
	m2 := NewMachine()
	m2.SetCancel(make(chan struct{}))
	recv := mustEval(t, m2, `chan-recv!`)
	got := make(chan Value, 1)
	go func() {
		v, err := m2.RunApply(recv, []Value{ch}, m2.Global)
		if err == nil {
			got <- v
		}
	}()
	select {
	case v := <-got:
		t.Errorf("the cancelled send delivered %s", WriteToString(v))
	case <-time.After(200 * time.Millisecond):
		// Correct: the receive is waiting for a value that was never sent.
	}
}

// mustEval evaluates a form and returns its value.
func mustEval(t *testing.T, m *Machine, src string) Value {
	t.Helper()
	forms, err := NewStringReader(src).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	var out Value
	for _, f := range forms {
		v, err := m.Run(f, m.Global)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		out = v
	}
	return out
}
