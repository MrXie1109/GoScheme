# GoScheme 示例

这里是可以直接运行的小程序，用来展示这门 Scheme 的特色。语言主体是
R7RS-small，所以真正有意思的地方是超出报告的部分：Go 风格的并发库、哈希表、
调用外部程序，以及 `goscheme build` 这个打包步骤。

每个示例都是普通脚本，运行时会把自己在做什么打印出来，所以它们既是给你跑的，
也是给你读的。

## 怎么运行

`goscheme` 在 `PATH` 里时：

```console
$ goscheme examples/numbers.scm
```

用 `dist/` 里的二进制：

```console
$ ./dist/goscheme-linux-amd64 examples/numbers.scm
```

一次跑全部（会报告哪些失败）：

```console
$ ./examples/run-all.sh
$ GOSCHEME=./dist/goscheme-linux-amd64 ./examples/run-all.sh
```

## 每个示例展示什么

| 文件 | 展示的内容 |
|---|---|
| `numbers.scm` | 数值塔：任意精度精确整数、精确有理数、复数，以及读取器接受的数字写法（`#x1f`、`#e1.5`、`s f d l` 指数标记）。 |
| `recursion.scm` | 正确的尾调用：一百万次的循环只占常数栈空间，包括互递归与 `cond` 中的尾位置。 |
| `hash-tables.scm` | `(goscheme hash-table)`：三种键比较方式、`ref` 与 `ref/default` 的区别、修改、遍历，以及带边界的 `hash`。 |
| `concurrency.scm` | `(goscheme channel)`：带缓冲与不带缓冲的通道、`chan-recv!` 的两个返回值、工作池、`(go ...)`、`go-wait`，以及带 `(after ms)` 与 `(else)` 的 `(select ...)`。 |
| `processes.scm` | `(goscheme process)`：走 shell 的 `system` 与直接执行的 `system*`、退出状态（含被信号杀死的情况）、子进程继承标准流，以及"程序不存在"是文件错误。 |
| `script-args.scm` | `(command-line)` 以及解释器名字为何不在其中、`(assert ...)` 是可捕获的普通条件、`#!unspecified`、`(features)`。 |
| `libraries/main.scm` | 从文件加载库：`(lib greet)` 从 `lib/greet.sld` 读取，而 `(lib namer)` 又导入了它。 |

## 从文件加载库

库就放在它名字所描述的文件里，不需要注册任何东西：

```scheme
(import (lib greet))        ; 读取 lib/greet.sld，或 .scm、.sls、.ss
```

搜索从"发起导入的那个文件所在目录"开始，每加载一个库就把该库自己的目录也加进去，
所以库可以导入它的邻居。之后依次是 `GOSCHEME_LIBRARY_PATH` 里的目录（Unix 用 `:`
分隔，Windows 用 `;`），最后是当前目录。所以 `examples/libraries/main.scm` 无论从
仓库根目录、从它自己所在目录，还是用绝对路径从任何地方运行，结果都一样。

## 把脚本变成单个可执行文件

`goscheme build` 把脚本和一份解释器绑在一起；加上 `-static` 还会把它需要的每个库
和 `include` 都内联进去，于是产物在那些文件都不存在的地方照样能跑：

```console
$ goscheme build -static examples/libraries/main.scm -o hello
$ cd / && /path/to/hello
hello from a library, world
```

不加 `-static` 时，可执行文件仍在运行时从磁盘读取库——开发库的时候这正是你想要的。
`build` 的完整说明见仓库根目录的 README；至于为什么这些二进制**故意**不能调用 C，
见 `docs/ffi-design.md`。
