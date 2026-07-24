# MyApphub 产品需求文档

| 项目 | 内容 |
| --- | --- |
| 文档版本 | v1.0 |
| 文档状态 | 评审版，确认后作为 v1 开发基线 |
| 产品形态 | Go 原生服务端 + Cobra CLI + Vue Web |
| 目标用户 | App 开发人员、测试人员或私有分发用户 |
| 数据方案 | 文件目录 + JSON 元数据，不使用数据库 |
| 部署方式 | 单台自有服务器、单服务实例、可信公网 HTTPS |

## 1. 背景

App 开发完成后，需要交付给安装用户体验或使用。目前逐个连接设备、手工安装 IPA/APK 的方式效率低，也无法通过固定地址持续分发新版本。

本项目需要提供一个类似蒲公英、Fir 的轻量应用托管工具，但不建设完整的应用管理平台。开发者通过命令行上传安装包并管理版本；用户只接触安装页面和独立的 iOS UDID 获取工具。

## 2. 产品目标

### 2.1 核心目标

1. 开发者使用一条 CLI 命令上传 IPA 或 APK。
2. CLI 在开发机本地解析、校验安装包并生成版本元数据。
3. 服务端保存安装包与 JSON 版本记录，不依赖数据库。
4. 每个应用拥有稳定的分享地址和可选的应用级密码。
5. Android 用户可以直接下载 APK。
6. iOS 用户可以通过 `itms-services` 和动态 Manifest 直接安装有效的企业或 Ad Hoc IPA。
7. 提供独立 UDID 工具；用户获取并复制 UDID 后，自行发送给开发者。
8. 系统可以在一台普通自有服务器上低成本运行、备份和恢复。
9. 部署者可以通过一个服务端环境变量自定义站点显示名称。

### 2.2 成功标准

- 上传成功后，固定分享页立即展示新版本，除非上传时明确指定不发布。
- 不登录后台、不安装额外客户端的用户可以完成安装。
- 服务重启后能够完全从文件目录和 JSON 恢复应用与版本信息。
- 未获得管理 Token 的用户不能上传、切换或删除版本。
- 未通过应用密码验证的用户不能下载受保护安装包。
- UDID 不形成长期设备库，临时结果在 30 分钟后失效并清理。

## 3. 用户与权限

### 3.1 开发者

通过 Go CLI 使用管理能力：

- 上传 IPA/APK。
- 查看应用和版本。
- 切换当前版本。
- 删除、恢复历史版本。
- 设置或清除应用分享密码。

开发者使用服务器统一配置的 `APPHUB_TOKEN` 访问管理接口。v1 不区分管理员、上传者或应用负责人。

### 3.2 安装用户

通过浏览器访问：

- 应用安装页。
- UDID 获取工具。

用户不需要 App Hub 账号。受保护应用只需要输入该应用的分享密码。

## 4. 产品边界

### 4.1 v1 包含

- Go 服务端。
- 基于 Cobra 框架开发的 Go CLI。
- Vue 安装页和 UDID 工具页。
- IPA/APK 文件托管。
- 文件目录和 JSON 版本记录。
- 当前版本、历史版本、回滚、回收站。
- 应用级分享密码。
- Android APK 下载。
- iOS 企业/Ad Hoc OTA 安装。
- 临时 UDID 获取和复制。
- 基础安全日志、备份和恢复。

### 4.2 v1 不包含

- Web 上传页或管理后台。
- 用户、角色、权限和企业 SSO。
- 多公司、多租户。
- 用户身份和安装完成追踪。
- 长期设备列表或 UDID 管理。
- Apple Developer API 自动注册设备。
- IPA 自动重签、证书托管。
- MDM、TestFlight、Apple Business Manager。
- Android AAB 分发。
- 定时发布、灰度发布、审批流。
- 对象存储、CDN、微服务和多实例部署。

## 5. 总体产品结构

```text
开发者 Mac
  └─ apphub CLI
       ├─ 本地解析/验证 IPA、APK
       └─ HTTPS 上传安装包与元数据
                    │
                    ▼
自有服务器
  ├─ Go API 服务
  ├─ data/apps 文件仓库
  ├─ runtime/udid 临时数据
  └─ Vue Web
       ├─ /app/{slug} 安装页
       └─ /udid       UDID 工具
```

Go 服务端和 Vue 构建产物可以部署在同一个上级目录下，但保持 `server/` 与 `web/` 两个完全独立的应用。服务端不读取、复制、嵌入或托管前端产物，也不需要安装 Node、Xcode、Android SDK 或安装包解析工具。Web 通过可配置的 API Origin 调用服务端；CLI 使用 Cobra 组织根命令、子命令、参数、帮助信息和错误输出。

源码仓库包含三个相互独立的项目，根目录不创建 Go module：

```text
server/   # 独立 Go module，仅实现服务端
cli/      # 独立 Go module，实现 Cobra CLI
web/      # 独立 Vue 项目
docs/     # 产品与设计文档，不属于任何运行项目
```

三个项目不得通过相对路径或共享内部源码产生构建耦合。CLI 和 Web 只通过服务端公开的 HTTP/JSON API 协作；每个项目拥有自己的依赖、构建、测试和发布入口。

### 5.1 Go 服务端技术约束

Go 服务端必须使用标准库原生开发，不采用第三方 Web 框架或第三方服务端运行时依赖。

允许使用的核心标准库包括：

- `net/http`：HTTP Server、路由、请求和流式响应。
- `encoding/json`、`encoding/xml`、`encoding/asn1`：JSON、Plist/XML 和 CMS 数据编码。
- `io`、`os`、`path/filepath`：流式上传下载和文件管理。
- `os.Root`：限制文件操作范围，防止目录穿越。
- `crypto/*`：随机数、HMAC、SHA-256、PBKDF2、X.509 和签名。
- `log/slog`：结构化安全日志。
- `net/http`：为独立 Web Origin 提供严格白名单 CORS 和 OPTIONS 预检。
- `sync`、`context`、`time`：写锁、超时、清理任务和优雅退出。

明确禁止服务端引入：

- Gin、Echo、Fiber、Chi、Beego 等 Web 框架或路由库。
- GORM、Ent、XORM 等数据库或 ORM 库。
- Viper、Zap、Logrus 等配置和日志库。
- 第三方 JWT、限流、文件存储、密码、Plist 或 PKCS#7/CMS 库。
- Cobra 及其传递依赖。

服务端路由使用 Go 1.22 及以上版本增强后的 `http.ServeMux` 方法与路径模式；配置直接从环境变量读取；日志使用 `slog`；安装令牌采用标准库 HMAC，不采用 JWT 库。

项目最低 Go 版本定为 Go 1.24，原因包括：

- 标准库提供 `crypto/pbkdf2`。
- 标准库提供 `os.Root` 目录受限文件访问。
- 可以在不引入第三方 Go Module 的前提下满足密码和文件安全需求。

服务端和 CLI 位于两个独立 Go module，并分别构建为独立二进制：

```text
apphub-server   # 标准库零第三方依赖
apphub          # Cobra CLI，允许依赖 Cobra
```

服务端不能依赖 CLI module，CLI 也不能导入服务端的 `internal` 包。服务端的 Go Import Graph 不能包含 Cobra。CI 必须从 `server/` 和 `cli/` 分别执行构建与测试，并检查服务端依赖图；发现任何非服务端项目内包、非 Go 标准库包时构建失败。

## 6. CLI 功能需求

### FR-CLI-00 CLI 框架与命令结构

CLI 必须使用 [Cobra](https://github.com/spf13/cobra) 框架开发，不自行实现命令路由和参数解析。Cobra 仅用于 `apphub` CLI，禁止被 `apphub-server` 导入。

固定命令树：

```text
apphub
├── upload <file>
├── list [app-slug]
├── info <app-slug> <version-id>
├── promote <app-slug> <version-id>
├── delete <app-slug> [version-id]
├── restore <app-slug> <version-id>
├── password <app-slug>
└── version
```

Cobra 实现要求：

- 根命令统一处理服务器地址、管理 Token、超时和输出格式。
- `--server`、`--token`、`--timeout`、`--json` 使用 Persistent Flags，所有子命令共享。
- 环境变量作为默认值，命令行参数具有更高优先级。
- 每个子命令必须声明准确的 `Use`、`Short`、参数数量校验和示例。
- 参数或业务错误返回非零退出码，不能在命令内部直接 `os.Exit`。
- 普通结果写入标准输出；进度和提示写入标准错误，保证 `--json` 可被脚本稳定消费。
- `--json` 模式禁止输出进度条、颜色和非 JSON 文本。
- 支持 Shell completion，由 Cobra 生成 `bash`、`zsh`、`fish`、`powershell` 补全脚本。
- 在单元测试中通过注入输入、输出和 API Client 测试命令，不启动真实进程。

### FR-CLI-01 环境配置

CLI 通过环境变量读取服务器和管理 Token：

```bash
export APPHUB_SERVER=https://hub.example.com
export APPHUB_TOKEN=<至少 32 字节随机 Token>
```

同时支持等价的根命令参数：

```bash
apphub --server https://hub.example.com <command>
```

`--token` 仅用于 CI 等受控场景；日常使用优先读取 `APPHUB_TOKEN`，避免 Token 留在 Shell 历史中。Token 不允许出现在上传元数据和普通日志中。

### FR-CLI-02 上传

```bash
apphub upload ./App.ipa --notes "修复登录问题"
apphub upload ./app-release.apk --notes-file ./release-notes.md
apphub upload ./App.ipa --no-promote
```

上传流程：

1. 根据扩展名和文件魔数确认 IPA/APK 类型。
2. 检查 ZIP 条目路径、条目数量和解压后总大小，拒绝路径穿越和压缩炸弹。
3. 验证 IPA/APK 签名。
4. 解析应用名称、标识、版本号、构建号、最低系统版本、图标、签名信息。
5. 计算文件大小和 SHA-256。
6. 在终端展示解析结果。
7. 以流式 multipart 请求上传元数据、图标和安装包。
8. 显示上传进度。
9. 输出固定安装地址。

默认上传成功后成为当前版本；`--no-promote` 仅保存版本。

IPA v1 只允许企业签名和 Ad Hoc 签名。开发签名和 App Store IPA 必须拒绝。

### FR-CLI-03 查询

```bash
apphub list
apphub list <app-slug>
apphub info <app-slug> <version-id>
```

查询结果至少包含应用标识、平台、当前版本、版本号、构建号、文件大小、哈希和上传时间。

### FR-CLI-04 回滚

```bash
apphub promote <app-slug> <version-id>
```

回滚只切换应用的 `currentVersionId`，不能复制或重新上传安装包。

### FR-CLI-05 删除与恢复

```bash
apphub delete <app-slug> --yes
apphub delete <app-slug> <version-id> --yes
apphub restore <app-slug> <version-id>
```

- 只传 Slug 时删除整个产品及其 iOS/Android 平台；传版本 ID 时仅删除指定版本。
- 单版本删除时，当前版本不能删除；整产品删除不受当前版本限制。
- 删除先移动到 `.trash`，保留 7 天。
- 7 天内可以恢复。
- 到期后由后台清理任务物理删除。

### FR-CLI-06 分享密码

```bash
apphub password <app-slug>
apphub password <app-slug> --clear
```

- 密码通过终端隐藏输入。
- 非交互环境可通过安全环境变量传入。
- 密码不能直接出现在命令参数或 Shell 历史中。
- 修改密码后，旧安装令牌必须立即失效。

### FR-CLI-07 支持环境

v1 CLI 优先支持 macOS 13 及以上版本：

- IPA 解析需要 Xcode Command Line Tools 中的 `codesign`、`security`、`plutil`。
- APK 解析需要 Android SDK 中的 `apkanalyzer`、`apksigner`。
- 缺少依赖时必须指出缺少的工具和安装位置，不能只返回“解析失败”。

## 7. 安装 Web 功能需求

Web 的视觉、布局、交互状态、响应式和可访问性规范独立维护，详见 [Web UI 设计说明](./WEB_UI_SPEC.md)。

### FR-WEB-01 固定安装页

```text
https://hub.example.com/app/{slug}
```

`slug` 表示产品而不是单个平台。同一产品的 iOS 与 Android 安装包共用该地址；页面按访问设备优先选择匹配平台，并允许用户手动切换平台。

页面使用 Vue 实现，展示：

- 应用名称和图标。
- iOS/Android 平台。
- 版本号、构建号。
- 文件大小、发布时间。
- 更新说明。
- 安装按钮。
- 分享二维码。

Web 只展示当前版本，不展示上传、回滚、删除等管理功能。

### FR-WEB-02 分享密码

- 应用未设置密码时直接显示安装按钮。
- 设置密码时，用户必须先验证密码。
- 每个“IP + 应用”10 分钟内最多失败 5 次。
- 超限后返回 `429` 并提示稍后重试。
- 验证通过后签发最长 2 小时的短期访问令牌。

### FR-WEB-03 Android 安装

- Android 点击安装后直接下载当前 APK。
- 返回正确的 APK Content-Type。
- 支持 HTTP Range 和断点续传。
- 页面提供允许安装未知来源应用的简短说明。

### FR-WEB-04 iOS 安装

- iOS 点击安装后使用 `itms-services` 打开动态 Manifest。
- Manifest 包含应用 Bundle ID、版本、名称和受保护 IPA 地址。
- Manifest 令牌与 IPA 下载令牌用途隔离，不能互换。
- 企业 IPA 页面提示首次信任开发者的步骤。
- Ad Hoc IPA 页面提示：设备 UDID 必须已经包含在当前描述文件中。
- iOS/iPadOS 18 及以上提示手工安装企业应用后可能需要重启完成信任。

Apple OTA 安装依赖有效签名、Manifest、IPA 和可信 HTTPS，平台不能绕过 Apple 的设备与证书限制。参见 [Apple 内部应用分发说明](https://support.apple.com/guide/deployment/depce7cefc4d/web)。

### FR-WEB-05 平台提示

- 手机平台匹配时突出显示安装按钮。
- 电脑或平台不匹配时显示二维码和正确设备打开提示。
- 安装页适配常见手机宽度和中英文长文本。

## 8. UDID 工具需求

### FR-UDID-01 使用流程

```text
https://hub.example.com/udid
```

1. 用户使用 iPhone/iPad Safari 打开页面。
2. 点击“获取 UDID”。
3. 服务端创建一次性 Challenge。
4. 用户下载并安装签名 `.mobileconfig`。
5. Apple 设备将 UDID 回调至服务端。
6. 用户返回 Safari，页面轮询并展示完整 UDID。
7. 用户点击复制，自行发送给开发者。

### FR-UDID-02 数据边界

- UDID 不关联应用或用户。
- 不形成设备列表。
- 完整 UDID 不写入访问日志和错误日志。
- 临时会话 30 分钟后失效并删除。
- 用户复制成功后可以立即删除临时结果。
- 服务重启导致未完成会话失效可以接受。

### FR-UDID-03 描述文件

- Challenge 至少使用 32 字节安全随机数。
- Challenge 一次性使用，禁止重放。
- `.mobileconfig` 使用独立描述文件签名证书。
- 不使用 IPA 签名私钥。
- 未配置签名证书时，生产环境必须关闭 UDID 功能并返回明确错误；不能降级为未签名描述文件。

UDID 获取采用 Apple Profile Service 的 Challenge 与设备回调机制。参见 [Apple Profile Service 文档](https://developer.apple.com/library/archive/documentation/NetworkingInternet/Conceptual/iPhoneOTAConfiguration/profile-service/profile-service.html)。

## 9. 文件存储需求

### 9.1 目录结构

```text
data/
  apps/{slug}/
    {platform}/
      app.json
      icon.png|jpg
      versions/{versionId}/
        version.json
        package.ipa|apk
  .staging/
  .trash/

runtime/
  udid/
```

### 9.2 应用识别

应用唯一键为：

```text
平台 + Bundle ID/Package Name
```

- `slug` 是产品唯一键，一个 slug 最多包含一个 iOS 变体和一个 Android 变体。
- 两个平台上传时指定相同 slug，应归入同一产品，不得自动追加 `-2`。
- 唯一键不存在时自动创建应用目录。
- 已存在时创建新版本。
- 文件哈希完全一致时返回已有版本，不重复保存。
- 版本号和构建号相同但哈希不同时拒绝上传，要求修改构建号。

### 9.3 JSON 规范

`app.json` 至少包含：

- `schemaVersion`
- `slug`
- `platform`
- `identifier`
- `name`
- `icon`
- `currentVersionId`
- `sharePasswordHash`
- `passwordVersion`
- `createdAt`
- `updatedAt`

`version.json` 至少包含：

- `schemaVersion`
- `id`
- `versionName`
- `buildNumber`
- `packageFile`
- `sha256`
- `fileSize`
- `minimumOsVersion`
- `distributionType`
- `provisioningExpiresAt`
- `signingCertificateSha256`
- `releaseNotes`
- `uploadedAt`

所有 JSON 必须带 `schemaVersion`，为后续格式升级或迁移数据库保留入口。

### 9.4 一致性

- 上传先流式写入 `.staging`。
- 服务端重新计算大小和 SHA-256。
- 校验成功后原子移动到正式版本目录。
- JSON 使用“同目录临时文件 + fsync + 原子重命名”。
- 写操作使用全局文件锁；下载不获取写锁。
- 服务启动时扫描 JSON 重建内存索引。
- 不维护独立的全局 `index.json`。
- 损坏记录只隔离并报警，不能自动删除安装包。

## 10. 接口边界

### 10.1 管理接口

统一使用：

```http
Authorization: Bearer {APPHUB_TOKEN}
```

最小接口能力：

- 上传安装包和元数据。
- 查询应用和版本。
- 切换当前版本。
- 删除、恢复版本。
- 设置、清除应用密码。

管理接口不提供浏览器登录、账号、Token 签发或角色接口。

### 10.2 公共安装接口

- 返回公开站点配置，包括 `APPHUB_PROJECT_NAME` 对应的项目名称。
- 查询当前应用信息。
- 验证分享密码。
- 签发短期安装令牌。
- 返回 APK/IPA。
- 动态生成 iOS Manifest。
- 返回应用图标。

### 10.3 UDID 接口

- 创建临时 Challenge。
- 下载 `.mobileconfig`。
- 接收 Apple 设备回调。
- 查询临时结果。
- 主动删除临时结果。

## 11. 安全需求

### NFR-SEC-01 管理接口

- `APPHUB_TOKEN` 至少 32 字节随机值。
- 使用恒定时间比较 Token。
- 管理接口建议只允许可信网络、VPN 或 IP 白名单访问。
- 错误响应不包含堆栈、磁盘路径、Token 和系统配置。

### NFR-SEC-02 安装包

- 安装包位于 Web Root 之外。
- 安装包文件不具备执行权限。
- 服务端忽略客户端原始文件路径，使用受控目录名和文件名。
- Slug、版本 ID 拒绝 `/`、`\`、`..`、控制字符。
- 单包默认上限 1 GB。
- 磁盘剩余空间低于“待上传包两倍 + 10 GB”时拒绝上传。

文件上传安全规则参照 [OWASP File Upload Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/File_Upload_Cheat_Sheet.html)。

### NFR-SEC-03 密码和令牌

- 分享密码使用 Go 1.24 标准库 `crypto/pbkdf2`，采用 PBKDF2-HMAC-SHA256、独立随机盐和可升级迭代参数保存。
- 安装令牌使用 HMAC-SHA-256 签名。
- 令牌包含应用、版本、用途、签发时间、过期时间、`passwordVersion`。
- 修改密码后旧令牌立即失效。
- 更新说明按纯文本输出，禁止注入 HTML/JavaScript。

PBKDF2-HMAC-SHA256 默认使用不少于 600,000 次迭代；实际参数在基准服务器上验证后写入密码哈希记录，后续允许升级。密码存储要求参照 [OWASP Password Storage Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html)。Go 1.24 已将 `crypto/pbkdf2` 纳入标准库，参见 [Go 1.24 Release Notes](https://go.dev/doc/go1.24#new-crypto-packages)。

### NFR-SEC-04 Web 与部署

- 生产环境只允许可信 HTTPS。
- 设置 HSTS、CSP、`X-Content-Type-Options`、`Referrer-Policy`。
- 上传、密码验证、下载和 UDID 创建分别限流。
- 服务进程使用非 root 用户。
- 日志不得记录 Token、分享密码、安装令牌或完整 UDID。

## 12. 性能与容量需求

v1 基准环境：

- 2 核 CPU。
- 4 GB 内存。
- SSD。
- 200 Mbps 公网出口。
- 不超过 100 个应用。
- 每个应用不超过 200 个版本。
- 单包不超过 1 GB，日常包体不超过 500 MB。
- 同时下载不超过 20 人。

验收指标：

- 安装页 API P95 小于 300 ms，不含安装包传输时间。
- 20,000 个版本时，CLI `list` P95 小于 500 ms。
- 服务启动并重建索引小于 5 秒。
- 上传 500 MB 文件时，服务额外内存不超过 100 MB。
- 20 路并发下载无 OOM，聚合吞吐达到可用出口带宽的 80% 以上。
- 上传和下载均为流式处理。
- 下载支持 Range；版本切换不能中断已有下载。

超过 100 人并发或单包超过 2 GB 时，安装文件迁移到对象存储/CDN，不在 v1 单机方案内继续扩容。

## 13. 运维与恢复

- 每日备份整个 `data/` 目录。
- 每月执行一次空服务器恢复演练。
- 日志必须轮转，避免占满磁盘。
- 服务启动时清理超时 staging、过期 UDID 和超过 7 天的回收站。
- 健康检查至少报告服务状态、应用数量和磁盘剩余空间；不能暴露敏感数据。
- 完整复制 `data/` 后，新服务器应能重建全部应用和版本。

## 14. 验收清单

### 功能验收

- CLI 能上传并解析企业 IPA、Ad Hoc IPA 和 APK。
- 上传后固定链接展示正确当前版本。
- `--no-promote` 不影响当前版本。
- CLI 可以查询、回滚、删除、恢复和修改分享密码。
- Android 实机可以下载安装 APK。
- iOS 实机可以触发企业/Ad Hoc OTA 安装。
- UDID 工具可以获得并复制 UDID。

### 安全验收

- 错误 Token、错误密码、过期令牌、篡改令牌和跨用途令牌均被拒绝。
- 路径穿越、伪造扩展名、损坏 ZIP、超大文件、版本冲突均被拒绝。
- 安装文件不能通过真实磁盘路径或猜测静态地址访问。
- UDID Challenge 不能重放，日志中不存在完整 UDID。
- 修改应用密码后旧安装令牌立即失效。
- `apphub-server` 的依赖图中不存在 Cobra、Web 框架或其他第三方 Go 包。

### 稳定性验收

- 上传中断不产生正式半成品版本。
- JSON 写入中断不破坏旧记录。
- 服务重启后能够重建索引。
- 当前版本不能误删。
- 回收站版本可以恢复。
- 20 路并发下载、Range 和断点续传通过测试。

## 15. 开发前置条件

在开始正式开发前必须完成：

1. 本文档评审并冻结为 v1 基线。
2. 删除当前仓库中已废弃的 Node 原型代码。
3. 建立标准库 Go 服务端、Cobra CLI 和 Vue 前端工程结构。
4. 确认测试用企业 IPA、Ad Hoc IPA、APK 各一个。
5. 准备开发环境用 HTTPS 域名或反向代理方案。
6. 准备用于签署 UDID 描述文件的独立证书与私钥。
