# Stratum MCP Governor 服务治理审计报告

**日期：** 2026-07-23
**模式：** 专项（`tools/mcp-governor`）
**审计阶段：** 只读静态审计

## 执行摘要

本次审计确认 3 项缺陷：High 2 项、Medium 1 项。最高风险路径是客户端退出或代理转发失败后，
代理只取消直接子进程且没有进程组、信号转发或退出预算；若 MCP 再派生 watcher/browser 等后代，
后代可能脱离会话继续运行。另一条 High 路径是观测写入位于协议转发关键路径，文件锁或存储停顿会
直接阻塞 MCP 请求/响应，持久化错误则终止整个代理，与“透明观测”目标冲突。

静态审计不能证明真实 WSL 中的信号、文件系统停顿和 systemd manager 默认值。Obsidian 仅检索到一条
`provisional` 的 WSL/systemd `/proc` 隔离案例，故只列为运行验证线索。官方网页抓取工具本次因
客户端配置错误不可用；Go 子进程语义改以本机 Go 1.25 上游源码注释核验，不对 systemd 外部语义作
超出项目证据的强断言。

## 范围与覆盖矩阵

| 边界 | 状态 | 审计内容 |
|---|---|---|
| CLI ingress / admission | Covered | 严格参数解析、客户端/服务白名单、transport/scope、命令与参数精确匹配、分类复核 |
| Config / identity | Covered | 严格 JSON、凭据参数拒绝、salt 私有读取、session/repository 哈希 |
| Stdio proxy / subprocess | Covered | stdin/stdout/stderr、行大小、并发、取消、首错、关闭、wait、信号和后代进程 |
| Observation writer / tracker | Covered | 元数据边界、锁、同步写入、失败传播、在途请求清理 |
| Snapshot / report / pruning | Covered | `/proc` 扫描、历史上限、事件扫描、锁、聚合基数、原子发布和 retention |
| Installer / systemd | Covered | 私有目录、原子安装、timer/service 组合、失败传播、重启与资源边界 |
| Cross-client E2E | Partial | 已读四客户端形状和现有 E2E；未在审计阶段操作真实客户端或 systemd |
| 网络、数据库、消息队列 | Not Present | 本工具无网络 listener、数据库、Redis、NATS 或远程依赖 |
| WSL 信号、孤儿和 `/proc` 权限 | Runtime Only | 需真实进程树、SIGTERM、systemd 和 WSL 内核验证 |

治理信号脚本因仓库 PreToolUse hook 将其来源路径判定为主 checkout 而拒绝直接执行；审计随后原样执行了
脚本中的 scoped `rg` 表达式。命中项仅作为候选，主要位于 proxy goroutine/channel、writer 并发锁和
timeout outcome 测试，未直接转化为发现。

## 调用链与重试组合

本工具没有自动重试、消息重投或网络 SDK 重试，因而不存在乘法重试链。关键调用链为：

```text
Codex / Claude Code / VS Code / Lingma
  -> rendered mcp-governor proxy argv
  -> runProxy catalog admission + private identity
  -> proxy.Run(context.Background)
  -> exec.CommandContext direct child
  -> two forwarding goroutines
  -> synchronous Writer.Write + flock
  -> child Wait
```

当前总预算：启动、转发、观测锁等待、客户端 EOF 后退出和子进程 wait 均无显式 deadline；单条协议消息
内存上限为 8 MiB，事件/历史 JSONL 单条读取上限为 4 MiB。systemd snapshot 每次完成后再按一分钟周期
触发，report 每日触发；项目 unit 未声明显式 start/stop timeout、CPU/内存/任务数上限或 restart policy。

## 分级发现

### SG-001 客户端终止与取消不能确定性清理完整 MCP 进程树

- **Repair status (2026-07-24):** Implemented. Linux proxy commands now run in an exact session-owned process group;
  the CLI injects a signal-aware context for `SIGINT`, `SIGTERM`, and `SIGHUP`; cancellation, forwarding failure,
  and bounded client-EOF drain use `SIGTERM` → named grace → exact-group `SIGKILL` under a total shutdown budget.
  Expected `ESRCH`/already-closed cleanup artifacts are filtered while other cleanup errors remain joined.
- **Repair evidence:** `internal/proxy/process_tree_linux_test.go` covers a direct child plus TERM-ignoring grandchild,
  cancellation, client EOF, forwarding error, exact PID disappearance, and unrelated-process isolation;
  `cmd/mcp-governor/main_test.go` verifies context injection without global signal capture in internal APIs;
  `scripts/e2e-observation.sh` verifies real CLI `SIGTERM`, EOF cleanup, exact PIDs, and concurrent-session isolation.
  Focused tests, full nested-module race tests, vet, and the disposable four-client E2E are the verification gates.
  Descendants that deliberately call `setsid(2)` leave the inherited ownership boundary and are explicitly excluded.

- **Severity:** High
- **Confidence:** Confirmed
- **Evidence:**
  [main.go:478](../../tools/mcp-governor/cmd/mcp-governor/main.go#L478)、
  [stdio.go:63](../../tools/mcp-governor/internal/proxy/stdio.go#L63)、
  [stdio.go:65](../../tools/mcp-governor/internal/proxy/stdio.go#L65)、
  [stdio.go:138](../../tools/mcp-governor/internal/proxy/stdio.go#L138)、
  [stdio.go:156](../../tools/mcp-governor/internal/proxy/stdio.go#L156)
- **Call path:** client process exit/signal → `runProxy` → `proxy.Run(context.Background())` →
  `exec.CommandContext` → direct MCP child → optional watcher/browser/grandchild
- **Trigger:** 客户端或代理收到 SIGTERM/SIGHUP、stdin EOF 后子进程不退出、直接子进程派生后代，或转发错误触发取消。
- **Existing protection:** 转发首错会 cancel 派生 context、关闭两端 pipe、等待两个 goroutine，并调用 `Cmd.Wait`；
  测试覆盖 context cancellation 对直接 fake server 的清理和错误 join。
- **Failure and impact:** CLI 没有把 OS signal 转换为 context cancellation；正常 `proxy` 路径使用不可取消的
  `context.Background()`。即使内部 cancel 生效，Go 1.25 `CommandContext` 默认 `Cancel` 只调用直接
  `Process.Kill`，代码也没有设置独立 process group、向组发送终止信号、等待 grace period 或验证后代退出。
  因而客户端崩溃/关闭与 WSL shutdown 不能满足设计中的“无 session adapter 或 child orphan”契约；重复发生可累积
  watcher、浏览器或索引后代并消耗 CPU/内存。
- **Repair direction:** 为 CLI 建立 signal-aware 根 context；将每个 MCP 放入独立进程组，定义 TERM→有限 grace→KILL
  的整体关闭预算，wait forwarding goroutines 和直接子进程后再验证/清理组内后代。不要按进程名匹配终止。
- **Compatibility concern:** 部分 MCP 自行管理独立 daemon；进程组策略必须只覆盖本次 session 所有权明确的后代，
  并验证 shell/npm/uvx 启动链、Windows/非 Linux 构建边界和正常 exit code 保留。
- **Verification:** 新增 direct child + stubborn grandchild E2E；分别向 proxy 发送 SIGTERM、关闭 client stdin、制造转发错误，
  在限定时间内用精确 PID/PGID 断言所有本会话进程退出，其他并发会话不受影响；在真实四客户端 gate 中重复验证。

### SG-002 同步观测锁和写失败位于协议关键路径，可阻塞或终止 MCP 会话

- **Severity:** High
- **Confidence:** Confirmed
- **Evidence:**
  [stdio.go:90](../../tools/mcp-governor/internal/proxy/stdio.go#L90)、
  [stdio.go:109](../../tools/mcp-governor/internal/proxy/stdio.go#L109)、
  [stdio.go:124](../../tools/mcp-governor/internal/proxy/stdio.go#L124)、
  [writer.go:102](../../tools/mcp-governor/internal/observe/writer.go#L102)、
  [writer.go:107](../../tools/mcp-governor/internal/observe/writer.go#L107)、
  [main.go:839](../../tools/mcp-governor/cmd/mcp-governor/main.go#L839)、
  [main.go:846](../../tools/mcp-governor/cmd/mcp-governor/main.go#L846)、
  [writer_test.go:14](../../tools/mcp-governor/internal/observe/writer_test.go#L14)
- **Call path:** client request / child response → forwarding callback → tracker → `writeEvents` → `Writer.Write` → blocking `flock`/disk write;
  daily report → shared `flock` held while scanning complete active JSONL file
- **Trigger:** report 扫描当前活跃 session 文件、另一个同 session writer 持锁、文件系统/磁盘停顿、磁盘满、权限或 I/O 错误。
- **Existing protection:** 单条 event 小且 metadata-only；mutex 和 file lock 保证原子行；不同 session 使用不同文件；写错误显式传播，
  不伪造成功。report/prune 通过 inode/size/lifecycle lock 避免错误删除活跃文件。
- **Failure and impact:** `flock` 没有 non-blocking/timeout/cancellation；report 在解析整个文件期间持 shared lock，writer 获取
  exclusive lock 会无限等待。客户端路径先把请求交给 child，再同步写事件；此时 server response 又可能等待 observation gate，
  故观测停顿会冻结协议链。任何 writer error 都被当作 terminal forwarding error，取消并杀死 MCP 直接子进程。
  一个非业务观测依赖因此能破坏核心工具调用，与计划的透明代理目标不符。
- **Repair direction:** 将观测从协议正确性路径隔离：使用有界内存队列/单 writer、明确满队列策略和健康信号；event 写入应有
  cancelable/有界等待，report 应避免长时间锁住活跃 append 文件（例如稳定快照/短锁复制后离线解析）。仍需暴露观测降级，
  不能静默声称完整采集。
- **Compatibility concern:** 若选择丢弃 metadata，七天命中率报告必须携带 dropped/error 计数并标为不完整；若选择反压，
  必须给出远小于 MCP 调用预算的上限，不能重新形成无限等待。
- **Verification:** 用真实文件锁阻塞、慢 writer、ENOSPC/短写和大型活跃 JSONL 并发 report 测试；断言 MCP 原始字节仍及时双向转发、
  会话不中断、队列有界、观测缺口可见、shutdown 能排空到预算后退出。

### SG-003 时间 retention 不限制单个长会话文件或报告聚合基数

- **Severity:** Medium
- **Confidence:** Confirmed
- **Evidence:**
  [writer.go:55](../../tools/mcp-governor/internal/observe/writer.go#L55)、
  [writer.go:70](../../tools/mcp-governor/internal/observe/writer.go#L70)、
  [main.go:807](../../tools/mcp-governor/cmd/mcp-governor/main.go#L807)、
  [main.go:857](../../tools/mcp-governor/cmd/mcp-governor/main.go#L857)、
  [main.go:1102](../../tools/mcp-governor/cmd/mcp-governor/main.go#L1102)、
  [aggregate.go:50](../../tools/mcp-governor/internal/report/aggregate.go#L50)、
  [aggregate.go:106](../../tools/mcp-governor/internal/report/aggregate.go#L106)
- **Call path:** long-lived client → one append-only `<session>.jsonl` → daily `WalkDir`/full scan → maps keyed by service/tool/session/duration/size
  → prune only when entire file is older than boundary
- **Trigger:** 长期不退出的编辑器/client、持续高调用量、高基数 tool 名/latency/response size，或异常客户端制造大量合法 metadata。
- **Existing protection:** raw retention 配置限定 7–90 天；每条 proxy 消息 8 MiB、每条 event/history record 4 MiB；snapshot history
  以 `retentionDays * 1440` 样本封顶；结束且全量过期的 session 文件会安全删除。
- **Failure and impact:** event writer 不轮转且无文件/目录 quota。只要 session 文件含一条未过 boundary 的事件，prune 就保留整个文件；
  长会话可持续保存超过 retention 的数据。报告虽然流式读文件，但聚合器为每个 tool/session/day 和每个不同 duration/response size
  保留 map 项，内存只受实际事件基数限制。磁盘增长或报告 OOM/长时间运行会使观测/report 失败，并通过 SG-002 反压活跃会话。
- **Repair direction:** 按日期或受控大小轮转 session event 文件，安全 prune 已封闭的 segment；为总 bytes、records、tool-cardinality、
  percentile histogram/bucket 和 report work budget 建立显式上限，超过时失败并输出可审计的不完整状态。
- **Compatibility concern:** 轮转不能改变 session hash 聚合语义，不能在 writer 活跃时 unlink 当前 segment；近似 percentile 需版本化并说明误差。
- **Verification:** 用跨 retention 的长会话和高基数 fixture 运行 bounded-memory/磁盘测试，确认旧 segment 被清理、活跃 segment 不丢数据、
  超限报告不替换上一份有效报告并明确标记缺口。

## 潜伏风险与证据缺口

- `--session PID:START_TICKS` 只校验格式，不校验该 identity 是否属于调用方父进程；生成的四客户端配置不会传此参数，
  因此当前归为非默认 CLI 的潜伏隔离风险，而非生产可达 finding。
- config decode 使用 `io.ReadAll`，catalog 大小无界；catalog 是本机私有运维文件，缺少不可信 ingress 证据，归为低优先级加固项。
- snapshot 遍历 `/proc` 和 report 遍历全部 event 文件没有应用级 deadline。systemd unit 未声明显式 timeout/resource limits；
  需读取真实 user manager 配置和运行时 duration/RSS 后判断是否形成缺陷。
- Obsidian 的“WSL systemd 文件系统隔离可能阻断跨进程 PSS USS 采集”状态为 `provisional`，范围仅是特定
  WSL2/systemd 255/`ptrace_scope=1` 实验；不能推广到当前运行环境，需复现实验。
- 官方网页抓取失败（fetch MCP 报 client `proxies` 参数错误）。Go `CommandContext` 语义由本机
  `/usr/local/go/src/os/exec/exec.go` 的 Go 1.25 上游注释核验；systemd timer 的“不重入”和默认 timeout 语义未作为 findings 的关键证据。

## 已有正确治理措施

- proxy admission 在启动前校验已知 client、service、stdio transport、client availability、repository identity、
  catalog command/args 精确一致和分类规则命中，失败不启动 child。
- 输入/输出协议按原字节转发，单消息 8 MiB 硬上限；无法可靠中断的自定义 stdin 在启动 child 前 fail closed。
- 首个 terminal error 后 cancel、关闭 pipe、等待两个 forwarding goroutine、`Wait` child，并用 `errors.Join` 保留 cleanup failure。
- event 仅持久化 metadata；参数、响应正文、error text、URL 和凭据不落盘。salt、event、report 和目录使用私有权限并拒绝 symlink。
- snapshot history 有锁、排序、去重、样本上限和原子替换；报告 malformed input 时保留上一份有效报告。
- prune 使用 lifecycle lock、file lock、device/inode/size 复核，避免删除已追加或仍活跃的 session 文件。
- installer 以私有 staging/atomic rename 安装 binary/unit，catalog 和 salt 已存在时不覆盖，systemctl 错误向上传播。
- `go test -race -timeout 30s ./...` 在 Go 1.25 nested module 全部通过。

## 建议修复顺序

1. SG-001：先定义 signal、process group、grace/KILL 和后代所有权契约，避免继续产生孤儿。
2. SG-002：再隔离观测 I/O 与协议路径，并加入 dropped/degraded 完整性信号。
3. SG-003：在新 writer 模型上加入 segment rotation、bytes/cardinality/work budgets，避免重复改存储格式。
4. 完成上述修复后再运行四客户端 E2E、真实 SIGTERM/WSL shutdown 和七天 rollout gate。

## 未覆盖与运行验证建议

- 未启动、停止或修改真实 systemd user unit；未操作 live Codex、Claude Code、VS Code 或 Lingma 配置。
- 未注入真实磁盘满、NFS/FUSE 锁停顿、OOM、WSL shutdown 或 kernel signal 场景。
- 运行时应采集：proxy/child/grandchild 精确 PID/PGID、退出耗时、event queue/写延迟、dropped/error 数、event bytes、
  report peak RSS/duration、systemd timeout/result 和 snapshot `/proc` permission warning。
- 在修复前，不建议把“客户端关闭后无 orphan”或“观测不会影响 MCP 可用性”作为 rollout 已满足条件。

## 修复授权门禁

审计阶段到此停止。等待用户明确选择 `SG-001`、`SG-002`、`SG-003` 或给出修复范围后，才能修改业务代码、
测试、配置或 systemd unit；修复阶段必须使用 `stratum-e2e-development` 并重新执行项目要求的验证。
