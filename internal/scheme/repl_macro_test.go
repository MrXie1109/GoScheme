// SPDX-License-Identifier: MIT

package scheme

import "testing"

// The REPL evaluates one form at a time through Machine.Run, so a macro defined
// in one form has to be findable in the next.  This is the bug the corpus cannot
// catch, because the corpus runs a whole program in one call.
func TestMacroVisibleToTheNextForm(t *testing.T) {
	m := NewMachine()
	out := NewOutputStringPort()
	m.CurOut = out
	m.OutParam.values[0] = out
	forms, err := NewStringReader(`
		(define-syntax swap!
		  (syntax-rules () ((_ a b) (let ((tmp a)) (set! a b) (set! b tmp)))))
		(define p 1)
		(define q 2)
		(swap! p q)
		(display (list p q))`).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	for i, f := range forms {
		if _, err := m.Run(f, m.Global); err != nil {
			t.Fatalf("form %d: %v", i, err)
		}
	}
	if got := out.OutputString(); got != "(2 1)" {
		t.Errorf("got %q, want %q", got, "(2 1)")
	}
}
