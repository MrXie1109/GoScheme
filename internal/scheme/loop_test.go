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

// TestRecogniseVecWalk checks the vector walk, and the shapes that look like one
// without being one.
func TestRecogniseVecWalk(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want loopKind
	}{
		{"求和", `(define (s v i n acc) (if (= i n) acc (s v (+ i 1) n (+ acc (vector-ref v i)))))`, loopSum},
		{"计数", `(define (c v i n acc) (if (= i n) acc (c v (+ i 1) n (+ 1 acc))))`, loopCount},
		{"收集", `(define (r v i n acc) (if (= i n) acc (r v (+ i 1) n (cons (vector-ref v i) acc))))`, loopCollect},
		// 应当拒绝
		{"步长不是 1", `(define (s v i n acc) (if (= i n) acc (s v (+ i 2) n (+ acc (vector-ref v i)))))`, loopNone},
		{"边界不是 n", `(define (s v i n acc) (if (= i n) acc (s v (+ i 1) i (+ acc (vector-ref v i)))))`, loopNone},
		{"测试的是别的对", `(define (s v i n acc) (if (= v n) acc (s v (+ i 1) n (+ acc (vector-ref v i)))))`, loopNone},
		{"用 vector-length 而非参数", `(define (s v i n acc) (if (= i (vector-length v)) acc (s v (+ i 1) n (+ acc (vector-ref v i)))))`, loopNone},
		{"不是向量取用", `(define (s v i n acc) (if (= i n) acc (s v (+ i 1) n (+ acc (car v)))))`, loopNone},
		{"五个参数", `(define (s v i n acc k) (if (= i n) acc (s v (+ i 1) n (+ acc (vector-ref v i)) k)))`, loopNone},
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
			w, got := recogniseVecWalk(name, formals, body)
			if got != (c.want != loopNone) {
				t.Errorf("识别=%v 期望=%v", got, c.want != loopNone)
			}
			if got && w.kind != c.want {
				t.Errorf("kind=%v 期望=%v", w.kind, c.want)
			}
		})
	}
}

// TestVecWalkIsEmittedAsOneCall checks the vector walk becomes one call.
func TestVecWalkIsEmittedAsOneCall(t *testing.T) {
	p, err := CompileToIR(`(define (vsum v i n acc) (if (= i n) acc (vsum v (+ i 1) n (+ acc (vector-ref v i)))))`, "test")
	if err != nil {
		t.Fatal(err)
	}
	if p.Native != 1 {
		t.Fatalf("the walk was not compiled: %v", p.Refused)
	}
	// One declare, one call.
	if got := strings.Count(p.IR, "@gs_vecwalk("); got != 2 {
		t.Errorf("gs_vecwalk mentioned %d times, want 2:\n%s", got, p.IR)
	}
	if strings.Contains(p.IR, "@gs_call") {
		t.Errorf("the walk still crosses through gs_call:\n%s", p.IR)
	}
}

// TestVecWalkOutOfRangeRaises checks that an index the caller got wrong raises
// rather than being quietly ignored.
//
// The interpreted `(vector-ref v i)` raises, so a walk that read past the end
// would be a compiled program that silently returned something where the
// interpreter reported an error — the disagreement the whole design is meant to
// prevent.
func TestVecWalkOutOfRangeRaises(t *testing.T) {
	v := &Vector{Items: []Value{Int(1), Int(2)}}
	defer func() {
		if r := recover(); r == nil {
			t.Error("reading past the end of a vector did not raise")
		}
	}()
	RunVecWalk(walkSum, v, Int(0), Int(5), Int(0))
}

// TestRecogniseConditionalFold checks a walk whose fold happens only for some
// elements.
//
//	(define (count-even lst n)
//	  (if (null? lst) n (count-even (cdr lst) (if (even? (car lst)) (+ 1 n) n))))
//
// The predicate has to be a builtin.  A programmer-written predicate would have
// to be called back into Scheme once per element, which is the crossing the walk
// exists to avoid, so such a walk is not recognised and takes the ordinary path.
func TestRecogniseConditionalFold(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want loopKind
		pred predKind
	}{
		{
			name: "计数偶数",
			src:  `(define (f lst n) (if (null? lst) n (f (cdr lst) (if (even? (car lst)) (+ 1 n) n))))`,
			want: loopCount, pred: predEven,
		},
		{
			name: "累积正数",
			src:  `(define (f lst acc) (if (null? lst) acc (f (cdr lst) (if (positive? (car lst)) (+ acc (car lst)) acc))))`,
			want: loopSum, pred: predPositive,
		},
		{
			name: "收集偶数",
			src:  `(define (f lst acc) (if (null? lst) acc (f (cdr lst) (if (even? (car lst)) (cons (car lst) acc) acc))))`,
			want: loopCollect, pred: predEven,
		},
		{
			// 测试的必须是同一个元素。
			name: "谓词测试别的",
			src:  `(define (f lst n) (if (null? lst) n (f (cdr lst) (if (even? n) (+ 1 n) n))))`,
			want: loopNone,
		},
		{
			// 用户写的谓词无法下沉。
			name: "谓词是用户过程",
			src:  `(define (f lst n) (if (null? lst) n (f (cdr lst) (if (my-pred (car lst)) (+ 1 n) n))))`,
			want: loopNone,
		},
		{
			// 两个分支都变，不是「累积或不变」。
			name: "两个分支都累积",
			src:  `(define (f lst n) (if (null? lst) n (f (cdr lst) (if (even? (car lst)) (+ 1 n) (+ 2 n)))))`,
			want: loopNone,
		},
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
				t.Fatalf("识别=%v 期望=%v", got, c.want != loopNone)
			}
			if !got {
				return
			}
			if w.kind != c.want || w.pred != c.pred {
				t.Errorf("kind=%v pred=%v，期望 kind=%v pred=%v", w.kind, w.pred, c.want, c.pred)
			}
		})
	}
}

// TestConditionalWalkAgreesWithTheSchemePredicate checks that the test the walk
// applies is the test the Scheme predicate is.
//
// The two are written separately — one is a Go method, the other is the
// library's — so a disagreement is possible and would be silent: a walk that
// counted odd numbers where the program asked for even ones still prints a
// number.
func TestConditionalWalkAgreesWithTheSchemePredicate(t *testing.T) {
	m := NewMachine()
	for _, tc := range []struct {
		pred predKind
		name string
	}{
		{predEven, "even?"},
		{predOdd, "odd?"},
		{predPositive, "positive?"},
		{predNegative, "negative?"},
		{predZero, "zero?"},
		{predPair, "pair?"},
		{predNull, "null?"},
		{predNumber, "number?"},
		{predString, "string?"},
		{predSymbol, "symbol?"},
		{predVector, "vector?"},
	} {
		proc, ok := m.Global.Lookup(Intern(tc.name))
		if !ok {
			t.Fatalf("%s is not defined", tc.name)
		}
		for _, v := range []Value{
			Int(0), Int(1), Int(-1), Int(2), Int(-7),
			Float(0.5), Float(-0.5),
			&String{}, &Symbol{Name: "s"}, &Vector{}, Nil, List(Int(1)),
		} {
			got, err := m.ApplySync(proc, []Value{v})
			if err != nil {
				// The predicate raised, so the walk must raise too; the walk's
				// holds panics in that case, which the caller turns into the
				// same condition.
				continue
			}
			want := IsTrue(got)
			var have bool
			func() {
				defer func() { _ = recover() }()
				have = tc.pred.holds(v)
			}()
			if have != want {
				t.Errorf("%s of %s: walk says %v, Scheme says %v",
					tc.name, WriteToString(v), have, want)
			}
		}
	}
}
