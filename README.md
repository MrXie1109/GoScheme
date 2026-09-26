# GoScheme

GoScheme 是一个用 **Go** 从零实现的 **R7RS Scheme** 解释器（R7RS-small 全部语言）。

- 求值核心：显式栈的 **CEK 抽象机**，天然支持**真尾调用（proper tail calls）**与**一等续延**（`call/cc` 可多次调用）。
- 宏系统：**`syntax-rules`**，支持嵌套省略号、尾部模式、自定义省略号、省略号转义、卫生（referential transparency + 不捕获用户绑定）。
- 数值塔：精确整数（自动 `int64` ↔ `big.Int` 提升）、精确有理数、`float64`、复数，以及完整的精确/非精确传播规则。
- 库系统：`define-library` / `import`（`only` / `except` / `prefix` / `rename`）、全部标准库 `(scheme *)`。
- 无第三方依赖，纯标准库实现，可静态交叉编译。

参考 R7RS 测试套件（chibi-scheme 的 `r7rs-tests.scm`，1227 条断言）**全部通过**。

```
== 1227 passed, 0 failed
```

## 目录结构

```
cmd/goscheme/          命令行入口（文件执行 / -e 求值 / REPL）
internal/scheme/       解释器实现
  value.go             运行时对象（符号、序对、字符串、向量、字节向量、过程、记录…）
  number.go            数值塔与算术
  reader.go            词法分析 + 数据读取（S 表达式）
  printer.go           write / display / write-shared / write-simple
  env.go               词法环境与卫生标识符解析
  machine.go           CEK 抽象机、续延、dynamic-wind、异常分发
  eval.go              特殊形式与派生语法
  macro.go             syntax-rules 模式匹配与模板实例化
  equal.go             eq? / eqv? / equal?
  library.go           R7RS 库与 import set
  port.go              文本/二进制端口、字符串端口、字节向量端口
  builtins.go         内建过程注册与参数检查
  b_number.go         (scheme inexact) (scheme complex) 等
  b_list.go b_string.go b_vector.go b_control.go b_io.go b_system.go
test/scheme/           Scheme 测试
  r7rs-tests.scm       参考 R7RS 测试套件
  goscheme-tests.scm   本实现的补充测试（TCO、续延、库、端口、记录…）
  chibi/test.scm       测试框架（(chibi test) 兼容层）
  run-r7rs.scm run-goscheme.scm  测试驱动
dist/                  交叉编译产物（6 个平台）
```

## 构建

```sh
make build          # 编译到 .build/goscheme
make test           # Go 单元测试 + 两个 Scheme 测试套件
make dist           # 交叉编译全部平台到 dist/
```

也可以直接使用 Go：

```sh
/usr/bin/go build -o goscheme ./cmd/goscheme
/usr/bin/go test ./...
```

## 使用

```sh
goscheme                       # 交互式 REPL
goscheme program.scm           # 执行文件
goscheme -e '(display (+ 1 2))' -e '(newline)'
goscheme -i program.scm        # 执行后进入 REPL
goscheme -q                    # REPL 不打印 banner
echo '(map (lambda (x) (* x x)) (list 1 2 3))' | goscheme -q
```

## 语言覆盖

### 特殊形式

`quote` `quasiquote` `unquote` `unquote-splicing` `if` `define` `set!` `lambda`
`case-lambda` `begin` `let` `let*` `letrec` `letrec*` `let-values` `let*-values`
`define-values` `cond` `case` `and` `or` `when` `unless` `do` `delay`
`delay-force` `parameterize` `guard` `define-record-type` `define-syntax`
`let-syntax` `letrec-syntax` `syntax-rules` `include` `include-ci`
`cond-expand` `import` `define-library`

### 库

`(scheme base)` `(scheme case-lambda)` `(scheme char)` `(scheme complex)`
`(scheme cxr)` `(scheme eval)` `(scheme file)` `(scheme inexact)`
`(scheme lazy)` `(scheme load)` `(scheme process-context)` `(scheme read)`
`(scheme repl)` `(scheme time)` `(scheme write)` `(scheme r5rs)`

### 数据类型

布尔、数值（整数 / 有理数 / 浮点 / 复数）、字符（完整 Unicode 大小写映射）、
字符串、符号、序对与列表、向量、字节向量、过程、续延、参数对象、
Promise、端口、记录类型、错误对象、`eof`、未指定值。

## 实现要点

### 尾调用优化

求值机维护一个显式的续延栈。调用过程**不压入返回帧**，因此尾位置的过程调用
不会增长栈；循环、相互递归、`cond`/`case`/`and`/`or`/`when`/`begin`/`apply` /
`call-with-values` / `force` 的尾位置都保持常量栈空间。测试中 `(let loop ((i 0))
(if (= i 2000000) i (loop (+ i 1))))` 在默认栈下正常运行。

### 一等续延

`call/cc` 复制当前续延栈、`dynamic-wind` 风栈与异常处理器栈；调用续延时：

1. 计算当前风栈与目标风栈的最长公共前缀；
2. 逆序执行被退出部分的 `after` 过程；
3. 顺序执行被进入部分的 `before` 过程；
4. 恢复续延栈 / 风栈 / 处理器栈并返回值。

因此续延可多次调用（多发射），并与 `dynamic-wind`、`parameterize`、
`with-exception-handler` 正确协作。

### 卫生宏

模板中引入的标识符带有一个“标记”，该标记记录了宏定义时的环境。查找变量时先做
词法查找（保证宏引入的绑定与用户绑定互不干扰），失败后再沿标记的环境解析，从而
保证引用透明；标记可以复合，因此嵌套宏（宏生成宏）也保持卫生。字面量（literals）
按标识符身份/绑定比较，而不是简单按名字比较。

### 数值

精确整数在 `int64` 内不分配堆内存，溢出自动提升为 `big.Int`；精确有理数使用
`big.Rat` 并始终约分。混合精确/非精确比较会把非精确操作数转换为精确值，
保证 `=` 的传递性（R7RS 6.2.6）。

## 交叉编译产物

`dist/` 下为 6 个目标平台的可执行文件（`CGO_ENABLED=0` 静态编译，`-trimpath -ldflags "-s -w"`）：

| 文件 | 平台 |
|---|---|
| `goscheme-linux-amd64` | Linux x86-64 |
| `goscheme-linux-arm64` | Linux AArch64 |
| `goscheme-darwin-amd64` | macOS Intel |
| `goscheme-darwin-arm64` | macOS Apple Silicon |
| `goscheme-windows-amd64.exe` | Windows x86-64 |
| `goscheme-windows-arm64.exe` | Windows on ARM |

`dist/SHA256SUMS` 记录各产物的校验和。

## 测试

```sh
make test                                    # 全部测试
./.build/goscheme test/scheme/run-r7rs.scm   # 参考 R7RS 测试套件
./.build/goscheme test/scheme/run-goscheme.scm
```

- `test/scheme/r7rs-tests.scm`：chibi-scheme 维护的 R7RS 参考测试套件，覆盖
  4.1–4.3 语法、6.1–6.14 全部标准过程，共 1227 条断言。
- `test/scheme/goscheme-tests.scm`：针对尾调用、续延多发射、`dynamic-wind`
  重入、库导入变换、记录、端口、文件读写、异常与 `eval`/`load` 的回归测试。

## 已知限制

- `define-syntax` 仅支持 `syntax-rules` 变换器（R7RS-small 只要求 `syntax-rules`）。
- 数值输出中的浮点数使用最短往返表示；`write` 对形如 `+inf.0` 的符号会加 `|...|`。
- 目标标准为 R7RS-small；未实现 R7RS-large / SRFI 库。
