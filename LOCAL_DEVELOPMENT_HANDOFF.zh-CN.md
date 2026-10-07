# 本地开发原生准入交接（2026-10-04）

已实现独立、明确 opt-in 的本地开发准入及稳定准备 CLI。SaaS 开发阶段可直接
消费已构建的 dirty/debug Core 与 SDK 源码，无需发布 Core、talon-bin 或 SDK。
默认签名发布准入、clean/release 约束和 SDK 操作能力门保留。

最终源码：`/Users/dark/.codex/worktrees/native-local-development/talon-sdk-go`。
Git 基线：`7847de0`；本次改动未提交。完整保留 `native-sql-contract` 候选的
BEGIN error-chain、invalid write/COMMIT response indeterminate 修复与契约测试；
这四个代码/测试文件与原候选逐字节核对一致。原 checkout、原候选、ai-platform
均未修改。未推送、发布、部署，未操作业务数据库；验证只使用临时数据目录。

## SaaS 现在可消费的配置

已准备好：

```sh
source /Users/dark/.codex/worktrees/native-local-development/talon-sdk-go/local-development-evidence-config/env.sh
```

这个 shell 中其他 `TALON_NATIVE_*` 发布字段必须先清除；混用会拒绝，而不会
自动丢弃发布信任锚。配置仅固定以下三项：

```text
TALON_NATIVE_MODE=local-development
TALON_NATIVE_DEV_POLICY_FILE=/Users/dark/.codex/worktrees/native-local-development/talon-sdk-go/local-development-evidence-config/policy.json
TALON_NATIVE_DEV_POLICY_SHA256=<env.sh 中固定值>
```

policy 固定库/header/真实 ABI 自证文件的规范绝对路径、SHA256、ABI profile
和 version，及明确的 required capability `name@version`。三个文件均须保留。
已准备配置对应的真实 Core：

- 库：`/Users/dark/.codex/worktrees/sql-operability/superclaw-db/target/sql-operability-evidence/libtalon.dylib`
- header：`/Users/dark/.codex/worktrees/sql-operability/superclaw-db/include/talon.h`
- SHA256：`53181b0a784fe50e081b5929bc66a227ee2c2c7027ce1b5342a6fad91832611b`
- 真实自证：Core `6010d748aebd3c4e595534ed4a9e959e7e5334cf`，`git_dirty=true`，
  `core_semver=0.1.1`，target `aarch64-apple-darwin`，ABI `talon-native-c@1`。
- 自证原始字节 SHA256：
  `9559f8eb8d99042f5795a579160b5c1958f3c2ce7ecf2f81bc9c28841d5423e9`。
- `debug` 是调用方根据构建事实声明的 profile；当前 Core ABI 不含 profile
  字段，SDK 明确以 `BuildProfileSource=caller-declared (not attested by Core ABI)`
  标注，不能将其称为二进制自证。

## 重新构建 Core 后准备新配置

```sh
cd /Users/dark/.codex/worktrees/native-local-development/talon-sdk-go
CGO_ENABLED=1 GOWORK=off go run ./cmd/talon-native-dev prepare \
  --library /Users/dark/.codex/worktrees/sql-operability/superclaw-db/target/sql-operability-evidence/libtalon.dylib \
  --header /Users/dark/.codex/worktrees/sql-operability/superclaw-db/include/talon.h \
  --build-profile debug --out /absolute/new/development-config
source /absolute/new/development-config/env.sh
```

`--out` 必须是父目录已存在的新目录，已有目录拒绝且不覆盖。若用 `mktemp -d`
创建父目录，传其尚不存在的 `config` 子目录。CLI 会执行明确选择的本地原生
代码，通过实际 `talon_build_manifest` 提取自证；不要求 Core 发布，不读取
fixture 伪装发布，不创建签名。profile 可声明 debug/release/unknown，默认 unknown。
CLI 默认 `--require native_sql_result@2,native_sql_session@1,native_shared_core@1`。
即使将 require 缩小，也不能绕过 SDK 固有 ABI、manifest、功能与操作能力门。

加载先读取并校验准确库字节，再复制到权限受限的私有临时目录 dlopen，避免
构建路径在 hash 与加载之间被重写。运行时自证必须与固定原始自证 SHA256 完全
一致；之后继续核验 target、header、精确符号集、build binding、feature 和
capability。dirty 值来自原始自证及 build binding。一个进程只接受一个 policy；
修改 Core/配置后应重新准备并重启，不支持进程内切换、热替换或同时加载发布库。

## Go 源码消费方式

临时 modfile 的 replace 指向最终源码，保持 SaaS 正式 go.mod/go.sum 不变：

```sh
# 在 SaaS 模块目录执行；这里只准备消费者构建输入和核对映射。
sdk_dev_mod_dir=$(mktemp -d "${TMPDIR:-/tmp}/talon-sdk-dev-mod.XXXXXX")
cp go.mod "$sdk_dev_mod_dir/dev.mod"
if test -f go.sum; then cp go.sum "$sdk_dev_mod_dir/dev.sum"; fi
GOWORK=off go mod edit -modfile "$sdk_dev_mod_dir/dev.mod" \
  -replace github.com/darkmice/talon-sdk-go=/Users/dark/.codex/worktrees/native-local-development/talon-sdk-go
GOWORK=off go list -modfile "$sdk_dev_mod_dir/dev.mod" -m github.com/darkmice/talon-sdk-go
```

SaaS wrapper 选择自己的启动/验收命令，并附加
`CGO_ENABLED=1 GOWORK=off` 与 `-modfile "$sdk_dev_mod_dir/dev.mod"`；需要新增
依赖时可用 `-mod=mod`，它仅更新临时 modfile/sum。wrapper/runbook 和真实 SaaS
验收归 ai-platform，本次没有修改或执行 SaaS 的业务路径。

GoFrame 继续通过原有 `talon.Open` 读取开发 env；ConfigNode.Name 仍为本地
数据库绝对目录。直接 SDK 调用方也可先读 `LocalDevelopmentPolicyFromEnvironment()`，
再传 `OpenOptions{LocalDevelopment: &policy}`；不能同时传 `Native`。

`NativeInfo.Admission=local-development`，真实 `CoreGitDirty=true`，发布 tag、
Core tag/repository、TalonBinCommit 和签名 key 字段为空，不制造来源中没有的信息。
默认发布身份的 Admission 为 release。发布专属 `storage_conditional_batch_v1`
外层 gate 在开发身份仍为 gated。不要把本地开发身份用于生产准入。

## 已验证证据

日志及独立消费者源码：`local-development-evidence/`。配置在
`local-development-evidence-config/`；这些是本机开发证据，不是发布包。

- `go test . ./goframe ./cmd/talon-native-dev` 的组件回归通过。
- 最终全仓：`CGO_ENABLED=1 GOWORK=off go test -ldflags=-linkmode=external ./...`
  通过（根包、GoFrame、serverprotocol、server；CLI 编译通过）。最初直接
  `go test ./...` 的两个纯 Go 测试可执行文件遇 macOS `missing LC_UUID`，
  改用系统外部链接器后全部通过；没有修改二进制掩盖错误。
- 真实 `TestLocalDevelopmentRealAdmission`：dirty/debug 明确 opt-in 成功；
  同配置撤销 mode 默认发布路径拒绝；同一真实 dirty 自证在发布校验分支拒绝。
  错误 library/header/self-manifest SHA256、不支持 ABI、真实 gated
  `revision_stream@2` 均拒绝。将语义相同但字节不同的自证重新 pin 后，
  真正 loaded ABI 的自证交叉校验仍拒绝。开发不能打开发布专属 gate。
- 组件回归还覆盖实际文件内容篡改、更新 pin 后仍错误的 ABI/header/binding、
  missing feature、gated SQL capability、policy SHA、未 opt-in 和混合策略拒绝。
- `TestNativeSQLConsumer(ReadYourWrites|CommitRestart|ExactValues|SingleOwner)`：
  binary SQL、SQL v2 和 GoFrame 三路径通过。复合键索引参数查询在同 TX INSERT
  后见到一行，扫描与索引一致；DELETE/ROLLBACK 后可见性恢复；精确值通过；
  正常 Close 和不 Close 的 `os.Exit` 后新进程读回 schema/已提交数据；
  单事务属主 Busy 保留。
- `TestLocalDevelopmentCommitKillRestart`：三路径在成功 COMMIT 后插入未提交
  行，子进程以 SIGKILL 终止；新进程索引查回已提交回执，未提交行不存在。
- 独立消费者模块 `local-development-evidence/consumer` 正式 go.mod 仍要求
  SDK v0.7.5；只有 `consumer.mod` replace 到最终源码，真实运行
  `talon.Open` + GoFrame TX 参数 INSERT/索引 SELECT，结果一行且输出真实开发身份。
  复跑：在该目录 source 上面的 env，再执行
  `CGO_ENABLED=1 GOWORK=off go run -mod=mod -modfile "$PWD/consumer.mod" .`。
- CLI 已有输出目录拒绝，无覆盖；`git diff --check` 通过。

复跑关键集成测试：

```sh
cd /Users/dark/.codex/worktrees/native-local-development/talon-sdk-go
source local-development-evidence-config/env.sh
TALON_TEST_LOCAL_DEVELOPMENT=1 CGO_ENABLED=1 GOWORK=off \
  go test . -run '^TestLocalDevelopmentRealAdmission$' -v
TALON_TEST_EMBEDDED_NATIVE=1 TALON_TEST_LOCAL_DEVELOPMENT=1 CGO_ENABLED=1 GOWORK=off \
  go test ./goframe -run '^Test(NativeSQLConsumer(ReadYourWrites|CommitRestart|ExactValues|SingleOwner)|LocalDevelopmentCommitKillRestart)$' -v
```

## 未验证边界

已验证的是本机 macos-arm64 源码与开发制品/独立消费者，不是 ai-platform SaaS
正式端到端、发布签名制品、生产准入或其他平台的运行验收。Linux/macOS-amd64
使用相同结构性平台门，但没有在本次运行；Windows 不在既有 native 平台契约中。
本次未验收 COUNT(DISTINCT) 和完整 SQL 表面，也未改连接池、存储锁、复制/HA。
SIGKILL 与 fsync 成功边界不等于断电、控制器/文件系统故障测试。
本地路径与 env 是调用方显式选择的开发信任输入，不提供发布级签名身份保证。
