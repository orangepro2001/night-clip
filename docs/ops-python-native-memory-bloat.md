# 运维记录：scope-recall 的 Python 进程合计 7.8 GiB（glibc 线程 arena 膨胀）

2026-10-28。机器级问题，不属于 ClipBridge 的代码；记在这里是因为这次的诊断方法、陷阱和结论可以复用到任何「Python + 原生扩展（Arrow / Lance / Rust）+ 多线程」的常驻进程。

## 1. 现象

`ps` 里 scope-recall 有 22～24 个 Python 进程，RSS 合计约 **7.8 GiB**（实占 PSS 7.2 GiB）。其中 10 个进程每个 600～860 MB，其余是 8～40 MB 的小进程。数据本身很小：共享存储 36 MB，没有本地模型（嵌入走远程 API）。

| 角色 | 个数 | 每个 | 怎么来的 |
| --- | --- | --- | --- |
| 远程入口服务（`scope-recall-remote-*.service`，systemd 用户服务） | 5 | 780～850 MB | 常驻；检查时都没有连接，累计 CPU 只有约 6 分钟 |
| 各个 Claude Code 会话自带的 MCP 进程（`mcp_entry`） | 4 | 600～860 MB | 每开一个会话起一个，会话不关就不退 |
| dsh-web 的常驻进程（`resident_entry`） | 1 | 760 MB | 属于 dsh-web.service，不是 scope-recall 的单元 |

## 2. 诊断（按顺序，每步都排除一种解释）

1. **RSS 会重复计算共享页，先看 PSS。** `awk '/^Pss:/' /proc/PID/smaps_rollup`。这里 PSS 只比 RSS 少 7%，所以不是「共享了很多页」的假象，是真占。
2. **按内存类型拆分 `/proc/PID/smaps`**（匿名 / 文件映射 / `[heap]` / 线程栈与 arena）。结果：~90% 是匿名内存，且几乎全在 **约 165 个 ≤64 MB 的匿名映射**里（glibc 给每个线程开的 arena 就是 64 MB 对齐的小块）；Python 自己的堆（`[heap]`）只有 5～24 MB，文件映射 50 MB 左右。⇒ 不是 Python 对象泄漏，也不是模型，是**原生代码的多线程分配**。
3. **看装了什么。** venv 里只有 `lancedb`（156 MB）和 `pyarrow`（149 MB）两个大包，都是原生库，带自己的线程池。
4. **对比新进程。** 新起一个 MCP 进程：空闲 96 MB，调用一次 `status` 后 ~180～260 MB。老进程是 600～860 MB，所以是**随使用慢慢涨出来的**，不是启动就这么大。
5. **复现。** 用 stdio 手工起一个 MCP 进程，发**合法参数**的 recall（每轮 12 个并发，长度不一的查询，四种 mode）：默认配置下 RSS 从 97 MB 一路涨到 **817 MB**、映射数从 40 涨到 257，和线上老进程一模一样。
6. **验证修复。** 同样负载下设 `MALLOC_ARENA_MAX`：

   | `MALLOC_ARENA_MAX` | 8 轮后 RSS | 趋势 |
   | --- | --- | --- |
   | 不设（默认，12 核 ⇒ 最多 96 个 arena） | 817 MB | 一直涨 |
   | 4 | ~400 MB | 平 |
   | 2 | ~320 MB（有回落） | 平 |
   | 1 | ~300 MB | 平 |

   响应时间没变（每轮 12 个请求都是 ~4.7 s）。选 2：效果和 1 接近，不把所有线程挤到同一把锁上。

**结论：** glibc malloc 给每个分配过内存的线程开独立 arena，原生库的工作线程把内存分配在各自的 arena 里，释放后碎片不归还系统，高水位一直保持。默认上限是核数 ×8，所以常驻服务会慢慢涨到近 1 GB。`MALLOC_ARENA_MAX=2` 把上限压到 2 个。

## 3. 做了什么

| 对象 | 改动 | 效果 |
| --- | --- | --- |
| 5 个 `scope-recall-remote-*.service` | 每个加 drop-in `~/.config/systemd/user/<unit>.service.d/10-malloc-arena.conf`：`[Service]` / `Environment=MALLOC_ARENA_MAX=2`；`daemon-reload` 后逐个重启（重启时都没有连接，端口不变） | 每个 ~800 MB → 252 MB 起步 |
| Claude Code 插件的 MCP 定义 `~/.claude/skills/scope-recall/.mcp.json` | 给 `scope-recall` 加 `"env": {"MALLOC_ARENA_MAX": "2"}`；原文件备份在 `~/srecall/mcp.json.bak-20261028`；`claude mcp list` 仍显示 Connected | **只对新开的会话生效** |

整体：scope-recall 的 Python 进程合计 **RSS 7.83 → 5.08 GiB，PSS 7.22 → 4.25 GiB**（当场）。

## 4. 还没做的（要么需要重启会话，要么属于别的项目）

- **现有的 4 个会话 MCP 进程（600～860 MB）**：在每个会话里执行 `/mcp` 重连 scope-recall，或者直接重开会话，它们就会换成带参数的新进程，每个降到 ~300 MB。我没有去杀它们：它们是活着的会话的 stdio 子进程，杀掉会让那个会话的记忆工具断掉。
- **dsh-web 的 `resident_entry`（760 MB）**：属于 `dsh-web.service`（另一个项目）。需要同样的 drop-in 并重启该服务；会打断 dsh 网页，所以没动。
- **5 个远程入口服务是否都需要**：当时都没有连接、CPU 近乎为零；`egg-*` 和 `remote-*` 两批各有一个 claude-code、一个 dsh 的重复。是否合并/按需启动取决于远程客户端实际怎么用，需要用户判断。
- scope-recall 升级或重装时，安装器可能重新生成 `.mcp.json`，把 `env` 冲掉；升级后检查一遍（systemd drop-in 不受影响）。

回退：删掉 5 个 `*.service.d/10-malloc-arena.conf`（及空目录）、`systemctl --user daemon-reload`、重启对应服务；`.mcp.json` 用上面的备份还原。

## 5. 复用的方法和陷阱

诊断命令（把 PID 换成目标进程）：

```sh
awk '/^Rss:|^Pss:|^Anonymous:/' /proc/PID/smaps_rollup           # 真实占用 vs 重复计算
ls /proc/PID/task | wc -l                                         # 线程数
# 按映射类型汇总 smaps：匿名且 ≤64MB 的一大堆 ⇒ 线程 arena；[heap] 小 ⇒ 不是 Python 对象
```

- **先区分「Python 堆」和「原生线程 arena」**：`[heap]` 小而几十上百个 64 MB 对齐的匿名块大，就是 glibc arena；换 allocator 或设 `MALLOC_ARENA_MAX` 比调库参数有效。
- **用新进程在可控负载下复现，再用同一负载对比修复。** 只看老进程无法判断是泄漏还是高水位。
- **测试要先确认调用真的成功。** 我第一轮用了不合法的工具参数，返回的全是校验错误，得出「25 次调用内存不涨」的错误结论；换成合法参数（先 `tools/list` 看 `inputSchema`）才复现。判断条件里要数「错误回复个数」。
- **环境变量要在进程 `exec` 之前就在环境里**：glibc 在第一次 `malloc` 时读 `MALLOC_ARENA_MAX`，进程起来后再 `os.environ[...]` 没用。systemd 用 `Environment=`，MCP 用 `.mcp.json` 的 `env`。
- 在 zsh 里 `units="a b c"; for u in $units` **不会按空格拆分**（bash 会）；用数组 `units=(a b c); for u in "${units[@]}"`。这次因此误建过一个名字带空格的 drop-in 目录，已清理。
- 不要为了「治」内存去装 jemalloc / 改库源码：先用环境变量验证，够用就停。
