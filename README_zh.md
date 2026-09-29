# GoScheme

用 **Go** 从零实现的、完整支持 **R7RS Scheme** 的解释器，无任何第三方依赖。

[English](README.md) | **简体中文**

```
$ goscheme -e '(display (map (lambda (x) (* x x)) (list 1 2 3 4))) (newline)'
(1 4 9 16)
```

* **求值核心** —— 显式续延栈的 CEK 抽象机。调用过程时**不压入返回帧**，因此
  **真尾调用（proper tail call）**是结构性的而非模拟出来的；`call/cc` 通过复制
  续延栈实现，因此续延是**多发射（multi-shot）**的。
* **宏系统** —— 卫生的 `syntax-rules`：嵌套省略号、省略号后的尾部模式、自定义
  省略号标识符、省略号转义 `(... template)`，以及模板引入标识符的引用透明性。
* **数值塔** —— 精确整数（`int64` 自动提升为 `big.Int`）、精确有理数、`float64`、
  复数，具备完整的 R7RS 精确性传播规则与可传递的比较。
* **库系统** —— `define-library` / `import`（`only`、`except`、`prefix`、
  `rename`），以及全部 R7RS-small `(scheme …)` 标准库。
* **标准符合性** —— 参考 R7RS 测试套件
  （[chibi-scheme `r7rs-tests.scm`](test/scheme/r7rs-tests.scm)）**全部通过**：
  **1227 条断言，0 失败**。

## 目录

- [快速开始](#快速开始)
- [命令行](#命令行)
- [独立可执行文件](#独立可执行文件)
- [仓库结构](#仓库结构)
- [语言覆盖](#语言覆盖)
- [实现要点](#实现要点)
- [测试](#测试)
- [交叉编译](#交叉编译)
- [环境要求](#环境要求)
- [已知限制](#已知限制)
- [许可证](#许可证)

## 快速开始

```sh
git clone https://github.com/MrXie1109/GoScheme.git && cd GoScheme

make build          # 或者：go build -o .build/goscheme ./cmd/goscheme
make test           # Go 单元测试 + 两个 Scheme 测试套件
make dist           # 交叉编译全部目标平台到 dist/

./.build/goscheme -v   # GoScheme 1.2.1 (R7RS)
```

版本号来自 `cmd/goscheme/VERSION`，并被内嵌进二进制，因此即使直接用 `go build`
也能正确报告版本；`make dist` 另外用 `-ldflags "-X main.version=..."` 打戳。

执行程序、求值表达式或进入交互式 REPL：

```sh
./.build/goscheme program.scm                 # 执行文件
./.build/goscheme -e '(display (+ 1 2))'      # 求值表达式
./.build/goscheme                             # 交互式 REPL
./.build/goscheme -i program.scm              # 载入文件后进入 REPL
```

## 命令行

```
goscheme [选项] [文件] [参数 ...]
goscheme build <脚本> [-o <输出>] [-i <解释器>]

  -e, --eval 表达式    求值表达式（可重复，按顺序求值）
  -i, --interactive    载入文件后进入 REPL
  -q, --quiet          REPL 不打印 banner
  -v, --version        打印版本号后退出
  -h, --help           打印用法
  --                   选项结束；其后的参数作为脚本
```

既没有文件也没有 `-e` 时进入 REPL。新表达式用 `>>> ` 提示，表达式尚未写完时用
`... ` 提示。

在终端下，REPL 会把终端切换到 raw 模式，并提供应有的编辑能力：光标移动
（方向键、Home/End、Ctrl-A/E/B/F）、退格与删除、Ctrl-U/K/W、Ctrl-L 清屏、
Ctrl-C 放弃当前行、Ctrl-D 退出，以及上下方向键的历史记录。同时启用
**bracketed paste（括号粘贴）**：由终端自己标出粘贴的起止，整段粘贴作为**一个
整体**插入到当前行——提示符绝不会插进粘贴内容中间，粘贴内部的换行也不会提前提交。
**只有按下 Enter 才会提交**，因此粘贴进来的程序可以先检查、甚至继续补写。
剪贴板的换行符（CR / CRLF / LF）都会归一化处理，所以粘贴出来的各行会正常换行，
而不会互相覆盖：

```text
>>> (define (f x)
  (* x x))
(f 12)
144
>>> 
```

Ctrl-C 会放弃正在编辑的行；若此时有表达式正在求值，则中止该表达式并回到提示符。
因此一个打错、会永久阻塞的表达式（比如没人会去满足的 `chan-recv!`）只会等待，
而不会把整个会话打死；求值过程中若发生 Go panic，也只会打印一行提示，而不是
堆栈转储。

当 stdin 不是终端（管道或重定向文件）时，不打印 banner、不打印提示符、也不做行
编辑，所以 `echo '(+ 1 2)' | goscheme` 只会输出 `3`。在不支持 raw 模式的平台上，
REPL 会退回到按行读取的实现。

`(command-line)` 是**脚本名后接用户参数**——解释器自身的名字不会出现，因此无论
脚本是被解释执行，还是已经用 `goscheme build` 绑定成可执行文件，
`(cdr (command-line))` 都等于用户参数：

```sh
$ goscheme a.scm 1 2 3     ; (command-line) => ("a.scm" "1" "2" "3")
$ goscheme build a.scm
$ ./a.out 1 2 3            ; (command-line) => ("./a.out" "1" "2" "3")
```

（这是**有意偏离 R7RS** 的：标准把命令名放在最前面。只有去掉它，同一份脚本才能在
两种形态下用同样的方式取参数。）

退出码：正常为 `0`，`(exit n)` 为 `n`，`(exit #f)` 与未捕获的错误为 `1`。

## 独立可执行文件

`goscheme build` 通过**把解释器绑定到脚本上**，把一份脚本变成单个自包含的可执行
文件：它复制解释器、把脚本附加在后面，因此运行它的机器上既不需要 Go，也不需要
goscheme。

```sh
$ goscheme build hello.scm        # 生成 ./a.out，与 C 编译器一致
$ ./a.out world
hello from a bundled program
argv: ("./hello" "world")
```

产物就是一张普通的解释器镜像加一段尾部数据：

```
[ 解释器 ][ 脚本 ][ 脚本名 ][ magic ][ 尾部长度 ]
```

往 ELF / PE / Mach-O 镜像末尾追加数据是无害的——加载器只读它认识的头部、忽略尾巴
——所以这个文件仍然会启动解释器；解释器在启动时检查自己的尾部，发现里面有脚本就
运行脚本，而不是走命令行。整个过程不重新编译，脚本按原样存放，因此产物大小正好是
`解释器 + 脚本 + 尾部`。

* `-o, --output FILE` 指定输出名。默认与 C 编译器一致：当前目录下的 `a.out`；
  若绑定的解释器是 Windows 二进制，则为 `a.exe`。
* `-i, --interpreter FILE` 绑定另一个解释器——一台机器可以借此为另一个平台产出
  可执行文件：`-i dist/goscheme-windows-amd64.exe` 会写出 `.exe`。
* 打包程序的 `(command-line)` 是 `(program arg ...)`，即被调用时的程序名（见上）；
  `include` / `load` 相对可执行文件所在目录解析，所以可以把数据文件与它放在一起
  分发。
* 在 macOS 上，追加数据会让链接器生成的代码签名失效，因此 `goscheme build` 会在
  可用时用 `codesign --force --sign -` 重新做 ad-hoc 签名，做不到时给出警告：
  Apple silicon 拒绝运行被修改过且未签名的二进制。

## 仓库结构

```
cmd/goscheme/             命令行入口
  VERSION                 `-v` 与 REPL banner 使用的版本号
  version.go              内嵌 VERSION，使任何构建方式都能报告版本
  main.go                 文件执行、-e 求值、REPL 主循环
  bundle.go               goscheme build：把脚本绑定到解释器
  lineedit.go             raw 模式行编辑器与 bracketed paste
  term_linux.go           termios raw 模式（Linux）
  term_darwin.go          termios raw 模式（macOS）
  term_other.go           无 raw 模式平台的退化实现
  main_test.go            REPL 与行编辑器回归测试
internal/scheme/          解释器实现
  value.go                运行时对象（符号、序对、字符串、向量、字节向量、
                          过程、记录 …）
  number.go               数值塔与算术
  reader.go               词法分析与数据读取
  printer.go              write / display / write-shared / write-simple
  env.go                  词法环境与卫生标识符解析
  machine.go              CEK 抽象机、续延、dynamic-wind、异常分发
  eval.go                 特殊形式与派生语法
  macro.go                syntax-rules 模式匹配与模板实例化
  equal.go                eq? / eqv? / equal?
  library.go              R7RS 库与 import set
  port.go                 文本 / 二进制 / 字符串 / 字节向量端口
  builtins.go             内建过程注册与参数检查
  b_number.go             数值过程
  b_list.go               序对与列表
  b_string.go             字符串、字符、符号
  b_vector.go             向量与字节向量
  b_control.go            apply、map、续延、多值、Promise
  b_io.go                 端口、read 与 write
  b_system.go             文件、进程上下文、时间、eval 与 load
  b_hashtable.go          哈希表（扩展）
  b_concurrent.go         通道、(go ...)、(select ...)（扩展）
  b_process.go            system 与 system*（扩展）
  scheme_test.go          Go 单元测试与测试套件驱动
test/scheme/              Scheme 层测试
  r7rs-tests.scm          参考 R7RS 测试套件
  goscheme-tests.scm      本实现的回归测试
  goscheme-concurrency-tests.scm
                          通道、线程与 select 测试
  chibi/test.scm          测试套件使用的 (chibi test) 兼容层
  run-r7rs.scm            驱动：goscheme run-r7rs.scm
  run-goscheme.scm
  run-concurrency.scm
examples/                 可直接运行的示例，如 examples/concurrency.scm
dist/                     `make dist` 的产物：发布用二进制，只挂在 GitHub
                          Release 上，不纳入 git 跟踪
scripts/build-dist.sh     `make dist` 使用的交叉编译脚本
Makefile                 构建、测试与打包目标
```

## 语言覆盖

### 语法

`quote` `quasiquote` `unquote` `unquote-splicing` `if` `define` `set!` `lambda`
`case-lambda` `begin` `let` `let*` `letrec` `letrec*` `let-values`
`let*-values` `define-values` `cond` `case` `and` `or` `when` `unless` `do`
`delay` `delay-force` `parameterize` `guard` `define-record-type`
`define-syntax` `let-syntax` `letrec-syntax` `syntax-rules` `include`
`include-ci` `cond-expand` `import` `define-library`

（按照报告要求，`else` 与 `=>` 只有在未被变量绑定遮蔽时才被识别为辅助语法。）

读取器支持完整的 R7RS 词法语法：可嵌套的块注释 `#|…|#`、数据注释 `#;`、
`#!fold-case` / `#!no-fold-case`、向量 `#(…)`、字节向量 `#u8(…)`、数据标签
`#0=` / `#0#`（含环状结构）、可转义的 `|…|` 符号、全部字符名与 `#\xHH`、
支持行内续行的字符串转义，以及带任意 `#b #o #d #x` 与 `#e #i` 前缀组合的
完整数值文法。

### 库

`(scheme base)` `(scheme case-lambda)` `(scheme char)` `(scheme complex)`
`(scheme cxr)` `(scheme eval)` `(scheme file)` `(scheme inexact)`
`(scheme lazy)` `(scheme load)` `(scheme process-context)` `(scheme read)`
`(scheme repl)` `(scheme time)` `(scheme write)` `(scheme r5rs)`

外加扩展库 `(goscheme hash-table)`、`(goscheme channel)` 与 `(goscheme process)`。

### 数据类型

布尔；数值（精确整数、精确有理数、非精确实数、复数）；完整 Unicode 大小写映射
的字符；可变字符串；符号；序对与列表；向量；字节向量；过程（闭包、原语、
续延、参数对象）；Promise；记录类型；错误对象；端口；环境对象；`eof`；
未指定值；哈希表（扩展）；通道（扩展）。

### 过程

标准环境中安装了 323 个绑定（其中约 250 个是 R7RS-small 标准过程），
内建库共导出 572 个名字。覆盖范围包括数值塔（`exact-integer-sqrt`、
`rationalize`、`floor/`、`truncate/`、`make-polar`、任意进制的
`number->string` …）、列表与向量操作、Unicode 感知的字符串与字符操作、
文本与二进制 I/O、文件与进程上下文过程、`eval` / `load` / `environment`、
`dynamic-wind`、`guard`、`with-exception-handler`、`parameterize`、
Promise 与 `values`。

## 实现要点

### 真尾调用

求值机把续延保存为显式的帧栈。求值一个组合式时，只为**尚未求值的操作数**
压入帧；**施加过程本身不压入任何帧**，被调用者因此直接运行在调用者的续延上，
这正是尾调用。尾位置在 `if`、`cond`、`case`、`and`、`or`、`when`、`begin`、
`let`/`let*`/`letrec` 的函数体、`apply`、`call-with-values`、`force`、
`dynamic-wind`、宏展开与 `guard` 中都得到保持。

```sh
$ goscheme -e '(let loop ((i 0)) (if (= i 2000000) i (loop (+ i 1))))'
2000000        # 常量栈空间
```

深层的**非尾**递归同样可用：续延帧分配在堆上，而不占用 Go 调用栈。

### 一等续延

`call/cc` 同时捕获续延栈、`dynamic-wind` 风栈与异常处理器栈，并对它们做复制，
因此捕获到的续延可以被反复调用。恢复续延时，计算当前风栈与目标风栈的最长公共
前缀，逆序执行被退出帧的 `after` 过程（最内层优先）、顺序执行被进入帧的
`before` 过程，然后恢复状态。`guard` 与 `with-exception-handler` 使用同一套
转移逻辑，因此从嵌套 `dynamic-wind` 中退栈时，每个 `after` 过程都会恰好执行
一次且顺序正确。

### 卫生宏

模板引入的标识符带有一个“标记”，该标记记录宏定义时的环境。名字解析首先搜索
词法环境（宏展开引入的绑定因此永远不会捕获用户绑定，反之亦然），失败后再沿
标记链回退解析，从而实现引用透明性。标记可以复合，所以“展开出宏定义的宏”
同样保持卫生。字面量（literals）按标识符身份或绑定比较，而不是简单按名字
比较，这正是 R7RS 4.3.2 所要求的。

### 错误与条件

原语以及 `error`/`raise` 抛出的错误会被分派给最内层的处理器。`raise` 是不可
继续的（处理器返回会触发二次异常），`raise-continuable` 则恢复处理器栈并在
`raise` 处继续执行。`error-object?`、`error-object-message`、
`error-object-irritants`、`read-error?`、`file-error?` 均已支持。

### 数值塔

精确整数在溢出前一直保存在机器字中，溢出后自动提升为 `big.Int`；精确有理数
使用 `big.Rat`，始终保持最简形式，整数值会归一化回整数。精确与非精确操作数
比较时先把非精确操作数转换为精确值，从而保证 `=`、`<` 等的传递性
（R7RS 6.2.6 的建议）。

### 并发（Go 风味）

解释器把 Go 的并发模型直接暴露给 Scheme（见 [`(goscheme channel)`](#库)）：

| 形式 | 含义 |
|---|---|
| `(make-channel)` | 无缓冲通道（一次握手） |
| `(make-channel n)` | 可缓冲 *n* 个值的通道 |
| `(chan-send! ch v)` | 发送；直到有接收者（或缓冲区有空位）才返回 |
| `(chan-recv! ch)` | 接收；返回两个值：值本身与 *ok?* 标志（关闭后为 `#f`） |
| `(chan-close! ch)` | 关闭；重复关闭是空操作 |
| `(channel? obj)` / `(channel-open? ch)` | 谓词 |
| `(go body ...)` | 在新的解释器线程（goroutine）上运行 *body* |
| `(go-wait)` | 等待目前为止启动的所有线程结束 |
| `(select ...)` | 同时竞速多个操作，等价于 Go 的 `select` |

```scheme
(define ch (make-channel))
(go (chan-send! ch 'hello))
(display (chan-recv! ch))            ; 打印 hello

(select
  (chan-recv! ch1)    => (lambda (v) (display "got: ") (display v))
  (chan-send! ch2 42) => (lambda () (display "sent"))
  (after 1000)        => (lambda () (display "timeout"))
  (else)              => (lambda () (display "idle")))
```

`select` 的子句是扁平的 `操作 => 处理函数` 三元组序列：接收子句的处理函数会收到
接收到的值，其余子句不带参数。所有通道表达式、发送值与处理函数都会在竞速开始前
求值；若有操作就绪则按书写顺序选择，`(else)` 只在没有任何操作就绪时被选中——
与 Go 的语义一致。

因为用的是 Go 原语而不是模拟，所以 Go 的规则同样适用：

* **无缓冲**通道是握手，**有缓冲**通道允许发送方先行；因此线程可能取回自己发出的
  缓冲消息，严格的交接协议应当使用无缓冲通道。
* **`(else)` 从不等待**，它把 `select` 变成非阻塞轮询，所以“即将就绪”的子句会被
  错过。
* **`(go-wait)` 等待此前启动的每一个线程**，包括永不返回的服务型循环——这类线程
  要留到最后再启动。
* **所有线程都阻塞的脚本**会被 Go 运行时判定为死锁并打印
  `all goroutines are asleep - deadlock!` 后终止，与 Go 程序的行为相同。
  交互式 REPL 则受保护：永久阻塞的表达式只会等待，**Ctrl-C 可中止它**并回到
  提示符。
* 线程内抛出的错误由该线程自己的处理器处理；未捕获的错误打印到当前错误端口，且
  只结束该线程。
* 续延属于捕获它的线程，跨线程恢复会报错。

解释器自身的共享状态是加锁保护的，因此线程可以自由共享全局环境、端口与参数；
普通 Scheme 数据（序对、字符串、向量、记录）**没有**同步——请通过通信共享内存。

### 扩展

除 R7RS-small 之外，解释器还提供：

* `(goscheme hash-table)` —— `make-eq-hashtable`、`make-eqv-hashtable`、
  `make-equal-hashtable`、`hash-table-ref`、`hash-table-ref/default`、
  `hash-table-set!`、`hash-table-update!`、`hash-table-delete!`、
  `hash-table-exists?`、`hash-table-keys`、`hash-table-values`、
  `hash-table-walk`、`hash-table->alist`、`alist->hash-table`、
  `hash-table-copy`、`hash-table-clear!`、`hash-table-size`、
  `hash-table-count` 与 `hash`。
* `(goscheme channel)` —— `make-channel`、`chan-send!`、`chan-recv!`、
  `chan-close!`、`channel?`、`channel-open?`、`go`、`select`、`go-wait`
  （见上文[并发](#并发go-风味)）。
* `(goscheme process)` —— `(system command)` 把命令行交给系统命令处理器执行
  （Unix 为 `/bin/sh -c`，Windows 为 `cmd /c`）；`(system* program arg ...)`
  直接执行程序、不经 shell。两者都返回精确整数形式的退出状态：正常退出返回退出码，
  被信号杀死返回 `128+信号`（与 shell 的表示一致）；程序无法启动则抛文件错误。
  子进程继承解释器的标准流；在终端下 REPL 会把终端交还给子进程，因此用它启动
  编辑器或 shell 时对方看到的是正常的 cooked 终端。

  ```scheme
  (system "make -j4")                 ; => 0
  (system* "git" "status" "--short")
  (guard (e ((file-error? e) (display "没有这个程序")))
    (system* "/nonexistent"))
  ```
* `(assert expr)`、`#!unspecified`、shebang 行
  （`#!/usr/bin/env goscheme`），以及读取器额外接受的指数标记 `s f d l`。

## 测试

```sh
make test                                     # 全部测试
go test ./...                                 # 同上
go test -short ./...                          # 跳过参考套件
./.build/goscheme test/scheme/run-r7rs.scm    # 仅参考套件
./.build/goscheme test/scheme/run-goscheme.scm
```

```
== 1227 passed, 0 failed     参考 R7RS 套件（test/scheme/r7rs-tests.scm）
==  135 passed, 0 failed     GoScheme 回归套件（test/scheme/goscheme-tests.scm）
==   39 passed, 0 failed     并发套件（test/scheme/goscheme-concurrency-tests.scm）
```

并发套件同样通过 Go 竞态检测器（`go test -race ./...`）。

* `r7rs-tests.scm` 是 chibi-scheme 维护的参考测试套件，覆盖 4.1–4.3 节
  （原始、派生与宏语法）与 6.1–6.14 节（全部标准过程），包含卫生宏的边界
  情形、数值文法、读取器语法与环状输出的处理。它通过内置的 `(chibi test)`
  兼容层运行。
* `goscheme-concurrency-tests.scm` 覆盖通道（缓冲、握手、关闭、`chan-recv!`
  的两个返回值）、`(go ...)`/`go-wait`、工作池、线程内错误处理以及每一种
  `select` 子句。
* `goscheme-tests.scm` 补充回归覆盖：真尾调用、多发射续延、`dynamic-wind`
  的重入与退栈、库导入变换、记录类型、文本/二进制端口、文件往返、
  `include` / `cond-expand`、哈希表、异常以及 `eval` / `load`。
* `internal/scheme/scheme_test.go` 中的 Go 测试驱动上述套件，并包含针对
  读取器、数值塔、尾调用行为与错误传播的直接单元测试。
* `cmd/goscheme/main_test.go` 覆盖 REPL 与行编辑器：括号粘贴视为一个输入单元、
  结束标记跨读取被切断时也能正确处理、编辑键与历史记录行为、管道会话不打印
  banner 与提示符、未以换行结束的输出不会被下一次提示符擦掉。

## 交叉编译

`make dist`（或 `scripts/build-dist.sh`）会把所有目标平台的静态链接可执行文件
输出到 `dist/`，不依赖 cgo，也不需要额外的工具链。

| 产物 | 平台 |
|---|---|
| `goscheme-linux-amd64` | Linux x86-64 |
| `goscheme-linux-arm64` | Linux AArch64 |
| `goscheme-darwin-amd64` | macOS Intel |
| `goscheme-darwin-arm64` | macOS Apple Silicon |
| `goscheme-windows-amd64.exe` | Windows x86-64 |
| `goscheme-windows-arm64.exe` | Windows on ARM |

编译参数：`GOOS=<os> GOARCH=<arch> CGO_ENABLED=0 go build -trimpath -ldflags "-s -w"`。
`dist/SHA256SUMS` 记录每个产物的校验和。

## 环境要求

* Go 1.22 或更高版本（模块声明为 `go 1.22`）。
* 无第三方模块，仅使用标准库。
* 解释器本身可运行在 Linux、macOS 与 Windows 的 amd64 / arm64 平台。

## 已知限制

* `define-syntax` 仅支持 `syntax-rules` 变换器，而这已经是 R7RS-small 宏系统
  的全部内容。
* 目标语言为 R7RS-small；除内置的哈希表扩展外，不提供 R7RS-large 与 SRFI 库。
* 这是树遍历解释器，没有编译器或 JIT。尾调用是真的，但深层非尾递归会分配
  堆上的续延帧。
* 非精确数值采用 Go 的最短往返表示输出；形似数值的符号（例如 `+NaN.0abc`）
  会被 `write` 加 `|…|` 引用。
* 按报告允许的行为，`write-simple` 作用于环状数据时可能不会终止。
* 并发扩展遵循 Go 而不是 R7RS/R6RS 的线程提案：没有互斥量、条件变量，也没有
  线程局部的动态状态，`(go-wait)` 是“等待全部”的粗粒度操作。

## 许可证

MIT，详见 [LICENSE](LICENSE)；每个源文件都带有
`SPDX-License-Identifier: MIT` 标识。

Copyright (c) 2026 MrXie1109。

随仓库附带的参考测试套件 `test/scheme/r7rs-tests.scm` 不属于 GoScheme：它来自
[chibi-scheme](https://github.com/ashinn/chibi-scheme)，仍遵循其自身的
BSD-3-Clause 许可证（文件头部已注明）。
