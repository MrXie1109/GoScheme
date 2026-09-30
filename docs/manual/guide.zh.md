# GoScheme 指南

一个文件带你走完这个解释器与它的库。逐过程速查表在
[`docs/extensions/`](../extensions/README.md)（`(goscheme ...)`）与
[`docs/srfi/`](../srfi/README.md)（SRFI），完整参考是
[主 README](../README_zh.md)，另外 `examples/` 里有可直接运行的程序。

## 1. 怎么跑起来

```sh
make build                        # 生成 .build/goscheme
./.build/goscheme program.scm     # 运行文件
./.build/goscheme -e '(display (+ 1 2))'   # 运行一个表达式
./.build/goscheme                 # REPL
```

`(command-line)` 是**脚本名加上用户的参数**：解释器自己的名字从不出现，这是对
R7RS 6.14 的有意偏离，因此 `(cdr (command-line))` 就正好是参数表。

```scheme
;; args.scm
(display (command-line)) (newline)
;; $ ./.build/goscheme args.scm one two
;; => ("args.scm" "one" "two")
```

## 2. 一页语言

```scheme
(define (square x) (* x x))              ; 过程
(define add (lambda (a b) (+ a b)))      ; 同样的东西写全
(let ((x 2) (y 3)) (+ x y))              ; => 5
(let* ((x 2) (y (* x x))) y)             ; => 4      （每个绑定能看到前一个）
(letrec ((even? (lambda (n) (or (= n 0) (odd? (- n 1)))))
         (odd?  (lambda (n) (and (> n 0) (even? (- n 1))))))
  (even? 10))                            ; => #t
(cond ((assv 2 '((1 . a) (2 . b))) => cdr) (else 'none))   ; => b
(case 3 ((1 2) 'low) ((3 4) 'high) (else 'other))          ; => high
(do ((i 0 (+ i 1)) (acc '() (cons i acc))) ((= i 3) acc))  ; => (2 1 0)
```

尾位置的递归不会增长栈；`call/cc` 是多发射的：续延返回之后还能再被调用一次。

```scheme
(define (loop n acc) (if (= n 0) acc (loop (- n 1) (+ acc 1))))
(loop 1000000 0)                          ; => 1000000，栈不增长

(call-with-values (lambda () (values 1 2)) (lambda (a b) (+ a b)))  ; => 3
(guard (e ((symbol? e) (list 'caught e))) (raise 'boom))            ; => (caught boom)
```

`define-record-type`、`define-syntax` + `syntax-rules`、`parameterize`、
`dynamic-wind`、`delay`/`force` 都是 R7RS-small；完整清单见
[README](../README_zh.md#语言覆盖)。

```scheme
(define-record-type point (make-point x y) point? (x point-x) (y point-y))
(point-x (make-point 3 4))                ; => 3

(define-syntax swap!
  (syntax-rules ()
    ((_ a b) (let ((tmp a)) (set! a b) (set! b tmp)))))
```

库按名字导入；非内置的库会从搜索路径加载，所以程序可以自带 `lib/greet.sld`。

```scheme
(import (scheme base) (scheme write) (goscheme fast) (srfi 1))
```

## 3. 数值

整数精确且任意精度，有理数精确；只有当不精确值参与运算时结果才变成不精确。

```scheme
(+ 1/3 1/6)            ; => 1/2
(expt 2 100)           ; => 1267650600228229401496703205376
(/ 1 3)                ; => 1/3
(exact->inexact 1/3)   ; => 0.3333333333333333
(exact 2.0)            ; => 2
(floor/ 7 2)           ; => 3 和 1
(number->string 255 16) ; => "ff"
```

Scheme 写起来慢的整数活由 `(goscheme fast)` 补上：`expt-mod`、`isqrt`、
`prime?`、`primes`、`factor`，以及位运算
`bit-and`/`-or`/`-xor`/`-not`/`-shift`/`-count`。

```scheme
(expt-mod 2 1000 1000000007)   ; => 688423210
(factor 1234567890)            ; => (2 3 3 5 3607 3803)
(length (primes 100000))       ; => 9592
(integer-length 255)           ; => 8
```

## 4. 数据与文本

列表有 R7RS 与 SRFI-1；向量有 R7RS、SRFI-133 与 `(goscheme fast)` 的批量车道。
注意折叠的参数顺序：SRFI-1 的 `fold` 调用 `(kons 元素 累加器)`，而
`(goscheme fast)` 的 `fold-left` 调用 `(proc 累加器 元素)`。

```scheme
(fold - 0 '(1 2 3))                       ; => 2   SRFI-1
(fold-left - 0 '(1 2 3))                  ; => -6  (goscheme fast)
(fold-right cons '() '(1 2 3))            ; => (1 2 3)
(take-while (lambda (x) (< x 3)) '(1 2 3 4))   ; => (1 2)
(lset-union = '(1 2) '(2 3))              ; => (1 2 3)
```

向量：批量过程对整段向量只走一遍 Go，带 `!` 的版本原地写入。

```scheme
(vector-add #(1 2 3) #(10 20 30))         ; => #(11 22 33)
(vector-prefix-sum #(1 2 3))              ; => #(1 3 6)
(let ((v (vector-iota 4))) (vector-scale! v 2) v)   ; => #(0 2 4 6)
(call-with-values (lambda () (vector-partition even? #(1 2 3 4)))
  (lambda (v k) (list v k)))              ; => (#(2 4 1 3) 2)
```

字符串按**字符**索引而不是字节：`string-length` 数字符，`string-byte-length`
数 UTF-8 字节。

```scheme
(string-fields "  a  b ")                 ; => ("a" "b")
(string-lines "a\r\nb\r\n")               ; => ("a" "b")
(string-titlecase "hello WORLD")          ; => "Hello World"
(string-split "a:b:c" ":" 2)              ; => ("a" "b:c")
(string-find-all "aaaa" "aa")             ; => (0 2)
```

哈希表、用于解构的 `match`、JSON 与正则：

```scheme
(import (goscheme hash-table) (goscheme match) (goscheme json) (goscheme regexp))

(define t (make-hash-table))              ; equal? 表
(hash-table-set! t "a" 1)
(hash-table-ref/default t "a" 'missing)   ; => 1

(match '(1 2 3)
  ((a b c) (+ a b c))
  ((_ ... rest) 'longer))                 ; => 6

(json-parse "{\"n\": 42}")                ; => 一张 equal? 表
(regexp-match-positions (regexp "a+") "baa")   ; => ((1 . 3)) —— 字节下标
```

## 5. 并发

线程是 Go 的 goroutine，通道是 Go 的 channel，用 `(go ...)` 启动。解释器自身的
状态、通道、互斥量与原子量都可以安全共享；普通 Scheme 数据（序对、字符串、向量、
记录）不行——请通过通道通信，而不是共享内存。

```scheme
(import (goscheme channel) (goscheme sync))

(define ch (make-channel))
(go (chan-send! ch (* 2 21)))
(chan-recv! ch)                           ; => 42

(select
  ((chan-recv! ch) => (lambda (v) v))
  ((after 100) => (lambda (ms) 'timeout))
  (else => (lambda () 'nothing-ready)))
```

用等待组写的工作池，以及无论 body 怎样离开都会释放锁的互斥形式：

```scheme
(define jobs (make-channel 4))
(define results (make-channel 4))
(define wg (make-waitgroup))
(waitgroup-add! wg 1)
(go (with-mutex some-mutex (chan-send! results 'done)) (waitgroup-done! wg))
(waitgroup-wait wg)
```

`go-wait` 等待此前启动的每一个线程，所以长期运行的服务线程要留到最后。有两种误用
会中止进程而不是抛条件：解锁自己并未持有的互斥量，以及 `(after ms)` 的 `ms` 为
负数或不是精确整数。

## 6. I/O 与操作系统

端口就是 R7RS：`read-line`、`read`、`write`、`display`、`with-output-to-file`、
`call-with-input-file`、`open-input-string`。socket 连接就是双向的普通端口。

```scheme
(import (goscheme fs) (goscheme process) (goscheme socket) (goscheme http) (goscheme time))

(glob "*.scm")                            ; 路径列表
(path-extension "notes/a.txt")            ; => ".txt"
(system* "echo" "hello")                  ; => 0（退出状态），输出继承
(define in (open-input-process "printf" "%s\n" "piped"))
(read-line in)                            ; => "piped"
(close-port in)
(process-status in)                       ; => 关闭之后是 0

(sleep 10)                                ; 毫秒
(monotonic-millisecond)                   ; 单调时钟读数
```

一个极小的服务器，以及跟它对话的客户端：

```scheme
(define listener (tcp-listen 0))
(go (let ((c (tcp-accept listener)))
      (write-string (string-append (read-line c) "\n") c)
      (close-port c)))
(define c (tcp-connect "127.0.0.1" (tcp-listener-port listener)))
(write-string "ping\n" c)
(read-line c)                             ; => "ping"
```

## 7. 性能

解释器是显式续延栈上的树遍历器，所以它不是最快的 Scheme；
[README 的性能一节](../README_zh.md#性能)有实测数字。慢活搬进了
`(goscheme fast)`，判断它是否划算的规则很简单：

* **内建**谓词或比较（`<`、`string<?`、`even?`、`string?`）在 Go 循环里直接执行，
  所以 `filter`、`sort`、`count`、`any`、`every`、`delete-duplicates` 以及用
  `+`/`*`/`max`/`min` 的折叠都快；
* **你自己写**的谓词每元素要调一次，开销与手写 Scheme 循环相当，那种情况下这个库
  提供的是方便，而不是速度。

```scheme
(define v (vector-iota 100000))
(filter even? v)                          ; Go 循环
(filter (lambda (x) (even? x)) v)         ; 每元素一次调用
```

测量要用不会倒退的时钟，并且不止测一次：`current-millisecond` 是墙钟，
`monotonic-millisecond` 才是量时长该用的。

```scheme
(import (goscheme time))
(define (timed thunk)
  (let ((start (monotonic-millisecond)))
    (let ((v (thunk))) (list v (- (monotonic-millisecond) start)))))
(car (timed (lambda () (length (sort (iota 20000))))))
```

## 8. 嵌入与打包

`goscheme build` 把脚本连同解释器打成一个可执行文件；`-o` 指定输出名（默认
`a.out`，Windows 上是 `a.exe`），`-static` 会预先解析库，使程序在运行时不会因为
缺文件而失败。

```sh
./.build/goscheme build script.scm -o mytool
./.build/goscheme build -static server.scm -o server
./dist/goscheme-linux-amd64 program.scm
```

解释器同时也是一个 Go 包；完整接口很小，
[README 的嵌入一节](../README_zh.md#嵌入到-go-程序)与 `examples/embed/` 有全部细节：

```go
import goscheme "github.com/MrXie1109/GoScheme"

i := goscheme.New()
i.Define("double", 1, 1, func(args []goscheme.Value) (goscheme.Value, error) {
    n, _ := args[0].Int()
    return goscheme.Int(n * 2), nil
})
v, _ := i.Eval("(double 21)")             // 42
```

## 9. 坑

* **后导入的会覆盖先导入的。** 两个库导出同名但不同的绑定时，留下的是最后那个。
  这正是 `(goscheme fast)`、`(srfi 1)` 与 `(srfi 133)` 对每一个共同名字只保留
  **同一个绑定**的原因。
* **`eqv?`/`equal?` 不是 `=`。** `(equal? 1 1.0)` 是 `#f`，而 `(= 1 1.0)` 是 `#t`。
* **普通数据没有同步。** 请用通道，或者 `(goscheme sync)`。
* **两种误用会中止而不是抛出：** 解锁自己没持有的互斥量是 Go 运行时致命错误；
  `(after ms)` 的 `ms` 不合法时 panic，而 `guard` 抓不住。
* **`select` 是随机选的**（在同时就绪的子句之间）；书写顺序只决定求值顺序。
* **`json-write` 写的是哈希表而不是 alist**，并且键按排序输出。
* **正则的位置是字节偏移**；匹配过程也接受裸的模式字符串。
* **单值上下文里的多值会被截断**为第一个。
* **进程退出时不等 goroutine**；要调用 `go-wait`。
* 对环状数据 `display` 可能不终止，`write-simple` 也可能，这在 R7RS 里是允许的。

## 10. 接下来看哪里

* [`docs/extensions/`](../extensions/README.md) —— 每个 `(goscheme ...)` 过程的
  参数与错误。
* [`docs/srfi/`](../srfi/README.md) —— SRFI-1、2、8、26、111、128、133。
* `examples/` —— 每个库的可运行程序，清单在
  [`examples/README_zh.md`](../../examples/README_zh.md)。
* [README](../README_zh.md) —— 参考：语言覆盖、实现说明、测试、交叉编译。
