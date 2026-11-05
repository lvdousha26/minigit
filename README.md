# minigit

一个只依赖 Go 标准库的小型 Git 实现。它写出来的 `.git` 目录能被真实的 Git 直接读：`git log`、`git status`、`git cat-file`、`git ls-tree` 和 `git fsck --strict` 都在测试里跑过。反过来，Git 创建的仓库和 index，minigit 也能读。

写这个东西的目的是把 Git 的磁盘格式拆开看一遍：blob、tree、commit 怎么互相引用，index 到底存了什么，一次 commit 究竟改了几个字节。所以这里没有漂亮的抽象，代码基本贴着格式走。

## 编译

```sh
go build -o bin/minigit .
# 或者 make / make check
```

需要 Go 1.25 以上。测试还要求 PATH 里有真实的 `git`（端到端测试拿它做交叉验证）。

## 试一次

```sh
mkdir demo && cd demo
minigit init
echo "hello" > a.txt
minigit add a.txt
minigit commit -m "first commit"

git log --oneline --stat      # 真实 Git 读得懂
git status --porcelain        # 空输出，工作区是干净的
git fsck --strict             # 无输出
```

`minigit hash-object a.txt` 打印的是 `ce013625030ba8dba906f756967f9e9ca394464a`（"hello\n" 的 blob id），跟 `git hash-object a.txt` 一致。这是内容寻址最直观的一次检验：文件名不参与 hashing，同内容不同路径会得到同一个对象，`git ls-tree -r` 里能看到两条记录指向同一个 id。

## 命令

| 命令 | 作用 |
| --- | --- |
| `init [-b name] [path]` | 建 `.git` 骨架，默认分支 `main` |
| `hash-object [-w] [-t type] file` | 算对象 id，`-w` 顺手写进对象库 |
| `cat-file (-t\|-s\|-p) <id>` | 看类型 / 大小 / 内容，支持 id 缩写 |
| `add <path>...` | 写 blob 并更新 index，`add .` 会递归整个工作区 |
| `write-tree` | 把 index 固化成 tree 对象 |
| `commit-tree <tree> [-p parent] -m msg` | 只造 commit 对象，不动 ref |
| `commit -m msg` | write-tree + commit-tree + 移动 HEAD 指向的 ref |
| `log [-n N]` | 沿第一父提交往回走 |
| `status` | 短格式，两列分别是 index↔HEAD、工作区↔index |
| `ls-tree [-r] [<tree-ish>]` | 列 tree，`-r` 只列叶子 |
| `rev-parse <rev>` | 解析 HEAD、分支名或对象 id |

## 磁盘格式

### 对象

loose object 是字节串 `<type> <十进制长度>\x00<content>`，取 SHA-1 当文件名，zlib 压缩后放在 `.git/objects/<前两位>/<后 38 位>`。header 里那个长度不是冗余：解析时拿它和解压后实际字节数对一遍，能挡住截断的文件。读的时候还会重新算一次 SHA-1 和文件名比对——一个能正常解压、格式也合法的文件，内容却对不上名字，仍然是坏对象。

写对象先写同目录临时文件再 `rename`，避免别的进程读到半个 zlib 流；落盘后 `chmod 0444`，和 Git 一样把对象当不可变数据。重复写同一内容直接返回，不会动原文件。

### index

index 是暂存区，不是工作区的镜像。它是一份扁平的 `path -> blob id` 表，加上一组 stat 缓存：

```
DIRC | u32 version(2) | u32 count
entry: 10 x u32 stat | 20 byte oid | u16 flags | path | NUL padding
末尾: 20 byte sha1(前面所有字节)
```

每条 entry 用至少 1 个 NUL 补齐到 8 字节边界，路径一律用 `/`。写错对齐或 checksum，Git 会直接拒绝这个 index，所以 `index/index_test.go` 里是逐字节验的。

这里只填了 mtime 和 size，dev / ino / uid / gid / ctime 留 0。这不会让 `git status` 误报脏：stat 只是加速用的缓存，对不上时 Git 会重新读文件算 hash，内容一致就判定 clean。把这件事故意留得不完整，反而能看清楚「内容 hash 才是真相」。

### tree

tree 的内容是重复的 `<八进制 mode> <name>\x00<20 字节原始 oid>`，没有分隔符。目录的 mode 写作 `40000`（不带前导 0）。Git 不存空目录，所以 index 里出现空目录也不会生成对象。

排序是最容易踩的坑：目录名比较时隐含一个尾部 `/`。于是 `a-b.txt`、`a.txt`、目录 `a`、`z.txt` 的正确顺序靠的是 `-`(0x2d) < `.`(0x2e) < `/`(0x2f)。单纯按路径字符串排会得出 `a` 在 `a.txt` 前面，`git fsck --strict` 会报 tree not properly sorted。`TreeSortingSurvivesGitFsck` 这个测试专门盯着它。

### commit 与 ref

commit 是纯文本：header 若干行（`tree`、0 到多个 `parent`、`author`、`committer`），空行，然后 message。`HEAD` 文件通常不是 id，而是 `ref: refs/heads/main`；那个 ref 文件里才是 40 位十六进制。第一次提交之前 ref 文件根本不存在，这就是 "unborn branch"。所以 commit 做的事其实只有三件：从 index 生成 tree、写一个 commit 对象、把 ref 文件里的那 40 个字符换掉。

`log` 就是顺着 `parent` 往回读；`status` 是把 HEAD 的 tree、index、工作区各自摊平成 `path -> oid` 再比，两列分别对应 index 与 HEAD 的差异、工作区与 index 的差异。

## 测试

```sh
make check        # go vet + go test -race ./...
# 没有 make 就直接跑：go vet ./... && go test -race ./...
```

单元测试覆盖已知 hash 向量、损坏的 zlib 流与 sha-1 不符的对象、index 的对齐和 checksum、tree 解析与排序、commit 头解析和固定时间戳。端到端测试会真的起 `git`：

- minigit 建库并提交 → `git cat-file` / `git status` / `git log` / `git ls-tree` / `git fsck --strict` 全部通过
- `git init` + `git commit` → minigit 的 `log` / `status` / `cat-file` / `write-tree` 读得出来，且两边 `write-tree` 结果相同
- 两次提交、修改、删除、未跟踪、先暂存又改文件的双列状态，最后用 `git show --stat` 复核

CI 在 Ubuntu 和 macOS 各跑一遍，同样带 `-race`。

## 没做的

- packfile、delta 压缩、多 pack index；只读 loose object，读不了打包过的仓库
- merge / rebase / branch / checkout / reset、remote、fetch、push
- reflog、hooks、tag 对象、`.gitignore`、`.gitattributes`、子模块、符号链接、gitlink
- index v3/v4 与扩展区（读的时候容忍，写的时候不生成）；冲突 stage、稀疏检出
- 超过 4095 字节的路径；`add` 不读 `.gitignore`，会把 `.git` 之外所有文件都收进来
- `config` 里写死 `filemode = false`，不跟踪可执行位，这样 Windows 和 Unix 上行为一致
- status 不做 rename 检测，不做换行符转换
- SHA-1 在这里只是寻址手段，不是安全边界；SHA-256 object format 不支持

其中 packfile 和 merge 是真正的分水岭：前者要引入另一套索引和增量编码，后者要引入三方合并与冲突表示，都跟「看清磁盘格式」这件事无关。

## License

MIT
