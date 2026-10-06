package scheme

import (
	"regexp"
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

// TestRecogniseCountLoop checks the loop that counts down instead of walking.
//
//	(define (build n acc) (if (= n 0) acc (build (- n 1) (cons n acc))))
//
// This is how make-list, iota and range are written, and there is no sequence
// to walk: the counter is the element.
func TestRecogniseCountLoop(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want loopKind
	}{
		{"收集", `(define (build n acc) (if (= n 0) acc (build (- n 1) (cons n acc))))`, loopCollect},
		{"累加", `(define (s n acc) (if (= n 0) acc (s (- n 1) (+ acc n))))`, loopSum},
		{"累加反序", `(define (s n acc) (if (= n 0) acc (s (- n 1) (+ n acc))))`, loopSum},
		{"计数", `(define (c n acc) (if (= n 0) acc (c (- n 1) (+ acc 1))))`, loopCount},
		{"零在左边", `(define (c n acc) (if (= 0 n) acc (c (- n 1) (+ acc 1))))`, loopCount},
		// 应当拒绝
		{"递减的不是同一个", `(define (f n acc) (if (= n 0) acc (f (- acc 1) (cons n acc))))`, loopNone},
		{"不是减一", `(define (f n acc) (if (= n 0) acc (f (- n 2) (cons n acc))))`, loopNone},
		{"测试的不是零", `(define (f n acc) (if (= n 1) acc (f (- n 1) (cons n acc))))`, loopNone},
		{"折叠的是别的", `(define (f n acc) (if (= n 0) acc (f (- n 1) (cons acc acc))))`, loopNone},
		{"不是尾递归", `(define (f n acc) (if (= n 0) acc (cons 1 (f (- n 1) acc))))`, loopNone},
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
			w, got := recogniseCountLoop(name, formals, body)
			if got != (c.want != loopNone) {
				t.Fatalf("识别=%v 期望=%v", got, c.want != loopNone)
			}
			if got && w.kind != c.want {
				t.Errorf("kind=%v 期望=%v", w.kind, c.want)
			}
		})
	}
}

// TestEmptyListHasItsOwnTag checks that the empty list is not carried as the
// number zero.
//
// It used to be, and that was harmless while every value only flowed back to the
// interpreter: `0` and `()` are both false and neither is arithmetic.  A walk
// *keeps* its accumulator, so `(build 5 '())` consed onto the number zero and
// produced the improper list `(1 2 3 4 5 . 0)` — a wrong answer with no error,
// and one that only appears when the empty list reaches a compiled body that
// stores it.
func TestEmptyListHasItsOwnTag(t *testing.T) {
	p, err := CompileToIR(`(define (build n acc) (if (= n 0) acc (build (- n 1) (cons n acc))))
(define (go) (build 3 '()))`, "test")
	if err != nil {
		t.Fatal(err)
	}
	if p.Native != 2 {
		t.Fatalf("not both compiled: %v", p.Refused)
	}
	// The quoted empty list has to arrive as null, not as a fixnum zero: the
	// call passes (bits, tag) and the tag is what says which it is.
	if !strings.Contains(p.IR, ", i64 "+tagNull+")") {
		t.Errorf("the empty list is not passed with the null tag:\n%s", p.IR)
	}
	// The tag has to be distinct from every other, or the emitter could choose
	// it for something else and the runtime would read the wrong value back.
	seen := map[string]string{
		tagFixnum: "fixnum", tagHandle: "handle", tagBoolean: "boolean", tagNull: "null",
	}
	if len(seen) != 4 {
		t.Errorf("two tags share a value: %v", seen)
	}
}

// TestWalkEntryPointsAreDeclaredConsistently checks that the module's
// declarations of the walk entry points match how it calls them.
//
// This is the bug that crashed: `gs_countloop` was changed from taking two
// tagged values to taking an array, and one of the two emitters that call it was
// updated while the other was not.  The generated code then declared and called
// the old shape while the runtime implemented the new one, so the callee read a
// pointer where a tag had been — a segfault in the compiled program, with
// nothing wrong in the Scheme source and nothing in the module that looks
// malformed.
//
// Two emitters for one entry point is the hazard, so what is checked is that
// every declaration in the module agrees with the call beside it.
func TestWalkEntryPointsAreDeclaredConsistently(t *testing.T) {
	src := `(define (build n acc) (if (= n 0) acc (build (- n 1) (cons n acc))))
(define (sum-list lst acc) (if (null? lst) acc (sum-list (cdr lst) (+ acc (car lst)))))
(define (vsum v i n acc) (if (= i n) acc (vsum v (+ i 1) n (+ acc (vector-ref v i)))))
(define (count-even lst n) (if (null? lst) n (count-even (cdr lst) (if (even? (car lst)) (+ 1 n) n))))
(define (go) (list (build 3 '()) (sum-list (list 1 2) 0)))`
	p, err := CompileToIR(src, "test")
	if err != nil {
		t.Fatal(err)
	}
	// Every walk entry point the module mentions has to be declared once, and
	// the declaration has to be the shape the call uses.
	for _, name := range []string{"gs_walk", "gs_vecwalk", "gs_countloop"} {
		decl := regexp.MustCompile(`declare %gs\.val @` + name + `\(([^)]*)\)`).FindStringSubmatch(p.IR)
		call := regexp.MustCompile(`call %gs\.val @` + name + `\(([^,)]*)`).FindStringSubmatch(p.IR)
		if decl == nil || call == nil {
			continue // this module does not use it
		}
		// The declarations are written with named types; what matters is the
		// number and kind of arguments, so compare the first argument's type.
		if !strings.Contains(decl[1], "i32") {
			t.Errorf("%s is declared as (%s), which is not the shape it is called with", name, decl[1])
		}
		if !strings.HasPrefix(call[1], "i32") {
			t.Errorf("%s is called with a %s as its first argument, but declared with i32", name, call[1])
		}
	}
	// And the count loop is called with the array convention, which is the
	// change that was half-applied.
	if strings.Contains(p.IR, "gs_countloop") &&
		!strings.Contains(p.IR, "@gs_countloop(i32, %gs.val*, i32)") {
		t.Errorf("gs_countloop is not declared with the array convention:\n%s", p.IR)
	}
}

// TestRecogniseUpLoop checks the loop that counts up to a bound, which is the
// shape a do loop is written in.
func TestRecogniseUpLoop(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want loopKind
	}{
		{"累加索引", `(define (f i acc) (if (= i n) acc (f (+ i 1) (+ acc i))))`, loopSum},
		{"计数", `(define (f i acc) (if (= i n) acc (f (+ i 1) (+ acc 1))))`, loopCount},
		{"收集", `(define (f i acc) (if (= i n) acc (f (+ i 1) (cons i acc))))`, loopCollect},
		// 应当拒绝
		{"步长不是 1", `(define (f i acc) (if (= i n) acc (f (+ i 2) (+ acc i))))`, loopNone},
		{"测试的是别的", `(define (f i acc) (if (= acc n) acc (f (+ i 1) (+ acc i))))`, loopNone},
		{"递减", `(define (f i acc) (if (= i n) acc (f (- i 1) (+ acc i))))`, loopNone},
		{"折叠的是别的", `(define (f i acc) (if (= i n) acc (f (+ i 1) (+ acc n))))`, loopNone},
		{"三个参数", `(define (f i acc k) (if (= i n) acc (f (+ i 1) (+ acc i) k)))`, loopNone},
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
			w, got := recogniseUpLoop(name, formals, body)
			if got != (c.want != loopNone) {
				t.Fatalf("识别=%v 期望=%v", got, c.want != loopNone)
			}
			if got && w.kind != c.want {
				t.Errorf("kind=%v 期望=%v", w.kind, c.want)
			}
		})
	}
}

// TestRunUpLoopAgreesWithARange checks the upward walk against the answer
// computed independently.
//
// The walk's arguments are (from, end, acc), and the order is not the one the
// loop's own parameters are in — getting it wrong made a sum come out as a
// count, because the bound arrived where the accumulator belonged.  A test that
// only checked "a number came back" would have passed.
func TestRunUpLoopAgreesWithARange(t *testing.T) {
	for _, tc := range []struct{ lo, hi int64 }{{0, 10}, {1, 10}, {0, 1}, {5, 5}, {0, 0}} {
		want := int64(0)
		for i := tc.lo; i < tc.hi; i++ {
			want += i
		}
		got := RunUpLoop(walkSum, Int(tc.lo), Int(tc.hi), Int(0))
		if WriteToString(got) != WriteToString(Int(want)) {
			t.Errorf("sum %d..%d = %s, want %d", tc.lo, tc.hi, WriteToString(got), want)
		}
		// Counting counts the iterations, which is the other walk.
		gotCount := RunUpLoop(walkCount, Int(tc.lo), Int(tc.hi), Int(0))
		if WriteToString(gotCount) != WriteToString(Int(tc.hi-tc.lo)) {
			t.Errorf("count %d..%d = %s, want %d", tc.lo, tc.hi, WriteToString(gotCount), tc.hi-tc.lo)
		}
	}
}

// TestDoLoopIsCompiledAsAWalk checks that a do loop reaches the walk at all.
//
// Two separate gates had to open for it: the scanner refused `do` as a form
// before the walk was ever considered, and the emitter's own do branch was
// unreachable until it did not.  Either one alone leaves the loop interpreted
// while looking like it should be compiled.
func TestDoLoopIsCompiledAsAWalk(t *testing.T) {
	p, err := CompileToIR(`(define N 100)
(define (go) (do ((i 0 (+ i 1)) (acc 0 (+ acc i))) ((= i N) acc)))`, "test")
	if err != nil {
		t.Fatal(err)
	}
	if p.Native == 0 {
		t.Fatalf("the do loop was not compiled: %v", p.Refused)
	}
	if !strings.Contains(p.IR, "@gs_uploop(") {
		t.Errorf("the do loop is not emitted as an upward walk:\n%s", p.IR)
	}
}

// TestRecogniseSearch checks the walk that stops at the first element a test
// accepts, which is the other thing a loop over a sequence does.
func TestRecogniseSearch(t *testing.T) {
	cases := []struct {
		name string
		src  string
		pred predKind
	}{
		{"找第一个偶数", `(define (f lst) (if (null? lst) #f (if (even? (car lst)) (car lst) (f (cdr lst)))))`, predEven},
		{"找第一个正数", `(define (f lst) (if (null? lst) #f (if (positive? (car lst)) (car lst) (f (cdr lst)))))`, predPositive},
		{"找到就返回固定值", `(define (f lst) (if (null? lst) #f (if (pair? (car lst)) 1 (f (cdr lst)))))`, predPair},
		// 应当拒绝
		{"谓词测试别的", `(define (f lst) (if (null? lst) #f (if (even? lst) (car lst) (f (cdr lst)))))`, predNone},
		{"谓词是用户过程", `(define (f lst) (if (null? lst) #f (if (mine? (car lst)) (car lst) (f (cdr lst)))))`, predNone},
		{"递归推进的不是 cdr", `(define (f lst) (if (null? lst) #f (if (even? (car lst)) (car lst) (f lst))))`, predNone},
		{"不是尾递归", `(define (f lst) (if (null? lst) #f (if (even? (car lst)) (car lst) (cons 1 (f (cdr lst))))))`, predNone},
		{"两个参数", `(define (f lst x) (if (null? lst) #f (if (even? (car lst)) (car lst) (f (cdr lst) x))))`, predNone},
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
			w, got := recogniseSearch(name, formals, body)
			if got != (c.pred != predNone) {
				t.Fatalf("识别=%v 期望=%v", got, c.pred != predNone)
			}
			if got && w.pred != c.pred {
				t.Errorf("pred=%v 期望=%v", w.pred, c.pred)
			}
		})
	}
}

// TestRunSearchDistinguishesFoundFromMissed checks that finding a value equal to
// the "not found" answer is not reported as not having found it.
//
// This is why both answers are passed in rather than a single value plus a flag:
// a search for zero through a list of zeros finds zero, and a search that runs
// out returns whatever the empty case says — which may also be zero.  A runtime
// that returned one value would make those the same.
func TestRunSearchDistinguishesFoundFromMissed(t *testing.T) {
	zeros := List(Int(0), Int(0), Int(0))
	// Looking for a zero, with zero as the "not found" answer: it found one.
	got := RunSearch(int(predZero), zeros, Int(0), Int(0), 1)
	if WriteToString(got) != "0" {
		t.Errorf("a search that found a zero returned %s", WriteToString(got))
	}
	// Looking for something absent, with -1 as the answer.
	got = RunSearch(int(predNegative), zeros, Int(0), Int(-1), 1)
	if WriteToString(got) != "-1" {
		t.Errorf("a search that found nothing returned %s, want -1", WriteToString(got))
	}
	// A fixed answer rather than the element.
	got = RunSearch(int(predZero), zeros, Int(99), Int(-1), 0)
	if WriteToString(got) != "99" {
		t.Errorf("a search with a fixed answer returned %s, want 99", WriteToString(got))
	}
}

// TestBooleanIsAValueNotAPointer guards the type that five separate type switches
// had wrong.
//
// `type Boolean bool` is a value type, so a switch listing `*Boolean` compiles,
// matches nothing, and makes every `#t` and `#f` look like something the
// compiler cannot read.  It showed up as a body containing `#f` being refused
// outright, with the reason "the body contains boolean, which is not a literal or
// a call" — which reads like a deliberate refusal and was not one.
func TestBooleanIsAValueNotAPointer(t *testing.T) {
	if !isLiteral(False) || !isLiteral(True) {
		t.Error("a boolean literal is not recognised as one")
	}
	if !isSelfEvaluating(False) || !isSelfEvaluating(True) {
		t.Error("a boolean does not count as self-evaluating")
	}
	// And the emitter must produce a boolean rather than a fixnum for it.
	p, err := CompileToIR(`(define (f x) (if (even? x) #t #f))`, "test")
	if err != nil {
		t.Fatal(err)
	}
	if p.Native != 1 {
		t.Fatalf("a body returning #t and #f was refused: %v", p.Refused)
	}
	// Both arms have to carry the boolean tag: that is what makes the result a
	// boolean rather than the fixnum 1 or 0.  The tag reaches the phi that joins
	// the arms, so what is checked is the phi's entries — one for the value, one
	// for the tag — rather than the presence of the digit anywhere.
	if !strings.Contains(p.IR, "phi i64 [ 1, %then") {
		t.Errorf("the boolean's value is not 1 in the then-arm:\n%s", p.IR)
	}
	if !strings.Contains(p.IR, "phi i64 [ "+tagBoolean+", %then") {
		t.Errorf("the boolean tag is not carried through the join:\n%s", p.IR)
	}
}

// TestRecogniseMerge checks the merge, which is the core of every sort written
// in Scheme.
func TestRecogniseMerge(t *testing.T) {
	good := `(define (merge a b)
	  (cond ((null? a) b) ((null? b) a)
	        ((< (car b) (car a)) (cons (car b) (merge a (cdr b))))
	        (else (cons (car a) (merge (cdr a) b)))))`
	// The same merge written with the recursive call's arguments the other way
	// round, which is just as natural to write and is the same call.
	swapped := `(define (merge a b)
	  (cond ((null? a) b) ((null? b) a)
	        ((< (car b) (car a)) (cons (car b) (merge (cdr b) a)))
	        (else (cons (car a) (merge b (cdr a))))))`
	for _, tc := range []struct {
		name string
		src  string
		want bool
	}{
		{"cond 写法", good, true},
		{"递归参数互换", swapped, true},
		{"缺一个空表测试", `(define (merge a b) (cond ((null? a) b) ((< (car b) (car a)) (cons (car b) (merge a (cdr b)))) (else (cons (car a) (merge (cdr a) b)))))`, false},
		{"空表返回自己", `(define (merge a b) (cond ((null? a) a) ((null? b) a) ((< (car b) (car a)) (cons (car b) (merge a (cdr b)))) (else (cons (car a) (merge (cdr a) b)))))`, false},
		{"取的不是 car", `(define (merge a b) (cond ((null? a) b) ((null? b) a) ((< (car b) (car a)) (cons (cdr b) (merge a (cdr b)))) (else (cons (car a) (merge (cdr a) b)))))`, false},
		{"比较方向颠倒", `(define (merge a b) (cond ((null? a) b) ((null? b) a) ((< (car a) (car b)) (cons (car b) (merge a (cdr b)))) (else (cons (car a) (merge (cdr a) b)))))`, false},
		{"递归推进别的", `(define (merge a b) (cond ((null? a) b) ((null? b) a) ((< (car b) (car a)) (cons (car b) (merge a a))) (else (cons (car a) (merge (cdr a) b)))))`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			forms, err := NewStringReader(tc.src).ReadAll()
			if err != nil {
				t.Fatal(err)
			}
			name, formals, body, ok := topLevelProcedure(forms[0])
			if !ok {
				t.Fatal("不是顶层过程定义")
			}
			_, got := recogniseMerge(name, formals, body)
			if got != tc.want {
				t.Errorf("识别=%v 期望=%v", got, tc.want)
			}
		})
	}
}

// TestCondToIf checks the reader that lets a merge be written as a cond.
//
// It handles only the clause shapes a merge uses, and returns the form unchanged
// for anything else — a reader for one shape, not a second implementation of
// cond.  Returning something wrong here would make the recognisers below accept
// a body they should not.
func TestCondToIf(t *testing.T) {
	got := WriteToString(condToIf(mustRead2(t, `(cond (a b) (c d) (else e))`)))
	want := "(if a b (if c d e))"
	if got != want {
		t.Errorf("condToIf = %s, want %s", got, want)
	}
	// Not a cond: unchanged.
	plain := `(if a b c)`
	if WriteToString(condToIf(mustRead2(t, plain))) != plain {
		t.Errorf("a non-cond was rewritten")
	}
	// A clause with several expressions is left alone rather than guessed at.
	multi := `(cond (a b c) (else d))`
	if WriteToString(condToIf(mustRead2(t, multi))) != multi {
		t.Errorf("a multi-expression clause was rewritten")
	}
}

// mustRead2 reads exactly one datum.
func mustRead2(t *testing.T, src string) Value {
	t.Helper()
	forms, err := NewStringReader(src).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(forms) != 1 {
		t.Fatalf("expected one datum, got %d", len(forms))
	}
	return forms[0]
}

// TestRunMergeAgreesWithASort checks the merge against the same merge written in
// Go, on lists that exercise both branches and the two ends.
func TestRunMergeAgreesWithASort(t *testing.T) {
	for _, tc := range []struct{ a, b []int64 }{
		{[]int64{1, 3, 5}, []int64{2, 4, 6}},
		{[]int64{}, []int64{1, 2}},
		{[]int64{1, 2}, []int64{}},
		{[]int64{}, []int64{}},
		{[]int64{1, 1, 1}, []int64{1, 1}},
		{[]int64{5, 6, 7}, []int64{1, 2, 3}},
	} {
		la := intList(tc.a)
		lb := intList(tc.b)
		want := append(append([]int64{}, tc.a...), tc.b...)
		sortInts(want)
		got := RunMerge(mergeLt, la, lb)
		if WriteToString(got) != WriteToString(intList(want)) {
			t.Errorf("merge(%v, %v) = %s, want %v", tc.a, tc.b, WriteToString(got), want)
		}
		// `<=` takes from b on a tie, which is a different merge on equal
		// elements — and the point of having two codes at all.
		gotLe := RunMerge(mergeLe, la, lb)
		var listLe []int64
		for p := gotLe; ; {
			pr, ok := p.(*Pair)
			if !ok {
				break
			}
			n, _ := pr.Car.(*Integer)
			listLe = append(listLe, n.i)
			p = pr.Cdr
		}
		sortInts(listLe)
		if len(listLe) != len(want) {
			t.Errorf("<= merge lost elements: %v", listLe)
		}
	}
}

func intList(xs []int64) Value {
	out := Value(Nil)
	for i := len(xs) - 1; i >= 0; i-- {
		out = Cons(Int(xs[i]), out)
	}
	return out
}

func sortInts(xs []int64) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}

// TestRecogniseBuild checks the walk whose recursive call is not in tail
// position — how append, copy-list, map and filter are written.
func TestRecogniseBuild(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		ok     bool
		filter bool
		mapIt  bool
	}{
		{"复制", `(define (f l) (if (null? l) '() (cons (car l) (f (cdr l)))))`, true, false, false},
		{"追加", `(define (f l rest) (if (null? l) rest (cons (car l) (f (cdr l) rest))))`, true, false, false},
		{"映射", `(define (f l) (if (null? l) '() (cons (even? (car l)) (f (cdr l)))))`, true, false, true},
		{"过滤", `(define (f l) (if (null? l) '() (if (even? (car l)) (cons (car l) (f (cdr l))) (f (cdr l)))))`, true, true, false},
		// 应当拒绝
		{"尾部不是字面量或参数", `(define (f l) (if (null? l) (car l) (cons (car l) (f (cdr l)))))`, false, false, false},
		{"递归推进别的", `(define (f l) (if (null? l) '() (cons (car l) (f l))))`, false, false, false},
		{"映射用用户过程", `(define (f l) (if (null? l) '() (cons (mine? (car l)) (f (cdr l)))))`, false, false, false},
		{"cons 的不是头", `(define (f l) (if (null? l) '() (cons (cdr l) (f (cdr l)))))`, false, false, false},
		{"过滤保留的是测试结果", `(define (f l) (if (null? l) '() (if (even? (car l)) (cons (even? (car l)) (f (cdr l))) (f (cdr l)))))`, false, false, false},
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
			w, got := recogniseBuild(name, formals, body)
			if got != c.ok {
				t.Fatalf("识别=%v 期望=%v", got, c.ok)
			}
			// A filter has a predicate too — it is what the filter tests — so
			// "is a map" means a predicate *and* not a filter.
			isMap := w.mapPred != predNone && !w.filter
			if got && (w.filter != c.filter || isMap != c.mapIt) {
				t.Errorf("filter=%v map=%v，期望 filter=%v map=%v", w.filter, isMap, c.filter, c.mapIt)
			}
		})
	}
}

// TestRunBuildDistinguishesTheThreeModes checks copy, map and filter against the
// answers the Scheme versions give.
//
// The three are the same shape and mean different things — a copy keeps every
// element, a map keeps what the test returns, a filter keeps the elements the
// test accepted — so a runtime that ran one of them for another would quietly
// produce the wrong list.  `(map even? '(1 2))` is `(#f #t)` and
// `(filter even? '(1 2))` is `(2)`; nothing about the shape says which.
func TestRunBuildDistinguishesTheThreeModes(t *testing.T) {
	lst := List(Int(1), Int(2), Int(3), Int(4))
	for _, tc := range []struct {
		mode int
		want string
	}{
		{buildCopy, "(1 2 3 4)"},
		{buildMap, "(#f #t #f #t)"},
		{buildFilter, "(2 4)"},
	} {
		got := RunBuild(int(predEven), lst, Nil, tc.mode)
		if WriteToString(got) != tc.want {
			t.Errorf("mode %d = %s, want %s", tc.mode, WriteToString(got), tc.want)
		}
	}
	// An append's tail has to be the last element's cdr rather than a fresh
	// list: sharing it is what makes append cheap.
	tail := List(Int(9))
	got := RunBuild(int(predEven), List(Int(1), Int(2)), tail, buildCopy)
	if WriteToString(got) != "(1 2 9)" {
		t.Errorf("append with a tail = %s", WriteToString(got))
	}
}

// TestQuotedEmptyListIsAcceptedAsATail checks that `'()` is readable as the end
// of a list-building loop.
//
// `'()` reads as the pair `(quote ())`, not as the empty list, so it is neither a
// literal nor a name.  A recogniser that only knew those two refused every
// append and copy-list ever written — and refused them in a way that looked like
// a considered decision rather than a gap.
func TestQuotedEmptyListIsAcceptedAsATail(t *testing.T) {
	if !isQuoted(mustRead2(t, `'()`)) {
		t.Error("'() is not recognised as a quoted literal")
	}
	if isQuoted(mustRead2(t, `'x`)) {
		t.Error("a quoted name is being treated as a literal tail")
	}
	p, err := CompileToIR(`(define (copy l) (if (null? l) '() (cons (car l) (copy (cdr l)))))`, "test")
	if err != nil {
		t.Fatal(err)
	}
	if p.Native != 1 {
		t.Fatalf("a copy-list was refused: %v", p.Refused)
	}
	if !strings.Contains(p.IR, "@gs_build(") {
		t.Errorf("the copy is not emitted as a build walk:\n%s", p.IR)
	}
}
