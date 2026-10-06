# PNCR01：native SQL context 源码与本机验收报告

日期：2026-10-05（Asia/Tokyo）。状态：**源码与本机合作式取消已交付；完整 recovery 的无条件 5 秒硬上界未交付。**

SDK 已将 Go context 接入请求级原生 ABI，并在同步返回前保留 native handle、context token 和事务清理所有权。固定旧 Core 上的能力缺口已核实；新 Core 上进入 BEGIN/read/COMMIT/ROLLBACK 后的取消、deadline、事务清理、同目录后续写与 COMMIT unknown 已真实验证。fsync、文件系统 I/O、原生打开与无检查点区间仍不能硬抢占，不能据此把 SaaS 的 `WithTimeout(5s)` 写成整体恢复必在 5 秒返回。若 PNCR01 的验收条件坚持该硬合同，其状态仍应 OPEN；若收敛为本报告的合作式合同，源码和本机证据可复审。

## 1. 输入与保护范围

- 非作者原报告：`/tmp/ai-platform-operability-validation/payment-history-closure-independent-review-20261005.md`，SHA256 `dcecb7cce12caac330c8c53f4cfad17fdf804214fbce9d4ca78e49a5c3894e4f`，本轮重新核对一致。
- 原报告只证明入口 `ctx.Err()` 检查与后续同步无 context 调用的源码缺口，未动态制造旧 Core 卡住。本报告没有把该源推理改写成旧制品运行缺陷。
- 实际 SDK 开发基线：`/Users/dark/.codex/worktrees/native-local-development/talon-sdk-go`，HEAD `7847de0f0d1b4018fb1a4569d8e5cdc713a8f0f4`，包含已有 dirty 的本机开发入口与 SQL 合同工作。
- 本轮 SDK 改动树：`/Users/dark/.codex/worktrees/native-sql-context/talon-sdk-go`，从该 HEAD 创建，再复制 16 个基线源码/文档文件。没有覆盖原开发树；初始快照与保护复核见 `context-evidence/baseline.json`、`baseline-preservation.json`。原仓库 cwd `/Users/dark/WebstormProjects/talon-sdk-go` 未写入。
- 固定旧 Core：`/Users/dark/.codex/worktrees/scalar-aggregate/superclaw-db/target/console-domain-schema-evidence-20261005/native-config/env.sh`。该路径和 dirty 源码树未修改。
- 对应 Core 新任务已创建：`01a10b97-cd06-76c2-9835-a98330d8186a`，名称“Talon Core native SQL context ABI 与真实取消验证”，独立树 `/Users/dark/.codex/worktrees/sql-context-cancel/superclaw-db`。Core 的调查、源码与专项验证由该任务交付。
- 只读核对正式消费者 `/Users/dark/WebstormProjects/github/ai-platform/saas/go.mod`，仍锁 `github.com/darkmice/talon-sdk-go v0.7.5`。没有编辑 SaaS、业务金融数据、正式依赖或部署。

真实 TypeSafe 调用返回模型 `jev-1.13.0`、request `req_01a10b9654847fd19de48744cf527b3b`。Choice `core_cooperative` probability=0.99、confidence=0.99；`entry_only`=0.01、`goroutine_return`=0.0。按该结果优先调查并实施 Core 合作式通路，确定性能力门、所有权和验收仍由源码/测试控制。记录见 `context-evidence/typesafe.json`。

## 2. 接口与责任边界

新增 Core capability `native_sql_context@1`、feature `native_sql_context_v1`，使用完整四符号组：

```c
TalonSqlContext *talon_sql_context_new_v1(uint64_t timeout_ms);
int talon_sql_context_cancel_v1(TalonSqlContext *context);
void talon_sql_context_free_v1(TalonSqlContext *context);
int talon_exec_sql_context_v1(const TalonHandle *handle,
    const char *command_json, TalonSqlContext *context, char **output);
```

`timeout_ms=0` 表示无 deadline；非零使用 Core 单调时钟。token 仅用于一次请求；所有 concurrent execute/cancel 结束后才可 free。SQL request 沿用 module=sql、action=query、protocol_version=2、typed bind 与当前 native session。合作式 lock wait、扫描/聚合和事务边界由 Core 检查，所有权在调用返回前清理。Core 能力和 ABI 细节以其头文件与报告为准。

SDK 新入口：

```go
result, err := db.QueryResultContext(ctx, sql, params...)
err = db.ExecContext(ctx, sql, params...)
err = db.SQLRollbackContext(ctx)
```

- 新 Core 上普通和可取消 context 均走 context ABI。旧 Core 仅在 `ctx.Done()==nil` 时继续旧 SQL 通路；可取消 context 缺能力即 `CodeCapabilityUnavailable`，不以 goroutine 模拟取消。
- 调用 SQL 的 goroutine 始终同步等待 native。旁路 watcher 只发 cancel；return/free 前 stop 并 join watcher，DB 读锁租约保留到 native 完成。Close 不能销毁进行中 handle。
- Go deadline 到 Core 毫秒 TTL 时向上取整。DeadlineExceeded 由 Core 自身 deadline 观察；显式取消才调用 cancel ABI，避免 Go timer 稍早触发而将 deadline 错锁为 cancelled。已经过期的 ROLLBACK 使用过期 native token，并始终执行 cleanup。
- `SQLRollbackContext` 特意不在入口因过期直接跳过。清理不接受“超时即视为已回滚”；native 返回前 Core 必须完成清理。若清理失败，保留 unknown 与原 interruption cause。
- GoFrame 的 BEGIN、Ping、query、mutation、COMMIT 全部转发 context；terminal ROLLBACK 使用 transaction 的原 context。BEGIN dispatch 失败或 COMMIT/ROLLBACK terminal 失败会同步 Close 该 session，并经 `driver.Validator` 丢弃连接。没有返回可触发 mutation 自动重放的 `driver.ErrBadConn`。
- `nativeConnector.Connect(ctx)` 在 Open 前后检查 context；Open 后发现取消先同步 Close 再返回。原生 Open、签名/开发准入和 GoFrame 首次 pool probe 本身仍没有请求级中断 ABI，不能承诺其硬上界。
- 根包是显式 session API，没有自动事务对象。入口预取消的 SQL 没有 native dispatch，不能据此认为既有事务已结束；根包调用者须通过 `SQLRollbackContext` 或同步 Close 收口。GoFrame/database/sql 另有事务生命周期管理。

错误合同：

|native code|SDK outcome / cause|消费规则|
|---|---|---|
|`cancelled`|NativeCodeOf 保留；errors.Is(context.Canceled)|只表示已观察合作式中断；入口拒绝与已 dispatch 必须区分|
|`deadline_exceeded`|CodeNativeTimeout；errors.Is(context.DeadlineExceeded)|保留 native code，不是整体硬时间保证|
|`result_indeterminate` / 既有 `uncertain`|CodeResultIndeterminate 优先；保留 native code、具体取消/持久化/协议 cause|用原始完整请求查 exact receipt；禁止盲重放|
|cleanup_failed 的 context 扩展|CodeResultIndeterminate；保留原 interruption error chain 和 cleanup diagnostic|不能声称已 rollback；同步 session 收口错误必须处理|
|旧 Core 缺 context capability|CodeCapabilityUnavailable|升级或明确使用没有取消承诺的调用；不得静默弱化有 deadline 的请求|

SDK 严格验证 context-v1 的 code/cause/retryable/transaction_state/outcome 扩展；普通 legacy command decoder 不接受此扩展。取消错误不能覆盖已确认成功结果，未知提交也不能被重新分类成已确认 rollback。清理失败的嵌套原 cause 有单独单元验证，未动态制造 Core 锁 poison。

## 3. 本机制品身份

全部是 dirty/debug **unsigned local-development**，不具 release 或生产准入身份。Core commit 自证为 `6010d748aebd3c4e595534ed4a9e959e7e5334cf`，git_dirty=true，平台 macos-arm64；build profile 由调用者声明，Core ABI 不自证该字段。

|用途|library SHA256|位置|
|---|---|---|
|固定旧 Core|`58b73406e18831e7c7b69734111ede9ff289b61984cbd99cec8c89d78eb1a1db`|输入 env.sh 指定产物|
|最终普通 Core，无 test seam|`38423f3286c155a1b0a6e2ae6ae373e7cc79d830f3b525b696891f9fc426e523`|`context-evidence/core-final-normal/libtalon.dylib`|
|最终专项 test-seam Core|`c4c04c74a9d3f2133fd240f5948cc6b0b7725b08244a05a74736e57760c35f6e`|`context-evidence/core-final-test/libtalon.dylib`|

最终 header SHA256 `d65555f9a2502c0d311a04836d5b7dbaa9c62a4e32e1c0b6f21dd4f26b1c249f`。普通 manifest SHA `1df21f8686e294f1d63b669af8baf9b82ac58735dfbc9ceea4d062546a9d4dcb`，专项 manifest SHA `5e62b2fd451200c57b76ae1197be7dcffa43dc718b2abf8fe262ca5c22f8f2e4`。

使用已有独立 `cmd/talon-native-dev prepare` 实际加载和提取 ABI manifest，生成全新的 config；不制造签名 release。SDK 保有独立固定副本，避免 Core 后续 rebuild 改变已验收字节。`core-final-normal-config/env.sh` 与 `core-final-test-config/env.sh` 只用于对应制品。nm 已验证普通产物不导出 test gate，专项产物额外导出 per-token arm/entered/release；见 `normal-context-symbols.txt`、`test-context-symbols.txt`。

Core 的正式报告见 [SQL_CONTEXT_HANDOFF.zh-CN.md](/Users/dark/.codex/worktrees/sql-context-cancel/superclaw-db/SQL_CONTEXT_HANDOFF.zh-CN.md)，完整源码库存、归档、context-only.patch 与 Core 原生/SQL 专项证据均已交付。其 Core 自行保存的 sidecar 为 pretty JSON（normal 5134 bytes、test 5163 bytes）；本 SDK Prepare 从真实 ABI 提取原始字节（normal 4044 bytes、test 4068 bytes），语义比较完全一致但 SHA 不同。SDK 严格绑定原始 ABI manifest 字节，Core 自行 pretty sidecar 的 env 不能直接用于本 SDK admission；实测失败与 cause 见 `core-sidecar-format-negative.log`、`core-sidecar-format-cause.log`。消费者必须使用本报告已真实验收的 `core-final-normal-config/env.sh` / `core-final-test-config/env.sh`，或由当前 SDK Prepare 新建配置，不覆盖既有证据。此差异未放宽 SDK 校验，也不需要重建 dylib。

首个普通候选重建后，旧 pinned config 曾因 hash 变化拒绝加载。这是准入门的预期保护，并未放宽校验；最终验收重新复制候选并 Prepare 新配置。早期日志和失败记录保留，不冒充最终结果。

## 4. 已执行验证

|证据|实际结果|边界|
|---|---|---|
|原报告 SHA、v0.7.5 源码、固定 Core symbols/source|缺请求取消/deadline ABI；入口检查不足|不声称旧 native 被动态卡住|
|固定旧 Core `TestNativeSQLContextLegacyCapabilityGate`|无取消 SQL 正常；可取消 INSERT dispatch 前能力拒绝；未产生行|真实根包 native，不是 fake|
|旧 local admission、ReadYourWrites、SingleOwner|本机入口；binary/result_v2/GoFrame；事务占槽正常|有过误选 opt-in 导致 Skip 的初始记录，随后显式重跑真正执行并通过|
|最终专项 Core entered 矩阵，`-race`|36 个取消/deadline子例 + 4 个正常事务正例|root、driver、database/sql、GoFrame；见最终日志|
|最终普通 Core `TestNativeSQLContextNormalLocalDevelopment`|5s context GoFrame commit/exact fixture receipt；过期 root ROLLBACK；同目录 peer 写通过|普通 SDK、普通 Core，无 test seam|
|最终普通 Core `TestNativeSQLContextNormalLargeScan`|60000 行 SUM+COUNT(DISTINCT) 在 root/GoFrame 返回 native deadline_exceeded；约10.09/10.47ms；peer 后续写通过|10ms 实测，不推广为任意查询或I/O硬上界|
|SDK 根包/GoFrame `go test -race`|通过|错误cause、capability一致性、invalid session、协议unknown和cleanup cause单元覆盖|
|纯 Go Server/serverprotocol `CGO_ENABLED=0 go test`|通过|仅回归，不能替代 native 验收|
|context-only patch `git apply --check`、`git diff --check`|通过|没有实际 apply 到原 dirty 树，没有 commit/push|

专项矩阵先命中真实 native 阶段：1 BEGIN-created、2 scan-row、3 COMMIT-before-apply、4 COMMIT-applied-before-ack、5 ROLLBACK-cleaned；then cancel/deadline。保持 test gate 时，即使 Go context 已终止，SDK 调用也不提前返回；stage1–4 还验证同目录 peer 的 bounded native read 无法绕过执行锁。释放 gate 后，观察错误cause、清理与后续 peer 写。stage5 gate 位于完成清理之后，不声称仍持 SQL 锁。root/driver 直接覆盖 stage5；database/sql/GoFrame 的自动 rollback/close 生命周期通过 cancelled read 等路径覆盖，没有用与 awaitDone 竞争的外层 Rollback 假造“已进入 rollback”。

在 stage3 前取消，fixture 请求未落行；stage4 后取消，返回 CodeResultIndeterminate，原取消/deadline cause 可达，查询相同 id 并校验原 request_digest，读取到 exact 原 fixture receipt，不再次 INSERT。该 fixture 验证 SQL/SDK 的 unknown 与原回执读取合同，**不等于 SaaS payment history 的 authenticated exact receipt、完整金融业务链或生产验收**。

首轮 entered 矩阵真实发现过 deadline/cancel cause 竞态；修复后重跑全部通过，首轮失败保存在 `native-entered-tests.log`。完整 `go test ./...` 初次对两个纯Go包出现 macOS dyld `missing LC_UUID`，保存在 `source-tests-initial.log`；对应纯Go包按 CGO_ENABLED=0 跑通，根包/GoFrame另有通过的 native/race证据。链接器的 LC_DYSYMTAB warning 未造成最终 native 测试失败。

最终日志与执行方式（cwd 为本报告所在 SDK 独立树）：

```sh
# 普通 Core：不使用 SDK test tag。
source context-evidence/core-final-normal-config/env.sh
TALON_TEST_SQL_CONTEXT_NORMAL=1 go test ./goframe \
  -run '^TestNativeSQLContextNormal(LocalDevelopment|LargeScan)$' -v -timeout 3m

# 专项 Core：仅该 SDK tag 暴露 test bridge。
source context-evidence/core-final-test-config/env.sh
TALON_TEST_SQL_CONTEXT=1 go test -race -tags talon_sql_context_test ./goframe \
  -run '^TestNativeSQLContext(EnteredCancellation|PositiveTransaction)$' -v -timeout 5m

go test -race . ./goframe
CGO_ENABLED=0 go test ./server ./internal/serverprotocol
```

最终日志：`core-final-normal-sdk.log`、`core-final-entered-race.log`、`race-tests-final.log`、`server-tests-final.log`，全部在 `context-evidence/`。

## 5. SaaS 接入与未交付项

1. 正式 SaaS 仍 v0.7.5；该版本不含新 SDK 代码。只改 Core 不会自动把现有 GoFrame 调用改成 context ABI，必须同时消费新 SDK 源码和具备 `native_sql_context@1` 的 Core。
2. 本地验收可用显式 go.mod replace 指向本 SDK 独立树，并 source 最终普通 env.sh；本轮未修改消费者。若进入正式分发，仍需版本发布、受信签名 bundle、精确制品身份与消费者原生重验，另获授权。
3. release RequiredCapabilities 支持 `native_sql_context` 的显式启动门；local-development prepare 使用 `native_sql_context@1`。四符号、feature、capability完整组必须匹配，不允许部分ABI或仅靠symbol推断准入。既有 release-only storage gate未放开。
4. recovery owner 必须保持到同步调用及 session 清理完成。不能将 legacy blocking query 包进 goroutine 后到期返回，不能提前释放目录owner，也不能只看 `ctx.Err()` 就写“rollback完成”。
5. CodeResultIndeterminate 判定须先于一般 cancelled/deadline 分类。保留原提交cause和读取cause，以原始完整请求/摘要/history身份查 exact receipt；未查到不等于未提交，不得换payload复用request id或盲重放。SaaS 自身 receipt/错误封装验收仍由其owner执行。
6. 原生 Open/准入/首个 pool probe、无checkpoint的 parser/结果编码/排序等区间、fsync/OS I/O、强制 cleanup与Close不具无条件墙钟上界。提前warm pool是减少恢复路径工作的实践，不是硬上界证明。
7. 如果需要进程级故障时的硬响应时间，必须另行设计受控单一目录owner进程、撤销/重建和原回执恢复协议；杀线程或释放悬挂owner不能作为替代。本轮没有实施进程隔离或硬抢占。

## 6. 交付层级

- **源码/组件**：本 SDK 独立树实现与验证；`context-evidence/sdk-context-only.patch` 是相对既有 dirty 开发基线的独立变化，不把其他并行工作归成本轮。每文件hash见 `source-files.json`；基线可用补丁预检查通过。
- **本机开发制品**：最终普通/test-seam Core固定字节、SDK独立Prepare配置、真实 native 证据成立。Core其他专项源修/Primary oplog证据以其正式报告为准：普通native16项、seam41项、SQL回归827项通过，均为其任务执行证据。
- **正式消费者**：仅核对锁v0.7.5；没有升级后的 SaaS业务闭环运行证据。
- **发布制品**：没有发版、tag、push、公开包或签名发布。
- **生产准入**：没有部署、生产写入或准入。不能将本机unsigned dirty产物用于证明这些状态。
