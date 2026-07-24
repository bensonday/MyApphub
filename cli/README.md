# MyApphub CLI

基于 Cobra 的独立命令行项目。CLI 在开发机本地解析、验证 IPA/APK，生成版本元数据，然后通过 HTTP API 上传到 App Hub 服务端。

## 环境要求

- Go 1.24 或更高版本。
- IPA 解析仅支持 macOS，需要系统自带的 `codesign`、`security`、`plutil`。
- APK 解析需要 Android SDK 的 `apkanalyzer` 和 `apksigner`。
- Android SDK 路径优先读取 `ANDROID_SDK_ROOT`，其次读取 `ANDROID_HOME`，未配置时默认使用 `$HOME/Library/Android/sdk`。

## 本地运行

先按 [服务端 README](../server/README.md) 启动本地服务，然后在另一个终端执行：

```bash
cd cli
make build

export APPHUB_SERVER=http://127.0.0.1:8787
export APPHUB_TOKEN='local-development-admin-token-0001'

./bin/apphub version
./bin/apphub list
```

本地服务地址是 `localhost` 或 `127.0.0.1` 时允许 HTTP；连接远程服务必须使用 HTTPS。

上传真实 IPA：

```bash
./bin/apphub upload ./App.ipa --notes "本地联调上传"
```

上传真实 APK：

```bash
./bin/apphub upload ./app-release.apk --notes "本地联调上传"
```

IPA 上传需要 macOS 系统工具；APK 上传需要 Android SDK 工具。`list`、`info`、`promote`、`delete`、`restore` 和 `password` 不需要本机安装包解析工具。

## 本地测试

CLI 测试使用模拟 API 和临时安装包，不需要启动服务端，也不会上传真实文件：

```bash
cd cli

# 全部单元测试
make test

# 数据竞争检测
make test-race

# Go 静态检查
make vet

# 构建 CLI
make build
```

只运行指定包或测试：

```bash
go test ./internal/cli -v
go test ./internal/client -v
go test ./internal/packageinfo -run Test -v
```

构建产物为 `cli/bin/apphub`。第三方依赖仅存在于本项目的 `go.mod`，目前固定使用 Cobra v1.10.2。

## 连接配置

建议通过环境变量配置服务端地址和管理 Token：

```bash
export APPHUB_SERVER=https://hub.example.com
export APPHUB_TOKEN='与服务端一致且不少于32字节的管理Token'
```

| 环境变量 | 必填 | 说明 |
| --- | --- | --- |
| `APPHUB_SERVER` | 否 | App Hub 服务端地址；未设置时复用 `APPHUB_PUBLIC_URL`，两者都没有时使用 `http://127.0.0.1:8787`。远程地址必须使用 HTTPS。 |
| `APPHUB_TOKEN` | 是 | 管理 Token，至少 32 字节。 |
| `APPHUB_SHARE_PASSWORD` | 否 | 执行 `password` 时使用的应用分享密码，适用于无人值守脚本。 |
| `ANDROID_SDK_ROOT` | APK | Android SDK 路径。 |
| `ANDROID_HOME` | APK | `ANDROID_SDK_ROOT` 未设置时的备用 Android SDK 路径。 |

## 全局参数

全局参数可以写在子命令之前或之后：

```text
--server string       覆盖 APPHUB_SERVER 或 APPHUB_PUBLIC_URL
--token string        覆盖 APPHUB_TOKEN
--timeout duration    单次请求超时，默认 30m
--json                输出机器可读 JSON
-h, --help            显示帮助
```

不要在多人可见的终端历史、CI 日志或进程列表中使用 `--token`；日常使用优先设置 `APPHUB_TOKEN`。

`--json` 适用于脚本调用。启用后，命令结果写入标准输出，上传进度和提示不会混入标准输出：

```bash
./bin/apphub --json list
./bin/apphub upload ./App.ipa --json
```

## 命令总览

```text
apphub upload       校验并上传 IPA/APK
apphub list         查询应用及其全部版本
apphub info         查询一个版本的详细信息
apphub promote      将指定版本设为当前安装版本
apphub delete       将版本移入七天回收站
apphub restore      从回收站恢复版本
apphub password     设置或清除应用分享密码
apphub version      显示 CLI 版本
apphub completion   生成 Shell 补全脚本
apphub help         查看帮助
```

## `upload`：上传安装包

```text
apphub upload <file.ipa|file.apk> [flags]
```

上传前会在本机完成以下工作：

- 确认文件是安全、有效的 ZIP 格式 IPA/APK。
- 拒绝绝对路径、目录穿越、符号链接、超过 100,000 个条目或解压后超过 4 GB 的包。
- IPA：校验代码签名，读取 `Info.plist` 和 `embedded.mobileprovision`，只接受 Enterprise 或 Ad Hoc 分发包。
- APK：使用 `apksigner` 校验签名，使用 `apkanalyzer` 读取包名、版本和最低 SDK。
- 计算文件大小和 SHA-256。
- 尝试从安装包提取图标；也可以通过 `--icon` 覆盖。

参数：

| 参数 | 说明 |
| --- | --- |
| `--notes string` | 直接填写版本说明。 |
| `--notes-file path` | 从 UTF-8 文本文件读取版本说明，不能和 `--notes` 同时使用。 |
| `--name string` | 覆盖从安装包读取的应用名称。 |
| `--icon path` | 使用指定 PNG/JPEG 图标。 |
| `--slug string` | 指定产品 URL Slug。iOS 与 Android 使用相同值会进入同一分享页；也可把已有平台重新归入该 Slug。 |
| `--no-promote` | 只上传版本，不将其设为当前安装版本。默认上传成功后立即设为当前版本。 |

示例：

```bash
./bin/apphub upload ./App.ipa --notes "修复登录问题"
./bin/apphub upload ./app-release.apk --notes-file ./release-notes.md
./bin/apphub upload ./App.ipa --name "示例应用内测版" --icon ./icon.png
./bin/apphub upload ./App.ipa --slug example --no-promote
./bin/apphub upload ./app.apk --slug example
```

如果服务端已存在 SHA-256 完全相同的文件，命令返回已有版本，不会重复保存。如果版本号和构建号相同但文件内容不同，服务端会拒绝上传。

## `list`：查询应用和版本

```text
apphub list [app-slug]
```

不传 Slug 时列出所有平台应用及其版本；传入 Slug 时显示该产品下的 iOS/Android 平台。文本输出中的 `*` 表示各平台当前安装版本。

```bash
./bin/apphub list
./bin/apphub list example-ios
./bin/apphub list --json
```

指定的应用不存在时返回错误 `app not found`。

## `info`：查看版本详情

```text
apphub info <app-slug> <version-id>
```

显示应用名、版本号、构建号、SHA-256 和文件大小。使用 `--json` 时返回完整版本元数据。

```bash
./bin/apphub info example-ios 2.0-20-aabbccdd
./bin/apphub info example-ios 2.0-20-aabbccdd --json
```

版本不存在时返回错误 `version not found`。

## `promote`：切换当前版本

```text
apphub promote <app-slug> <version-id>
```

将指定版本设置为安装页当前提供的版本。切换只影响新请求，不会中断已经开始的下载。

```bash
./bin/apphub promote example-ios 2.0-20-aabbccdd
```

## `delete`：删除应用或版本

```text
apphub delete <app-slug> --yes
apphub delete <app-slug> <version-id> --yes
```

删除操作必须显式添加 `--yes`，否则不会发起请求。只传 Slug 时删除该产品下的 iOS 和 Android；再传版本 ID 时只删除该版本。内容会被移动到服务端回收站，默认保留七天。

```bash
./bin/apphub delete example --yes
./bin/apphub delete example-ios 1.9-19-11223344 --yes
./bin/apphub delete example-ios 1.9-19-11223344 --yes --json
```

服务端不允许单独删除当前版本，需要先通过 `promote` 切换到其他版本；删除整个应用时不受此限制。

## `restore`：恢复版本

```text
apphub restore <app-slug> <version-id>
```

在回收站保留期限内恢复版本。恢复不会自动把该版本设为当前版本；如需重新发布，再执行 `promote`。

```bash
./bin/apphub restore example-ios 1.9-19-11223344
```

## `password`：设置或清除分享密码

设置密码：

```text
apphub password <app-slug>
```

交互执行时会提示输入新密码，并在真实终端中关闭输入回显：

```bash
./bin/apphub password example-ios
```

自动化脚本可通过环境变量传入密码：

```bash
APPHUB_SHARE_PASSWORD='新的分享密码' ./bin/apphub password example-ios
```

清除密码：

```bash
./bin/apphub password example-ios --clear
```

修改或清除密码会使该应用之前签发的安装令牌立即失效。

## `version`：显示版本

```text
apphub version
```

示例：

```bash
./bin/apphub version
```

该命令不连接服务端，不需要配置地址或 Token。

## `completion`：生成 Shell 补全

```text
apphub completion <bash|zsh|fish|powershell>
```

该命令不连接服务端。示例：

```bash
# 当前 zsh 会话生效
source <(./bin/apphub completion zsh)

# 生成文件，具体加载目录按本机 Shell 配置决定
./bin/apphub completion bash > apphub.bash
./bin/apphub completion fish > apphub.fish
./bin/apphub completion powershell > apphub.ps1
```

## `help`：查看帮助

```bash
./bin/apphub help
./bin/apphub help upload
./bin/apphub upload --help
```

参数数量错误、缺少 `--yes`、配置无效或服务端返回错误时，CLI 会向标准错误输出 `apphub: <错误信息>`，并以非零状态码退出。
