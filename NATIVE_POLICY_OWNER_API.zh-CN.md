# SDK 本机 policy 与同连接 native 身份 API

源码责任边界是 `native-local-development/talon-sdk-go` 的 dirty 本机候选，
基线 `7847de0f0d1b4018fb1a4569d8e5cdc713a8f0f4`。这些 API 不提供发布身份。

## 显式外部 pin

```go
verified, err := talon.VerifyLocalDevelopmentPolicy(originalBytes, originalSHA256)
verified, err := talon.VerifyLocalDevelopmentPolicyFile(originalPath, originalSHA256)
```

这是供安装 composition 的 owner 入口：不读/改进程 env，不打开数据库，
不执行原生代码，不创建临时文件。原始字节非空且不超过 1 MiB；外部 pin 必须
是小写 SHA256 并匹配原始字节。policy 必须精确包含当前 11 个字段；拒绝
duplicate（含转义同名）、unknown、大小写别名、漏字段、非法 UTF-8、尾随值。
三个 locator 是规范绝对 UTF-8 路径，不能相同。policy/header/self-manifest
文件各不超过 1 MiB，library 不超过 1 GiB。文件是非空 regular file，拒绝
末端 symlink、group/world writable 和打开前后 inode/size 变化；不修改权限。
父目录、文件所属用户与本机权限由调用方控制；它不提供操作系统沙箱。

共享的静态 owner 核对 library/header/self-manifest 准确字节 SHA256、当前
平台 target、受支持 ABI profile/version/符号集合、Core source identity、
build binding、feature/capability 约束及 caller required capabilities。
`BuildProfile` 仍是调用方声明，当前 Core ABI **不能自证 debug/release**。
报告的 `Stage=static-artifacts` 只说明文件和 sidecar；不能证明二进制实际 ABI。

```go
observed, err := talon.AttestLocalDevelopmentPolicy(originalBytes, originalSHA256)
observed, err := talon.AttestLocalDevelopmentPolicyFile(originalPath, originalSHA256)
```

这是显式执行原生代码的第二层：只允许尚未 admission 的进程，读取已校验库的
准确字节并创建私有 loader copy，复用 `OpenWithOptions` 同一 loader/Core
owner，核对真实自证原始字节、符号与能力，然后卸载、清除 copy。不开数据目录，
不设置/替换 `loadedNative`。构造器/析构器是所选择原生代码的一部分，因此安装
回调不要隐式调用这个 gate；建议以独立子进程显式运行。
`Stage=loader-core-attested` 仍不是数据库 admission，之后 Open 继续校验。

返回的 `Provenance=unsigned-local-development`；`Info.Admission` 继续是既有
`local-development`，签名 key/release 字段为空，`storage_conditional_batch_v1`
保持 gated。未知/不支持的 schema、ABI 和 capability 不靠诊断字符串放行。

## 三 locator 搬迁证明

```go
locators := talon.LocalDevelopmentLocators{
    LibraryPath: installedLibrary,
    HeaderPath: installedHeader,
    BuildManifestPath: installedSelfManifest,
}
derived, proof, err := talon.DeriveLocalDevelopmentPolicy(originalBytes, originalSHA256, locators)
proof, err = talon.VerifyLocalDevelopmentPolicyRelocation(
    originalBytes, originalSHA256, installedBytes, installedSHA256, locators,
)
```

先验证原 policy 和原文件，再验证搬迁后的三个已安装文件。Derive 返回新的
byte slice，不覆盖/改写原文件，安装写入由 composition 持有。只替换三个 JSON
locator string token，字段顺序、空白、其他值的每个原字节全部保留；拒绝
capability 缩放/重排、profile、hash、ABI 或任何其他字节变化，包括格式化。
proof 提供两侧 raw policy SHA256 和剔除 locator token 后的原字节 SHA256，
可放入安装回执；这是内容保留证据，不是签名。检查结果是时点观察，调用方需
保护安装目录并在运行时继续验 pin；不能把 proof 当作免复验的 admission token。

当前 ai-platform `PolicyOwnerVerifier` 可以闭包固定每一侧的外部 pin，然后调用
`VerifyLocalDevelopmentPolicy(raw, expectedPin)`，并由消费方将结果中的 locator
和 library/header/self-manifest digest 与自己的 index 断言。原/搬迁联合证明可
使用上面的 owner API。**现有消费方要求四 locator，但本机 policy 只有三项；
SDK 不提供 `bundle_manifest_path`，该消费方合同修正仍归 ai-platform。**

## 同一已持有的 SQL 连接

```go
info, err := goframe.ConnNativeInfo(existingSQLConn)
info, err = goframe.VerifyConnLocalDevelopmentPolicy(existingSQLConn, policyBytes, policySHA256)
```

只接受已经持有的 `*sql.Conn`；API 不调用 `pool.Conn`、不 Open 第二 handle，
不执行 SQL，不操作事务。`sql.Conn.Raw` 中也可断言 `goframe.NativeIdentityProvider`。
连接将读取转交给它本来的 `*talon.DB`：`CheckedNativeInfo` 读取实际 admission
身份；`DB.VerifyLocalDevelopmentPolicy` 先验外部原字节与文件，再比较这个 handle
在 Open 时持有的完整 policy hash（含 locator/profile/required capabilities）。
因此外部文件 pin 正确但实际 loaded policy 不同仍拒绝。任意相同语义的空白
表达不改变已加载 policy 身份；raw bytes pin 仍独立检查。既有 signed-release
连接不接受 local policy 绑定。关闭连接/DB 拒绝；缺 identity owner fail closed。

GoFrame composition 从已有 `Master()` pool 持有连接后，可在该连接上先断言身份、
再执行启动 SQL；需要贯穿事务的检查也使用同一个 held connection。API 不保证
消费方选择了同一个连接，不能用另一个新开的连接的报告替代目标连接。

所有验证失败使用 `CodeNativeVerification`，真实 dlopen 失败为 `CodeNativeLoad`；
保留 `TalonError.Cause`/OS error chain。连接 nil 为 `CodeInvalidArgument`，缺 owner
为 `CodeCapabilityUnavailable`，关闭连接保持 `sql.ErrConnDone`，关闭 DB 为
`CodeDatabaseClosed`。既有 release 验证及能力门复用原 owner，不放宽。

## 显式命令入口

```sh
CGO_ENABLED=1 GOWORK=off go run ./cmd/talon-native-policy verify --policy ABS --sha256 EXTERNAL_PIN
CGO_ENABLED=1 GOWORK=off go run ./cmd/talon-native-policy attest --policy ABS --sha256 EXTERNAL_PIN
```

输出仅安全身份/digest/stage/release gate，绝不输出 policy 正文或 key。
运行证据与未发布边界见 `NATIVE_POLICY_OWNER_HANDOFF.zh-CN.md`。
