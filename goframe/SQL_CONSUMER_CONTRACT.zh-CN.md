# ai-platform native SQL 回归契约（2026-10-04）

SDK 错误链与结果分类已在本地修正；索引事务可见性、聚合 DISTINCT 和提交耐久性仍由 Core 修复。消费者仅升级 SDK 不能关闭这三项缺口。

## 当前证据

本任务从 SDK 主线 `7847de0f0d1b4018fb1a4569d8e5cdc713a8f0f4` 建立独立工作树，保留原 `codex/shared-native-sessions` 检出。本地修改尚未提交或发布。

| 层级 | 实际证据与边界 |
| --- | --- |
| SDK 源码/组件 | GoFrame v2.8.3；全包外部链接器测试、pure-Go Server/protocol 测试与 vet；错误结果注入为 fake native 单测，未声称真实 Core 已产生 uncertain 或畸形响应 |
| 本地签名 bundle | 清洁、自证 Core `a968c4db18d1f14b51d9d4b2e4f55ec5a642e72c`，macos-arm64；fixture 临时签名，不是生产信任锚；原互操作矩阵通过，新增严格契约失败 |
| 已发布制品 | 缓存的 go-runtime v0.1.54，经 SDK 正常签名、摘要、身份、自证验证加载；Core `6010d748aebd3c4e595534ed4a9e959e7e5334cf`，动态库 SHA256 `2033f36e4666175e5bfe30e253993aeb91710d3782ca551d537d2f7ab4ce4610`；本次没有重新下载或发布任何制品 |
| 消费者运行 | ai-platform/saas 仍锁定 SDK v0.7.5、go-runtime v0.1.54、GoFrame v2.8.3；五个真实 schema 测试实跑通过，其中复合 PK 测试通过表示它成功复现旧缺陷，不表示健康契约通过 |
| 生产准入 | 未操作生产数据、部署或凭据；没有生产验收。本次失败的健康契约阻止将该组合称为完整可用 |

日志在 [native-contract-evidence](native-contract-evidence/)。所有 SQL 均使用 native ABI；没有以 Server HTTP、PgWire 或 RESP 的结果替代。

## Core 归属与更精确的结论

`TestNativeSQLConsumerReadYourWrites` 在三个独立目录分别通过 binary `DB.Query/Exec`、v2 `DB.QueryResult` 和 GoFrame `gdb` 执行同样的绑定 SQL。三路一致：

- 新 INSERT 的复合 PK 行，全扫读到；`operator_id` 与 `role_id` 二级索引等值查询读不到。
- `EXPLAIN` 确认 `operator_id` 走 Index scan。首次无 `operator_id` 索引的对照运行走 Full table scan + filter，等值读能看到未提交行。不能再把原因概括为“复合主键等值读不可见”。消费者当前真实 0001 DDL 的两条单列索引已纳入正式契约。
- 单列 PK 回执表的主键点读可见。DELETE 后全扫与等值读均为空，ROLLBACK 后原行恢复；COMMIT 后复合 PK 等值读可见。

当前 Core `engine_select.rs` 的二级索引分支先从已存储索引枚举候选，再 `tx_get` 取行；它没有合入 `TxState.index_writes` 中的新索引候选。主键点读直接 `tx_get`，全扫使用事务缓冲。该源码机制与三路对照吻合，但还需要 Core 修复后的同制品回归；SDK 不通过 SQL 改写、隐式扫描或会话锁掩盖它。Core 应统一索引候选与事务视图，并覆盖 INSERT、UPDATE、DELETE、range/IN 等访问路径，不仅修一个 SQL 字符串。

`TestNativeSQLConsumerDistinctAggregate`：三条指派行、两个 operator；投影 DISTINCT 得到两行，`COUNT(DISTINCT operator_id)` 得到 3，应为 2。binary 与 result_v2 已返回同样的错误计数，责任在 Core 聚合语义。派生表查询未纳入新契约，不据此声称支持。

`TestNativeSQLConsumerCommitRestart`：六组全新子进程，明确在 writer COMMIT 成功后退出、再启动 reader。三路 clean Close 都读到 schema 与行；三路未 Close 立即退出都不能恢复已提交行。发布制品上 direct binary/result_v2 保留 schema 而丢行；GoFrame 首次得到 0 表，再跑保留表但仍丢行；因此不能把“0 表”推广成每个 native 入口的唯一现象，也不能将 Close 当成 COMMIT 耐久性的替代。该实验是进程退出恢复测试，不证明电源故障耐久性。当前 Core storage 的 PersistGuard 注释也明确 batch.commit 不等待 fsync，后台定时 SyncAll；这解释了该实现为何不能承诺立即耐久，但不能代替制品实测。Core 必须在声明耐久提交前保证数据、schema 与日志持久边界；SDK 不在 COMMIT 后补一个 Persist 来模拟新的提交契约。

对应 Core 任务 `01a10725-c413-7a11-ab17-f63a0e0a3ef9` 仅作为上下文，本任务未向其发送消息或编辑 Core 源码。

## SDK 本地修复与错误契约

1. BEGIN 能力检查用两个 `%w` 保留 `ErrTransactionsUnavailable`、底层 `TalonError` 与 cause；`errors.Is/As`、`ErrorCodeOf`、`NativeCodeOf` 均可继续使用。
2. BEGIN 的 `busy` 是明确拒绝，保持 `native_unclassified` / `busy`，不误分类为能力缺失或提交不确定。
3. Core 已返回 `uncertain` 时原样传播。COMMIT 或写语句已派发后，结果解码协议错误、DML affected_rows 缺失或溢出时，顶层为 `CodeResultIndeterminate`，cause 保留 `CodeProtocolViolation` 与原始 error chain。明确的 Core 拒绝保留原分类；不从诊断文本判断。
4. 不自动重试、回滚或补发 COMMIT。native SQL 当前没有本测试已验证的 authenticated receipt API；结果不确定须靠业务幂等标识和重新读取/核账恢复，不能照搬条件 KV receipt 契约。

单目录显式事务期间，第二个 BEGIN，以及另一句柄和外层 db 的 SELECT/INSERT/UPDATE/DELETE 均被拒；回滚后只有已提交种子行，之后新的事务可提交。测试给第二个请求独立 context，避免 GoFrame TXFromCtx 将它变成 SAVEPOINT。应用回调的每个语句仍必须经 tx；单句柄、连接池大小或增加句柄均不提供并行事务保证。此处没有 SDK 自建事务锁。

## 精确值证据

三个 native 入口均验证 typed DECIMAL `1234567890123456.78` 与 `-0.01`、int64 最大/最小值，以及 INTEGER nanos `1791062400123456789` 的绑定、事务内回读、提交、Close 后重新打开回读。大 INTEGER/nanos 等值参数也检查命中。金额未经过 float64。

这是闭合值集合的往返证据，不是 DECIMAL SUM/AVG、范围索引、所有 schema precision/scale 的承诺。Go string 参数是 TEXT；查询金额需 `talon.DecimalValue`。Go `time.Time` 仍映射为毫秒 TIMESTAMP；整数 nanos 用 `int64`/INTEGER，不声称 TIMESTAMP 自动保留纳秒。

## 消费者升级的最小依赖

- 需要包含本地错误链/结果分类修复的 SDK 新版本。只更新 go-runtime 不能取得 SDK 错误契约修复；目前尚无本任务的新版本号或发布制品。
- 需要包含索引事务视图、COUNT(DISTINCT) 与耐久提交修复的可信签名 Core bundle，再由 go-runtime 明确锁定该制品并完成 SDK 自证验证；不能仅替换未签名 dylib 或放宽门。
- 消费者 `inTransaction` 已只向 body 传 tx，无需为了这项修复改 SQL 调用入口。保留单目录事务属主协调和关闭句柄；这不补足崩溃耐久性。
- 分类逻辑保留原始 SDK cause，BEGIN busy 不当作能力缺失；COMMIT/DML 的 `CodeResultIndeterminate` 不盲重试。财务参数继续传 typed DECIMAL、整数 nanos。
- Core 还需提供真实提交失败注入：分别证明 BEGIN 未派发/被拒、提交前拒绝、提交应用或持久化阶段不确定的机器码。SDK fake 测试只证明传播和包装，不能证明 Core 按阶段返回正确代码。
- Core 新制品到位后先跑 SDK 健康契约，再跑消费者真实 schema、财务/授权和进程恢复测试。消费者旧“预期索引读不到”测试需改成健康断言，并分别移除已实测可退役的有界扫描/应用计数补偿。本任务没有修改消费者补偿路径。

运行发布制品契约：

```sh
TALON_TEST_EMBEDDED_NATIVE=1 GOWORK=off CGO_ENABLED=1 \
  go test -ldflags=-linkmode=external ./goframe -run '^TestNativeSQLConsumer' -count=1 -v
```

已知缺陷发生时必须 FAIL；未启用 native 测试时的 SKIP 不计作验收。环境/签名加载失败同样 FAIL，不降级到 fake、其他协议或诊断字符串。
