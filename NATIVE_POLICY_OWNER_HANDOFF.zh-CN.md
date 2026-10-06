# SDK native policy owner 模块交接（2026-10-06）

结论：显式原 policy bytes/file + 外部 pin 验证、三 locator 搬迁证明、独立
loader/Core 自证，以及 GoFrame 同一已持有连接的身份绑定已实现并通过本机验收。
这是源码与 unsigned-local-development 的证据，不是 SDK 已发布或 SaaS 已上线。

## 实际源码与责任边界

- 消费候选：`/Users/dark/.codex/worktrees/native-local-development/talon-sdk-go`，
  HEAD `7847de0f0d1b4018fb1a4569d8e5cdc713a8f0f4`，dirty、未提交。
- 主 checkout HEAD `fb524b6c4cd9c2d57ca6dd04a97c495bace1d596` 保持原状；它不是
  此消费者候选。当前聊天与任务清单没有另一个 active SDK owner，因此按已授权
  的候选责任边界复用此树，没有创建/覆盖另一份 SDK 主树实现。
- 开工前保护基线 150 个现存非隐藏文件；本次只改已有 `native_development.go`、
  `native_development_cgo.go`、`talon.go`。其余 147 个逐字节保持，未丢失文件，
  包括已有 GoFrame SQL/error-chain、契约和文档并行改动。
  新增代码单独放在 `native_development_owner.go`、`goframe/native_identity.go`
  和对应 tests、`cmd/talon-native-policy`。现存 `goframe/native_sql.go` 未改写。
- 依据 [Talon mandatory skill](/Users/dark/.agents/skills/talon-sdk-go-best-practices/SKILL.md)
  和对应 verification-and-release 规则保持静态/真实运行/发布/生产证据分层。
  TypeSafe `jev-1.13.0` 架构 Choice 返回 layered=1.0、confidence=1.0，实际采用
  共享静态 owner + 显式 runtime gate；概率不代替代码验权或运行证据。

## API 与调用方责任

完整签名和边界见 [API 说明](NATIVE_POLICY_OWNER_API.zh-CN.md)。关键入口：

| 调用方需求 | SDK owner 入口 |
| --- | --- |
| bytes/file 外部 pin + 静态文件/权限/ABI/能力核验 | `VerifyLocalDevelopmentPolicy` / `VerifyLocalDevelopmentPolicyFile` |
| 不开数据库的真实 loader/Core 自证 | `AttestLocalDevelopmentPolicy` / `AttestLocalDevelopmentPolicyFile` |
| 原 policy 逐字节保留，仅三个 locator token 变化 | `DeriveLocalDevelopmentPolicy` / `VerifyLocalDevelopmentPolicyRelocation` |
| SDK 已有 handle 的实际身份与 policy 绑定 | `DB.CheckedNativeInfo` / `DB.VerifyLocalDevelopmentPolicy` |
| GoFrame 已有 `sql.Conn` 的实际身份与 policy 绑定 | `goframe.ConnNativeInfo` / `goframe.VerifyConnLocalDevelopmentPolicy`，或 Raw 中的 `NativeIdentityProvider` |

静态校验与 Open 共用原 artifact validator；probe 与 Open 共用同一
`loadAndAttestNative` 和 `attestLoadedNative`。未复制到 ai-platform、未读/写进程
global env、未扩大 capability、未制造 bundle/signature/trust。policy 仍只有
library/header/self-manifest 三 locator。严格原字节解析与 pin，拒绝 unknown、
duplicate、漏字段、profile/ABI mismatch、错误路径、文件 budget、symlink、
group/world writable，以及不满足的 capability/feature/build binding。

搬迁联合 API **先验原件，再验搬迁件**，只替换 JSON string token，其他每个
原字节保留，输出是新的 byte slice。profile 与 required capabilities 的改动即使
能独立通过静态验证，也不能通过搬迁证明。`null` 与空数组的 capabilities
切片复制保持原形，避免隐式改写完整 policy hash 或导致重复 admission 身份冲突。

同连接身份 API 不从外部文件推断 loaded identity；它转交该连接原来的 `DB`
owner，比较 Open 已保留的完整 policy hash，且读取同一个 handle 的 NativeInfo。
不 acquire pool、不 Open 第二 handle、不执行 SQL或操作 TX。

## 当前真实 Core

- 库：`/Users/dark/.codex/worktrees/unique-index-tx-overlay/superclaw-db/target/unique-index-tx-evidence-20261006/libtalon.dylib`
- 库 SHA256：`5c72bd38a38a4baee2115c02ca50cfecd14505282fc8f26d668b2c9c9a21ffec`，开工与交付时核对一致，未重编 Rust。
- Core 自证 commit：`6010d748aebd3c4e595534ed4a9e959e7e5334cf`，git_dirty=true，
  `aarch64-apple-darwin`，`talon-native-c@1`。
- header SHA256：`455eb8ed49b8d112134e766f0c6a5cbfd0f76bf9285bebcaaae2616b3d1eebe9`。
- 原 self-manifest SHA256：`9559f8eb8d99042f5795a579160b5c1958f3c2ce7ecf2f81bc9c28841d5423e9`。
- 本次新 policy 原字节 SHA256：`c09d8cdeb55391470d53a548aaf6c67495c9bbd790a255941253cbf355bc106b`。
  新配置仅存于 `native-policy-owner-evidence/current-core-config/`；未覆盖旧配置。
- `debug` 仍是 caller-declared、不是 Core 自证；`unsigned-local-development`
  标签保留，signed release 字段为空，`storage_conditional_batch_v1` 保持 gated。

## 已完成 QA 与证据

先完整实现与 fixtures，再集中运行，日志位于 `native-policy-owner-evidence/`：

1. `component.log`：`CGO_ENABLED=1 GOWORK=off go test -count=1 -ldflags=-linkmode=external ./...`
   通过：SDK、GoFrame、serverprotocol、server；两个 CLI 编译通过。使用 macOS
   系统外部链接器，未处理/改写测试二进制掩盖 LC_UUID 问题。
2. 单元 fixtures：外部 pin、严格 JSON（含转义 duplicate 和大小写别名）、
   三文件 tamper/empty/missing/symlink/directory/permissions/budget、OS cause、
   ABI/target/symbol/header/binding/feature/capability、搬迁原件先验/保留字节、
   loader 拒绝非 library fixture 与完整错误 cause、GoFrame owner 缺失/关闭拒绝。
3. `real-core.log`：`TestLocalPolicyOwnerRealCore` 通过：真实库原址与搬迁 loader
   自证；同语义但原字节不同、重新 pin 的 self-manifest 静态通过但 runtime 拒绝；
   失败 probe 后恢复；真实 gated capability 拒绝；已 Open handle 与外部 policy
   绑定，locator/profile 不匹配拒绝；同一 dirty 自证不能冒充 signed release。
   probe 后 `loadedNative` 未被 admission，原 policy 文件逐字节不变。
4. 同一 `real-core.log`：`TestNativeIdentityRealConnectionDuringTransaction` 通过：
   已持有的实际 native SQL conn 的身份/原policy pin 检查，文件有效但 loaded
   profile 不匹配拒绝；检查后 TX 自有写入可见，ROLLBACK/COMMIT 正常；pool
   始终一条物理连接。只使用临时目录和 SDK 自有 probe 表，没有商业 DB。
5. `cli-static.json` / `cli-runtime.json`：独立命令入口在新进程实际运行，两层
   输出分别为 static-artifacts / loader-core-attested，原始 SHA 和真实库身份一致。
6. `external-consumer.json`：独立 Go 模块正式 require SDK v0.7.5，仅通过 replace
   映射本地候选（`consumer-sdk-resolution.json`）。实际调用 SDK owner callback、
   真正 GoFrame `gdb.New -> Master -> sql.Conn` 的身份与 TX 中的同连接断言；
   env 不变、held pool 只有一条连接。运行源码在 `consumer/`，未修改 SaaS。
7. 最后代码复核发现原有 slice clone 会把 `[]` 变成 `null`，改变 policy hash；
   修正后仅定向验 `PreservesEmptyRequirementIdentity`、locator 搬迁和同连接错误
   cause（`targeted-identity.log`）。fixture 漏字段/缩减 capability 拒绝也定向
   通过（`targeted-fixture.log`）；未再重复完整全仓/商业生命周期。
8. `preservation.json` / `protected-baseline.json`：保留现存/并行文件的 hash 证据；
   `git diff --check` 通过，主 checkout clean。`SOURCE_MANIFEST.json` 固定最终交付
   源码与 QA 日志文件 hash，便于消费者核对当前候选。

复跑真实 owner acceptance（只操作临时 DB/自有 probe 表）：

```sh
cd /Users/dark/.codex/worktrees/native-local-development/talon-sdk-go
source native-policy-owner-evidence/current-core-config/env.sh
TALON_TEST_LOCAL_POLICY_OWNER=1 \
TALON_TEST_OWNER_LIBRARY_SHA256=5c72bd38a38a4baee2115c02ca50cfecd14505282fc8f26d668b2c9c9a21ffec \
CGO_ENABLED=1 GOWORK=off go test -count=1 -ldflags=-linkmode=external . ./goframe \
  -run '^Test(LocalPolicyOwnerRealCore|NativeIdentityRealConnectionDuringTransaction)$' -v
```

复跑独立消费示例：source 同一新配置后，在 `native-policy-owner-evidence/consumer`
执行 `CGO_ENABLED=1 GOWORK=off go run -mod=mod -ldflags=-linkmode=external .
--policy "$TALON_NATIVE_DEV_POLICY_FILE" --sha256 "$TALON_NATIVE_DEV_POLICY_SHA256"`。
外层 shell 是显式选择 env 的示例；owner API 本身不读取或变更 env。

## 仍归消费方的边界

ai-platform `tools/saas-artifact/artifact/policy.go` 四 locator 合同与 SDK 三 locator
不一致是消费方独立问题，此处没有增加 `bundle_manifest_path` 绕过它。消费方
应修正合同/接入回调，然后用外部 index 断言与 SDK owner 的验证结果逐项核对，
再在真正 startup composition 持有的目标连接上调用同连接身份 API。

本次没有把实际 ai-platform 安装/ADR0072 startup composition 接线称为完成；
没有运行 ai-platform 商业表/数据库、生产数据或生产进程。未验收其他平台或
完整 SQL 表面、没有生产准入，未 commit/push/merge/tag/发布 SDK。
