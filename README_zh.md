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
- [在 Go 程序里嵌入](#在-go-程序里嵌入)
- [示例](#示例)
- [命令行](#命令行)
- [独立可执行文件](#独立可执行文件)
- [仓库结构](#仓库结构)
- [语言覆盖](#语言覆盖)
- [扩展库与 SRFI 接口参考](docs/extensions/README.md)
- [实现要点](#实现要点)
- [性能](#性能)
- [测试](#测试)
- [交叉编译](#交叉编译)
- [环境要求](#环境要求)
- [已知限制](#已知限制)
- [开源倡议](docs/open-source.md)
- [许可证](#许可证)

## 快速开始

```sh
git clone https://github.com/MrXie1109/GoScheme.git && cd GoScheme

make build          # 或者：go build -o .build/goscheme ./cmd/goscheme
make test           # Go 单元测试 + 两个 Scheme 测试套件
make dist           # 交叉编译全部目标平台到 dist/

./.build/goscheme -v   # GoScheme 2.2.0 (R7RS)
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

## 在 Go 程序里嵌入

仓库根包就是作为库的解释器，所以 Go 程序可以用 Scheme 做配置或插件语言：

```go
import goscheme "github.com/MrXie1109/GoScheme"

i := goscheme.New()
i.Define("double", 1, 1, func(args []goscheme.Value) (goscheme.Value, error) {
    n, ok := args[0].Int()
    if !ok {
        return goscheme.Value{}, fmt.Errorf("double: 需要整数")
    }
    return goscheme.Int(n * 2), nil
})

v, _ := i.Eval("(double 21)")           // 42
square, _ := i.Lookup("square")
v, _ = i.Call(square, goscheme.Int(12)) // Go 调用 Scheme 过程
```

全部接口就是 `Eval`、`EvalFile`、`Define`、`Lookup`、`Call`、`SetOutput`、
`SetArgs`；`Value` 提供带类型的取值方法（`Int`、`Float`、`Str`、`Bool`、
`Slice`、`IsNil`、`IsFalse`、`IsProcedure`），读结果不需要写类型分支。宿主函数
返回的 error 会变成普通 Scheme 条件，Scheme 侧可以用 `guard` 捕获。每个 `Interp`
有独立环境，两个解释器互相看不见对方的定义。`examples/embed/main.go` 用到了全部
这些。

## 示例

`examples/` 是一趟能跑起来的方言之旅：数值塔、真尾调用、哈希表、Go 风味并发、
调用外部程序、脚本能看到什么，以及从文件加载库。

```sh
./examples/run-all.sh                    # 全部跑一遍，报告失败的
./.build/goscheme examples/numbers.scm   # 或者一个一个跑
```

每个文件都有注释、并把自己在做什么打印出来，所以它们既是给你跑的也是给你读的；
`examples/README.md` 说明每个示例展示什么。有一个 Go 测试会把它们全部跑一遍，
所以它们不会悄悄失效。其中两个值得单独一提：

* `examples/libraries/main.scm` 从 `lib/greet.sld` 导入 `(lib greet)`，用
  `goscheme build -static` 就能变成一个完全不需要库文件的可执行文件；
* `examples/script-args.scm` 展示 `(command-line)` 的形状、解释器名字为何不在其中，
  以及 `(assert ...)` 与 `#!unspecified` 的行为。

## 命令行

```
goscheme [选项] [文件] [参数 ...]
goscheme build <脚本> [-o <输出>] [-i <解释器>]
goscheme compile <脚本> [-o <输出.scmc>]

  -e, --eval 表达式    求值表达式（可重复，按顺序求值）
  -i, --interactive    载入文件后进入 REPL
  -q, --quiet          REPL 不打印 banner
  -interp              用树遍历解释器运行，而不是字节码虚拟机
  -v, --version        打印版本号后退出
  -h, --help           打印用法
  --                   选项结束；其后的参数作为脚本
```

脚本会被**编译成字节码并在虚拟机上执行**（编译器能处理的部分），处理不了的部分
回落到树遍历解释器；`-interp` 强制使用解释器，这也是两者对照的方式。
`goscheme compile` 把编译结果写成 `.scmc` 文件，而把 `.scmc` 交给解释器会直接加载
运行、不再解析源码。细节见 [docs/bytecode.md](docs/bytecode.md)。

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
文件：它复制解释器、把**编译后**的脚本附加在后面，因此运行它的机器上既不需要 Go，
也不需要 goscheme，启动时也不用再解析源码。

```sh
$ goscheme build hello.scm        # 生成 ./a.out，与 C 编译器一致
$ ./a.out world
hello from a bundled program
argv: ("./hello" "world")
```

产物就是一张普通的解释器镜像加一段尾部数据：

```
[ 解释器 ][ 载荷 ][ 脚本名 ][ magic ][ 尾部长度 ]
```

往 ELF / PE / Mach-O 镜像末尾追加数据是无害的——加载器只读它认识的头部、忽略尾巴
——所以这个文件仍然会启动解释器；解释器在启动时检查自己的尾部，发现里面有程序就
运行程序，而不是走命令行。用户机器上不重新编译，产物大小正好是
`解释器 + 载荷 + 尾部`。

载荷是**编译后的字节码**（构建期写好）；若本机编译不了（例如脚本导入的库只在运行时
才在可执行文件旁边），则退回存放源码，`goscheme build` 会把这一点说出来。两种情况
下，编译器不接受的部分仍然在树遍历器上运行，与从文件运行完全一致。

* `-o, --output FILE` 指定输出名。默认与 C 编译器一致：当前目录下的 `a.out`；
  若绑定的解释器是 Windows 二进制，则为 `a.exe`。
* `-i, --interpreter FILE` 绑定另一个解释器——一台机器可以借此为另一个平台产出
  可执行文件：`-i dist/goscheme-windows-amd64.exe` 会写出 `.exe`。
* `-static` 把库一起烘进去：脚本导入的所有库、以及这些库 `include` 的内容，都在
  **构建期**解析出来并写在脚本前面，因此可执行文件旁边不需要任何库文件；库找不到
  会在**构建期**报错，而不是到用户机器上才出意外。不加 `-static` 时，打包程序会在
  可执行文件旁边查找库（见[从文件加载库](#从文件加载库)）。
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
  bundle.go               goscheme build：把编译后的脚本绑定到解释器
  static.go               goscheme build -static：构建期解析全部库
  ffi_cgo.go / ffi_stub.go  load-shared-library 与 foreign-function
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
  vm.go                   字节码虚拟机：指令、帧、变量 cell
  compile.go              字节码编译器：语言里的每个表单
  bytecode.go             .scmc 文件格式的读写
  b_system.go             文件、进程上下文、时间、eval 与 load
  b_hashtable.go          哈希表（扩展）
  b_concurrent.go         通道、(go ...)、(select ...)（扩展）
  b_sync.go               互斥锁、等待组、once、原子计数器（扩展）
  b_socket.go             TCP 监听器与连接（扩展）
  b_http.go               HTTP 客户端与服务器（扩展）
  b_json.go               JSON（扩展）
  b_regexp.go             正则表达式（扩展）
  b_time.go               睡眠、时钟、时间格式化（扩展）
  b_fs.go                 glob、目录遍历、路径（扩展）
  b_process.go            system、system* 与进程管道（扩展）
  b_match.go              match 特殊形式（扩展）
  b_fast*.go              (goscheme fast) 库（扩展）
  ffi_cgo.go              load-shared-library 与 foreign-function（扩展）
  scheme_test.go          Go 单元测试与测试套件驱动
  docs_test.go            检查 docs/extensions 覆盖每个导出名
test/scheme/              Scheme 层测试
  r7rs-tests.scm          参考 R7RS 测试套件
  goscheme-tests.scm      本实现的回归测试
  goscheme-concurrency-tests.scm
                          通道、线程与 select 测试
  chibi/test.scm          测试套件使用的 (chibi test) 兼容层
  run-r7rs.scm            驱动：goscheme run-r7rs.scm
  run-goscheme.scm
  run-concurrency.scm
examples/                 可直接运行的示例与运行脚本（见 examples/README_zh.md）
dist/                     `make dist` 的产物：发布用二进制，只挂在 GitHub
                          Release 上，不纳入 git 跟踪
scripts/build-dist.sh     `make dist` 使用的交叉编译脚本
scripts/scmc-disassemble.scm
                          用 GoScheme 写的 .scmc 反汇编器：打印编译文件里有什么
test/bytecode/v4.scmc     一份旧字节码版本的文件，留在这里让 `make check-disasm`
                          能证明旧文件仍读得进来
docs/extensions/         每个 (goscheme ...) 库一页接口参考
docs/srfi/               每个 (srfi N) 库一页接口参考
docs/ffi-design.md       (goscheme ffi) 的设计说明
docs/development.md      本地构建、测试与交叉编译
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
支持行内续行的字符串转义（另加 `\e`、不带分号的 `\xHH`、以及 C 风格八进制
`\NNN`，所以 `"\033[31m"` 就是它看起来的意思），以及带任意 `#b #o #d #x` 与 `#e #i` 前缀组合的
完整数值文法。

### 库

`(scheme base)` `(scheme case-lambda)` `(scheme char)` `(scheme complex)`
`(scheme cxr)` `(scheme eval)` `(scheme file)` `(scheme inexact)`
`(scheme lazy)` `(scheme load)` `(scheme process-context)` `(scheme read)`
`(scheme repl)` `(scheme time)` `(scheme write)` `(scheme r5rs)`

外加扩展库 `(goscheme hash-table)`、`(goscheme channel)`、`(goscheme fast)` 与 `(goscheme process)`。

### 从文件加载库

不是内置的库会**从搜索路径加载**，因此程序可以导入自己文件里的库。名字
`(lib greet)` 对应 `lib/greet.sld`（也接受 `.scm`、`.sls`、`.ss`），按以下顺序查找：

1. 加载路径，最内层优先——正在执行的脚本所在目录，以及通过 `load` 或 `include`
   到达的每个文件所在目录；
2. `GOSCHEME_LIBRARY_PATH` 中的目录（用平台的路径分隔符分隔）；
3. 当前工作目录。

```scheme
;; lib/greet.sld
(define-library (lib greet)
  (export greet)
  (import (scheme base))
  (begin (define (greet who) (string-append "hello " who))))
```

```scheme
;; main.scm
(import (scheme base) (scheme write) (lib greet))
(display (greet "world")) (newline)
```

库在导入时按需加载，并且是**递归**的：一个库导入另一个库会把它一并拉进来；
库文件里的 `include` 相对定义它的文件解析。库之间形成环会**报错**而不是无限递归；
文件里定义的库与导入请求的名字不符同样是错误。加载过程中抛出的错误就是普通条件，
所以把 `import` 放进 `guard` 里可以捕获。

`cond-expand` 的 `(library ...)` 要求会同时检查库是否已注册**或**在搜索路径上找得到。
用 `goscheme build` 打包的程序会在可执行文件旁边查找，因此库可以随它一起分发。

### 数据类型

布尔；数值（精确整数、精确有理数、非精确实数、复数）；完整 Unicode 大小写映射
的字符；可变字符串；符号；序对与列表；向量；字节向量；过程（闭包、原语、
续延、参数对象）；Promise；记录类型；错误对象；端口；环境对象；`eof`；
未指定值；哈希表（扩展）；通道（扩展）。

### 过程

标准环境中安装了 323 个绑定（其中约 250 个是 R7RS-small 标准过程），
标准环境里有 469 个绑定，28 个内建库共导出 676 条名字（去重后 469 条，因为
`(scheme …)` 各库会重复导出同一批）。覆盖范围包括数值塔（`exact-integer-sqrt`、
`rationalize`、`floor/`、`truncate/`、`make-polar`、任意进制的
`number->string` …）、列表与向量操作、Unicode 感知的字符串与字符操作、
文本与二进制 I/O、文件与进程上下文过程、`eval` / `load` / `environment`、
`dynamic-wind`、`guard`、`with-exception-handler`、`parameterize`、
Promise 与 `values`。

## 实现要点

### 两条执行路径

语言里的**每个表单**都会编译成字节码在栈式虚拟机上执行：表单本身是语法糖的（`do`、
`let-values`、反引号），编译器就调用解释器自己的展开器再编译展开结果；需要在运行期
做点什么的（`guard`、`match`、`select`、`go`、记录类型），就把周围的片段编译成过程
交给一个助手。因此两条路径是"构造上一致"，而不是"互相模仿"——模式怎么匹配、哪条子句
胜出，用的都是解释器那一份代码。宏在编译前展开，尾调用有自己的指令，而在编译代码里
捕获的续延之所以可用，是因为虚拟机的帧与解释器的帧一样——写一次、永不修改。
指令集、`.scmc` 文件格式与实测数字在 [docs/bytecode.md](docs/bytecode.md)，逐字节的
格式与虚拟机执行一次调用的细节在
[docs/bytecode-internals.md](docs/bytecode-internals.md)。

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
| `(channel? obj)` / `(chan-open? ch)` | 谓词 |
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
求值。当多个子句同时就绪时，胜出者是**随机**选出的，与 Go 的 `select` 完全一致；
书写顺序决定的是求值顺序，而不是哪个就绪子句获胜。`(else)` 只在没有任何操作就绪时
被选中。

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

下面每个库都在 [`docs/extensions/`](docs/extensions/README.md) 有一页速查参考，
SRFI 库则在 [`docs/srfi/`](docs/srfi/README.md)，都列出全部导出过程、参数与语义；
这里只给概览。

* `(goscheme fast)` —— 158 个过程，专治 Scheme 干得慢的活，全部用 Go 实现。
  每一个都把循环、索引、复制与排序留在 Go 里，而不是每个元素走一步解释器：

  * 排序 —— `sort`、`sort!`、`sort-by`、`vector-sort`、`vector-sort-by`、
    `vector-binary-search`、`vector-binary-search-insert`
  * 序列 —— `iota`、`vector-iota`、`range`、`take`、`drop`、`take-right`、
    `drop-right`、`split-at`、`last`、`chunk`、`vector-take`、`vector-drop`、
    `vector-chunk`、`vector-concat`、`vector-reverse`、`vector-reverse!`、
    `vector-swap!`
  * 选择 —— `filter`、`filter-not`、`vector-filter!`、`count`、`any`、
    `every`、`find`、`list-index`、`delete`、`delete-duplicates`、
    `partition`、`vector-partition`、`vector-index-of`、`zip`、`unzip`、
    `flatten`
  * 折叠 —— `fold-left`、`fold-right`、`assoc-set`
  * 聚合 —— `sum`、`product`、`min-of`、`max-of`、`vector-dot`、
    `vector-norm`、`vector-argmin`、`vector-argmax`、`mean`、`median`、
    `percentile`、`variance`、`stddev`、`mode`
  * 批量算术 —— `vector-add`、`vector-sub`、`vector-mul`、`vector-div`、
    `vector-scale`、`vector-negate`、`vector-abs`、`vector-clamp`、
    `vector-prefix-sum`——前八个都有原地版本（名字带 `!`，写回调用者的向量）
    ——外加 `vector-equal?` 与 `vector-compare`
  * 数论 —— `bit-and`、`bit-or`、`bit-xor`、`bit-not`、`bit-shift`、
    `bit-count`、`integer-length`、`expt-mod`、`isqrt`、`prime?`、`primes`、
    `factor`、`clamp`、`sign`
  * 字符串 —— `string-split`（可给上限）、`string-join`、`string-contains`、
    `string-index`、`string-index-from`、`string-last-index`、
    `string-find-all`、`string-count`、`string-prefix?`、`string-suffix?`、
    `string-prefix-ci?`、`string-suffix-ci?`、`string-trim`、
    `string-trim-left`、`string-trim-right`、`string-replace`、
    `string-replace-first`、`string-pad-left`、`string-pad-right`、
    `string-pad-center`、`string-reverse`、`string-repeat`、`string-chunk`、
    `string-sort`、`string-take`、`string-drop`、`string-fields`、
    `string-lines`、`string-titlecase`、`string-upper`、`string-lower`、
    `string-blank?`、`string-empty?`、`string-chomp`、`string-integer?`、
    `string-byte-length`
  * 字节与哈希 —— `sha256`、`sha1`、`sha512`、`md5`、`hmac-sha256`、
    `crc32`、`random-bytes`、`hex-encode`、`hex-decode`、`base64-encode`、
    `base64-decode`、`base64url-encode`、`base64url-decode`、
    `base32-encode`、`base32-decode`、`bytes-xor`、`bytes-and`、
    `bytes-or`、`bytes-not`、`bytes-reverse`、`bytes-index`、
    `bytevector-fill!`、`vector->bytevector`、`bytevector->vector`
  * 随机 —— `random-int`、`random-float`、`random-string`、`random-choice`、
    `shuffle`、`vector-shuffle!`、`vector-sample`、`uuid`

  名字在 R7RS 有对应时保持 R7RS 风格（`sort` 可选 `less?`、`string-split`
  可选分隔符），没有对应时另起不冲突的名字，所以 `(goscheme fast)` 与
  `(scheme base)`、`(scheme char)` 一起导入永远不会撞名。当 `less?` 或谓词是
  内建过程（`<`、`string<?`、`even?`、`string?`）时**不会**回调进 Scheme；
  自己写的谓词仍要为每个元素付一次调用，那种情况下这个库提供的是方便而不是
  速度。`delete-duplicates` 与 `assoc-set` 同样可传比较器，带 `!` 的过程返回
  它写入的那个向量。实测数字见[性能](#性能)；`examples/fast.scm`
  会自己计时，然后展示整个库。

* `(goscheme sync)` —— 并发故事的另一半：`make-mutex`、`mutex-lock!`、
  `mutex-unlock!`、`with-mutex`（无论 body 怎样离开都会释放锁）、
  `make-waitgroup` 与 `waitgroup-add!`/`-done!`/`-wait`、`make-once` 与
  `once-run!`，无锁的 `make-atomic` 计数器，以及配套的 `mutex?`、
  `waitgroup?`、`once?`、`atomic?` 谓词。注意：解锁一个本线程并未持有的互斥量是
  Go 运行时的致命错误，而不是一个可捕获的条件（见[已知限制](#已知限制)）。
* `(goscheme socket)` —— `(tcp-listen port [host])`、`(tcp-accept listener
  [mode])`、`(tcp-connect host port [mode])`、`tcp-listener-port`、
  `tcp-listener-address`、`tcp-listener?`、`tcp-address`、
  `tcp-close-listener`。连接就是双向的普通端口，所以 `read-line` 与
  `write-string` 就是全部协议词汇，而 `(go ...)` 让监听器变成服务器。host 默认
  是回环地址；mode 是 `'textual` 或 `'binary`。
* `(goscheme http)` —— `http-get`、`http-post`、`http-put`、`http-delete`、
  `http-head` 返回正文；`http-request` 返回整个响应（`http-response-status`、
  `-body`、`-header`、`-content-type`）。`(http-serve port handler [host])`
  启动服务器，handler 就是接收请求的普通过程（`http-request-method`、`-path`、
  `-query`、`-header`、`-body`）；net/http 为每个请求开一个解释器线程，handler
  抛错会变成 500 而不是整个进程崩掉。能带正文的方法签名是
  `(url [body [headers]])`：第二个位置是字符串就是正文，是 alist 就是请求头；
  交给 handler 的请求正文最多读取 8 MiB。
* `(goscheme json)` —— `json-parse` 与 `json-write`。对象是字符串键的 `equal?`
  哈希表，数组是向量，JSON 的 `null` 是符号 `null`；整数字面量无论多少位都保持精确。
  写出方向 `json-write` 同样只把字符串键的哈希表当作对象（不接受 alist），并且
  键按排序输出，因此往返一次不会保留插入顺序。
* `(goscheme regexp)` —— `regexp`、`regexp-match`（含捕获组）、`regexp-match?`、
  `regexp-match-positions`、`regexp-replace`（可用 `$1`）、`regexp-replace-all`、
  `regexp-split`，底层是 Go 的 RE2 引擎。这些过程也都接受裸的模式字符串（在当次
  调用中现场编译）；另外 `regexp-match-positions` 返回的下标是**字节**偏移而不是
  字符下标。
* `(goscheme time)` —— `sleep`、`current-millisecond`、`monotonic-millisecond`、
  `time-format`、`time-parse`、`time-utc-parts`。时长以毫秒为单位，与
  `(after ms)` 一致；`time-format`/`time-parse` 的布局串是 Go 的参考布局
  （`2006-01-02 15:04:05`）而不是 `strftime`；`time-utc-parts` 是七对
  `(year month day hour minute second weekday)` 的关联表。
* `(goscheme fs)` —— `glob`、`directory-walk`、`directory-list`、
  `create-directory`、`create-directory-tree`、`delete-directory`、
  `delete-directory-tree`、`path-join`、`path-directory`、`path-base`、
  `path-extension`、`path-absolute`、`file-size`。
* `(goscheme match)` —— 模式匹配：`_`、符号（做绑定）、`(quote datum)`、字面量、
  `()`、`(p ... . rest)`、`#(p ...)`，以及 `(and ...)`、`(or ...)`、`(not ...)`。
  一个 clause 写作 `(pattern body ...)` 或 `(pattern (guard test) body ...)`——
  guard 跟在 pattern 后面，不在它里面：`((n (guard #t)) body)` 是在匹配一个二元
  列表。`else` 是 `_` 的另一种写法；同一个模式里重复出现的名字会静默地以最后一次
  绑定为准，而不要求两者相等。
* `(goscheme process)` —— `popen` 那一对，直接建立在 `os/exec` 上（不是
  `system`）：`(open-input-process program arg ...)` 是子进程输出的端口，
  `(open-output-process program arg ...)` 是其输入的端口，
  `(process-status port)` 在端口关闭后给出退出状态（关闭前是 `#f`）。两个用循环
  接起来就是管道。无法启动的程序会让 `system*` 与 `open-*-process` 抛出文件错误，
  而 `system` 经过 shell，因此找不到命令时得到的是 shell 自己的退出状态 127。

除 R7RS-small 之外，解释器还提供：

* `(goscheme hash-table)` —— `make-eq-hashtable`、`make-eqv-hashtable`、
  `make-equal-hashtable`、`make-hash-table`、`hash-table-ref`、
  `hash-table-ref/default`、`hash-table-set!`、`hash-table-update!`、
  `hash-table-delete!`、`hash-table-exists?`、`hash-table-contains?`、
  `hash-table-keys`、`hash-table-values`、
  `hash-table-walk`、`hash-table->alist`、`alist->hash-table`、
  `hash-table-copy`、`hash-table-clear!`、`hash-table-size`、
  `hash-table-count`、`hash`，以及 `hash-table?` / `hashtable?` 谓词。
  `make-hash-table` 还可以给一个大小提示与等价性（写出 `eq?`、`eqv?`、`equal?`
  的符号或过程）；`alist->hash-table` 会填充你传入的那张表，而不是总新建一张；
  `hash-table-update!` 可选的第四个参数是“键不存在时使用的值”，不是 thunk。
* `(srfi 1)` —— 列表库：`fold`、`unfold`、`reduce`、`take-while`、`span`、
  `delete-duplicates`、`lset-` 系列集合运算以及 SRFI-1 的其余 API。它与
  `(goscheme fast)` 重叠的十六个名字——`filter`、`take`、`drop`、`iota`、`zip`、
  `any`、`every` 等——在两个库里是**同一个绑定**，所以同时导入也不会互相矛盾。
* `(srfi 2)`、`(srfi 8)`、`(srfi 26)` 与 `(srfi 111)` —— `and-let*`、
  `receive`、`cut`/`cute` 以及 box（`box`、`unbox`、`set-box!`、`box?`）。
  三个宏用 Scheme 写成并内嵌进二进制，因此 `goscheme build` 出的可执行文件里也能用。
* `(srfi 128)` —— 比较器：`make-comparator`、`=?`/`<?`/`>?`/`<=?`/`>=?` 链、
  `comparator-if<=>`、现成的 `eq?`/`eqv?`/`equal?` 比较器、
  `make-default-comparator`，以及包括大小写不敏感版本在内的哈希函数；
  `hash-bound` 与 `hash-salt` 是参数。
* `(srfi 133)` —— 向量库：`vector-unfold`、`vector-fold`、`vector-map!`、
  `vector-count`、`vector-index`、`vector-skip`、`vector-any`/`vector-every`、
  `vector-partition`、`subvector`、`vector-concatenate`、
  `vector-append-subvectors` 等。R7RS 与 `(goscheme fast)` 已经定义的名字
  （`vector-copy`、`vector-map`、`vector-swap!`、`vector-binary-search` …）
  是**同一个绑定**，所以三者同时导入也不会互相矛盾。
* 每个库在 [`docs/srfi/`](docs/srfi/README.md) 有一页参考，
  [`docs/manual/`](docs/manual/README.md) 是把它们串起来的教程。

* `(goscheme channel)` —— `make-channel`、`chan-send!`、`chan-recv!`、
  `chan-close!`、`chan-open?`、`channel?`、`nil-channel`、`nil-channel?`、
  `go`、`select`、`go-wait`（见上文[并发](#并发go-风味)）。`nil-channel`
  就是 Go 的 nil channel：永远不就绪，所以 `(set! ch (nil-channel))` 能把一个
  子句永久移出 `select` 的竞争——关闭它做不到这点，因为**已关闭**的通道永远就绪。
* `(continue)` —— 在 `do` 的 body 里放弃 body 的剩余部分，直接进入该循环的步进
  表达式（步进照常执行），即 Go 的 `continue`。与 Go 不同，它是动态的而非词法的，
  因此 body 调用的过程里也能用；实现上它抛出一个私有条件，所以 body 里"什么都抓"
  的 `guard` 会拦截住它。在循环外使用它是一个普通的、可捕获的条件。
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
* `(goscheme ffi)` —— 加载共享库并调用其中的 C 函数：

  ```scheme
  (define libm (load-shared-library "libm.so.6"))
  (define cbrt (foreign-function libm 'cbrt 'double 'double))
  (cbrt 27.0)                                    ; => 3.0000000000000004

  (define strlen (foreign-function (load-shared-library #f) 'strlen 'long 'string))
  (strlen "hello")                               ; => 5
  ```

  `(load-shared-library name)` 打开一个库；`#f` 表示当前程序自身的符号表（libc 就在
  那里）。`(foreign-function lib name 返回类型 参数类型 ...)` 返回一个调用该符号的
  过程。类型有 `void`、`int`/`long`、`double`、`string`（C 的 `char *`）与
  `pointer`；参数必须**要么全为整数类、要么全为 double**，最多 4 个整数类参数或
  3 个 double 参数。返回类型必须与 C 函数真正返回的类型一致：写错并不会做转换，
  而是按该类型重新解释返回寄存器，`void` 返回值则是未指定值。库或符号找不到、
  参数类型写错，都是普通条件。

  加载库是唯一随平台不同的部分：POSIX 用 `dlopen`，Windows 用
  `LoadLibrary`/`GetProcAddress`，其中 `#f` 表示当前可执行文件本身。Windows 上 C 库
  并不在程序自身的导出表里，所以要显式给出库名——`ucrtbase.dll`（`strlen`、`cbrt`
  等）或 `kernel32.dll`（Windows API）。

  `string` 返回值会被复制成 Scheme 字符串；`pointer` 返回值就是地址本身（精确
  整数），空指针是 `#f`。字符串参数只在这次调用期间有效，所以函数返回的指针如果
  *指向自己的某个参数*（`strchr` 就是这样），拿到手时已经悬空 —— 只有当这块内存
  由 C 自己持有时（例如 `getenv` 返回的指针），才能把它再传回 C。

  这需要 **cgo**，所以它在 `-dynamic` 产物里、以及你自己用 `CGO_ENABLED=1` 构建的
  版本里可用；可用时 `(features)` 会包含 `ffi`。静态产物里这些名字仍然存在，调用时
  会给出明确提示，告诉你该换哪个二进制。

  加载共享库是动态加载器的职责，静态链接的程序里没有它可用：在 ELF 平台上，
  FFI 与静态链接无法兼得，换任何工具链都一样。`docs/ffi-design.md` 记录了得出
  这个结论的实测过程（musl 与 glibc 各自的行为），以及将来上游要变成什么样才有
  可能实现。
* `(assert expr)`、`#!unspecified`、shebang 行
  （`#!/usr/bin/env goscheme`），以及读取器额外接受的指数标记 `s f d l`。

## 性能

它是树遍历解释器（显式续延栈），所以不是最快的 Scheme；下面是这个设计取舍的代价，
可用 `go test ./internal/scheme -bench BenchmarkPrograms` 复现（本机为 12 代 i3）。

| 程序 | 耗时 |
|---|---|
| `(fib 22)`，约 2.8 万次调用 | ~51 ms |
| 尾循环 50 万次 | ~0.58 s |
| 20 万次加法与比较 | ~0.31 s |
| 构造 2 万元素列表 | ~34 ms |
| 20 万次闭包创建与调用 | ~260 ms |

让机器跑得快主要是三件事：一次调用不再把操作数复制成切片（帧改为沿参数表行走，
已收集的值放在帧内的小数组里）；小整数做缓存，比较不再经过 `big.Rat`；环境帧的前
四个绑定放在自己的字段里，不再为此分配 map。三者合起来约为 2.1.1 的两倍。

把慢活搬进 Go 的就是 `(goscheme fast)`。用 `go test ./internal/scheme -bench
BenchmarkFast` 测得，每一对的输入用同样方式构造，因此差异只来自被比较的那一步：

| 任务 | Scheme 写法 | `(goscheme fast)` |
|---|---|---|
| 归并排序 1500 个数 | ~58 ms | ~0.9 ms |
| 用 `even?` 筛选 2 万个数 | ~40 ms | ~2.3 ms |
| 构造 2 万元素列表 | ~32 ms | ~1.7 ms |
| 在 2 万字符里找 `"xxxy"` | ~56 ms | ~1.4 ms |
| 两个 2 万元素向量相加 | ~61 ms | ~3.3 ms |
| 4000 个元素去重 | ~62 ms | ~4.7 ms |
| 筛出 3 万以内的素数 | ~1.26 s | ~0.9 ms |

向量过程就是批量车道：整段向量一次 Go 遍历，还有不分配任何内存的原地版本，
而不是每个元素走一步解释器。这背后没有 SIMD，省掉的是逐元素的类型检查、
中间结果的装箱，以及那一步解释本身。

例外是用 Scheme 写的谓词：`filter` 必须逐元素调用它，开销与手写循环相当，所以只有
内建谓词能胜任时这套接口才划算。

**字节码虚拟机**的收益比这更大，而且它已经是默认执行路径。在一组十二个程序的"面板"
上（调用与算术、闭包、全局/局部变量密集的循环、列表、向量、字符串、高阶函数、一个小
求值器、归并排序、尾循环、`call/cc` 重入），它比树遍历解释器**快 1.33–4.99 倍**
（几何平均 3.0 倍），分配内存**少 82%**：绑定在编译期就解析成 (depth, slot)；宏只
展开一次而不是每次求值都展开；`+`、`car` 这类内建过程调用完全不建续延帧；调用编译过
的过程时直接**替换**当前活动而不是递归，因此一百万层非尾递归在 Go 栈上只占两帧。它
还带来"编译一次、存起来"的能力：`goscheme compile script.scm` 写出 `.scmc` 文件，
运行时**不再解析源码**。表格与注意事项在
[docs/bytecode.md](docs/bytecode.md)；字节码的逐字节格式、指令集、虚拟机执行一次
调用时究竟做了什么，以及实测差异，在
[docs/bytecode-internals.md](docs/bytecode-internals.md)。

帧只在**确实存在多个解释器线程**时才加锁，因此常见的单线程场景没有锁开销；
`(go ...)` 与 HTTP handler 会在启动前把计数加上，共享环境照旧受锁保护。

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
==  244 passed, 0 failed     GoScheme 回归套件（test/scheme/goscheme-tests.scm）
==   83 passed, 0 failed     并发套件（test/scheme/goscheme-concurrency-tests.scm）
==   42 passed, 0 failed     网络套件（test/scheme/goscheme-network-tests.scm）
==   35 passed, 0 failed     进程与文件系统套件（test/scheme/goscheme-process-tests.scm）
==   86 passed, 0 failed     数据套件（test/scheme/goscheme-data-tests.scm）
==   43 passed, 0 failed     模式匹配套件（test/scheme/goscheme-match-tests.scm）
==  462 passed, 0 failed     性能库套件（test/scheme/goscheme-fast-tests.scm）
==  175 passed, 0 failed     SRFI-1 套件（test/scheme/srfi-1-tests.scm）
==   47 passed, 0 failed     小型 SRFI 套件（test/scheme/srfi-small-tests.scm）
==   73 passed, 0 failed     SRFI-133 套件（test/scheme/srfi-133-tests.scm）
==   94 passed, 0 failed     SRFI-128 套件（test/scheme/srfi-128-tests.scm）
```
合计 2611 条断言；goscheme 套件在 cgo 构建下是 244 条而不是 229 条，因为 FFI
那一段只在有 FFI 的构建里运行。

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

`make dist`（或 `scripts/build-dist.sh`）产出六个**静态**产物到 `dist/`，发布里带的
就是这些。**动态**风味（`CGO_ENABLED=1`、链接平台 C 库、唯一带 `(goscheme ffi)` 的
那种）由同一个脚本在"目标平台有 C 编译器"时构建（`DYNAMIC_PLATFORMS`，用
`CC_<os>_<arch>` 指定编译器），CI 会为 macOS 与 Windows 构建它；这些**不随发布附带**。

| 产物 | 平台 | 风味 |
|---|---|---|
| `goscheme-linux-amd64` | Linux x86-64 | 静态 |
| `goscheme-linux-arm64` | Linux AArch64 | 静态 |
| `goscheme-darwin-amd64` | macOS Intel | 静态 |
| `goscheme-darwin-arm64` | macOS Apple Silicon | 静态 |
| `goscheme-windows-amd64.exe` | Windows x86-64 | 静态 |
| `goscheme-windows-arm64.exe` | Windows on ARM | 静态 |

**静态**产物是 `CGO_ENABLED=0`：运行时无依赖、也没有 FFI，`(features)` 不含
`ffi`。**动态**产物是 `CGO_ENABLED=1`，链接平台自身的 C 库，是唯一能加载共享库的
风味，也就是带 `(goscheme ffi)` 的那一个；它运行时需要该 C 库，而这里的 Linux 产物
是针对 glibc 2.34 构建的，所以需要不低于该版本的 glibc。

darwin 的动态产物与 Windows 那个由 `.github/workflows/ci.yml` 在**各自平台上**构建，
这也是 macOS 唯一的验证方式：Mach-O 二进制无法在开发这台机器上执行，所以 CI 在
macOS、Linux 与 Windows 上原生跑测试。

动态构建需要**目标平台的** C 编译器，所以默认列表只列本机能构建的目标：两个 Linux
目标，以及可配 mingw 的 windows/amd64；如果你有对应的
交叉编译器，把其它目标写进 `DYNAMIC_PLATFORMS` 即可，每个目标的编译器可用
`CC_<os>_<arch>` 覆盖，找不到编译器时会给出提示并跳过该目标，而不是让整轮构建失败：

```sh
DYNAMIC_PLATFORMS="linux/amd64" make dist
CC_windows_amd64=x86_64-w64-mingw32-gcc make dist
```

编译参数：`GOOS=<os> GOARCH=<arch> CGO_ENABLED=<0|1> go build -trimpath -ldflags "-s -w"`。
`dist/SHA256SUMS` 记录每个产物的校验和。

## 环境要求

* Go 1.22 或更高版本（模块声明为 `go 1.22`）。
* 无第三方模块，仅使用标准库。
* 解释器本身可运行在 Linux、macOS 与 Windows 的 amd64 / arm64 平台。

## 已知限制

* `define-syntax` 仅支持 `syntax-rules` 变换器，而这已经是 R7RS-small 宏系统
  的全部内容。
* 目标语言为 R7RS-small。R7RS-large 整体不提供；R7RS-small 之外提供的是内置
  哈希表扩展与[扩展](#扩展)小节列出的 SRFI（目前是 SRFI-1、2、8、26、111、128、133）。
* 这是树遍历解释器，没有编译器或 JIT。尾调用是真的，但深层非尾递归会分配
  堆上的续延帧。
* 非精确数值采用 Go 的最短往返表示输出；形似数值的符号（例如 `+NaN.0abc`）
  会被 `write` 加 `|…|` 引用。
* 按报告允许的行为，`write-simple` 作用于环状数据时可能不会终止；`display` 也不带
  已访问集合，所以在 `write` 能活下来的循环结构上它可能挂死。
* 并发扩展遵循 Go 而不是 R7RS/R6RS 的线程提案：没有线程局部的动态状态，也没有条件
  变量，`(go-wait)` 是“等待全部”的粗粒度操作。不过 `(goscheme sync)` 提供了 Go
  程序员习惯的互斥量、等待组、一次性执行与原子量。
* 导入的绑定是**副本**：库里用 `set!` 改自己的变量、导入方读到的可能分叉；对导入
  名字 `set!` 也只改导入方那一份。chibi-scheme 行为相同，R7RS 原文在这一点上有歧义，
  但在依赖它之前值得知道。
* 单值上下文里的多值会被静默截断为第一个（零个值则变成未指定值），而不是报错。
* 有两种误用会**中止进程**而不是抛出条件，`guard` 拦不住：对并非本线程持有的互斥量
  调用 `mutex-unlock!` 会触发 Go 运行时的致命错误；`select` 里 `(after ms)` 的
  `ms` 为负数或不是精确整数时，会在求值该子句时 panic。

## 许可证

MIT，详见 [LICENSE](LICENSE)；每个源文件都带有
`SPDX-License-Identifier: MIT` 标识。

Copyright (c) 2026 MrXie1109。

随仓库附带的参考测试套件 `test/scheme/r7rs-tests.scm` 不属于 GoScheme：它来自
[chibi-scheme](https://github.com/ashinn/chibi-scheme)，仍遵循其自身的
BSD-3-Clause 许可证（文件头部已注明）。
