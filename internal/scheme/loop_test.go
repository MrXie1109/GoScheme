package scheme

import (
	"strings"
	"testing"
)

func TestRecogniseListWalk(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want loopKind
	}{
		// 应当识别的
		{"求和", `(define (sum-list lst acc) (if (null? lst) acc (sum-list (cdr lst) (+ acc (car lst)))))`, loopSum},
		{"计数", `(define (count lst n) (if (null? lst) n (count (cdr lst) (+ 1 n))))`, loopCount},
		{"收集", `(define (rev lst acc) (if (null? lst) acc (rev (cdr lst) (cons (car lst) acc))))`, loopCollect},
		// 应当拒绝的（形状相似但含义不同）
		{"测试的是别的参数", `(define (f lst acc) (if (null? acc) acc (f (cdr lst) (+ acc (car lst)))))`, loopNone},
		{"推进的是别的参数", `(define (f lst acc) (if (null? lst) acc (f (cdr acc) (+ acc (car lst)))))`, loopNone},
		{"基础情形不是 acc", `(define (f lst acc) (if (null? lst) 0 (f (cdr lst) (+ acc (car lst)))))`, loopNone},
		{"组合用减法", `(define (f lst acc) (if (null? lst) acc (f (cdr lst) (- acc (car lst)))))`, loopNone},
		{"不是尾递归", `(define (f lst acc) (if (null? lst) acc (+ 1 (f (cdr lst) (+ acc (car lst))))))`, loopNone},
		{"调用的不是自己", `(define (f lst acc) (if (null? lst) acc (g (cdr lst) (+ acc (car lst)))))`, loopNone},
		{"三个参数", `(define (f lst acc k) (if (null? lst) acc (f (cdr lst) (+ acc (car lst)) k)))`, loopNone},
		{"组合里 car 的不是 lst", `(define (f lst acc) (if (null? lst) acc (f (cdr lst) (+ acc (car acc)))))`, loopNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			forms, err := NewStringReader(c.src).ReadAll()
			if err != nil {
				t.Fatal(err)
			}
			name, formals, body, ok := topLevelProcedure(forms[0])
			if !ok {
				t.Fatal("不是顶层过程定义")
			}
			w, got := recogniseListWalk(name, formals, body)
			if got != (c.want != loopNone) {
				t.Errorf("识别=%v 期望=%v", got, c.want != loopNone)
			}
			if got && w.kind != c.want {
				t.Errorf("kind=%v 期望=%v", w.kind, c.want)
			}
		})
	}
}

// TestListWalkIsEmittedAsOneCall checks that a recognised walk becomes a single
// runtime call rather than a loop that crosses per element.
//
// The speed comes from the shape of the emitted code, so it is worth asserting
// directly: a walk that is recognised but emitted element by element would still
// compute the right answer and still be slow, and no output-comparison test
// would notice.
func TestListWalkIsEmittedAsOneCall(t *testing.T) {
	p, err := CompileToIR(`(define (sum-list lst acc) (if (null? lst) acc (sum-list (cdr lst) (+ acc (car lst)))))`, "test")
	if err != nil {
		t.Fatal(err)
	}
	if p.Native != 1 {
		t.Fatalf("the walk was not compiled: %v", p.Refused)
	}
	if got := strings.Count(p.IR, "@gs_walk("); got != 2 { // one declare, one call
		t.Errorf("the module mentions gs_walk %d times, want 2 (declare + call):\n%s", got, p.IR)
	}
	for _, unwanted := range []string{"@gs_call", "@gs_truthy"} {
		if strings.Contains(p.IR, unwanted) {
			t.Errorf("the walk still crosses into the runtime through %s:\n%s", unwanted, p.IR)
		}
	}
}

// TestListWalkKindNumbersMatchTheRuntime checks that the number the emitter
// writes means the same thing to the runtime.
//
// This is the bug that was there: loopKind starts at loopNone = 0 so that its
// zero value means "not a walk", which put loopSum at 1, while the runtime's
// table started at walkSum = 0.  Summing therefore ran the counting walk, and
// `(sum-list (list 1..100) 0)` returned 100 instead of 5050 — a wrong answer
// with no error and no crash.
func TestListWalkKindNumbersMatchTheRuntime(t *testing.T) {
	if int(loopSum) != walkSum || int(loopCount) != walkCount || int(loopCollect) != walkCollect {
		t.Errorf("the emitter and the runtime disagree: loop=(%d,%d,%d) runtime=(%d,%d,%d)",
			loopSum, loopCount, loopCollect, walkSum, walkCount, walkCollect)
	}
	// And the walks actually do what their names say.
	lst := List(Int(1), Int(2), Int(3))
	if got := WriteToString(RunListWalk(int(loopSum), lst, Int(0))); got != "6" {
		t.Errorf("sum walk gave %s, want 6", got)
	}
	if got := WriteToString(RunListWalk(int(loopCount), lst, Int(0))); got != "3" {
		t.Errorf("count walk gave %s, want 3", got)
	}
	if got := WriteToString(RunListWalk(int(loopCollect), lst, Nil)); got != "(3 2 1)" {
		t.Errorf("collect walk gave %s, want (3 2 1)", got)
	}
}
