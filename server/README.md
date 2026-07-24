# MyApphub Server

仅使用 Go 标准库实现的 HTTP 服务端，使用文件目录和 JSON 元数据存储，不依赖数据库。

## 环境要求

- Go 1.24 或更高版本。
- macOS 或 Linux。
- 运行和构建服务端不需要 Node.js、Android SDK、Xcode，也不需要存在 `web/` 目录。

## 本地运行

在 `server/` 目录启动，确保所有相对数据路径都落在服务端项目内：

```bash
cd server

export APPHUB_ROOT=.
export APPHUB_TOKEN='local-development-admin-token-0001'
export APPHUB_HMAC_SECRET='local-development-hmac-secret-0001'
export APPHUB_ALLOW_HTTP=true
export APPHUB_PUBLIC_URL=http://127.0.0.1:8787
export APPHUB_PROJECT_NAME=MyApphub
export APPHUB_CORS_ORIGINS=http://localhost:5173,http://127.0.0.1:5173

make build
./bin/apphub-server
```

服务默认监听 `127.0.0.1:8787`。另开终端检查：

```bash
curl http://127.0.0.1:8787/healthz
```

正常返回示例：

```json
{"ok":true,"apps":0,"diskAvailableBytes":123456789}
```

本地数据写入 `server/data/`，运行文件和日志写入 `server/runtime/`。停止服务使用 `Ctrl+C`。没有本地签名证书时，UDID 接口返回 `udid_not_configured`，不会影响上传、查询和下载功能。

## 本地测试

服务端测试使用临时目录和 `httptest`，不需要提前启动服务，也不会写入正式 `data/`：

```bash
cd server

# 全部单元测试和 HTTP 集成测试
make test

# 增加数据竞争检测，提交代码前必须执行
make test-race

# Go 静态检查
make vet

# 确认服务端没有第三方 Go 包，也没有 Web 项目路径引用
make check-deps

# 20,000 个版本的列表性能基准
make benchmark

# 构建服务端二进制
make build
```

只运行指定包或测试：

```bash
go test ./internal/config
go test ./internal/httpapi -run TestCORS -v
go test ./internal/store -run Test -v
```

构建产物为 `server/bin/apphub-server`。`make check-deps` 会验证依赖图只包含 Go 标准库和本项目包，并阻止服务端源码、构建配置或环境变量重新引用 Web 项目。服务端只提供 HTTP API 和安装资源，不读取、复制或托管任何 Web 文件。

## 配置与启动

配置项示例见 [.env.example](.env.example)。程序直接读取环境变量，不会自动加载 `.env` 文件。

建议部署结构如下：

```text
apphub-server/
  apphub-server
  profile-cert.pem
  profile-key.pem
  data/
  runtime/
```

Web 应用应由 Nginx、Caddy、对象存储静态站点或其他独立静态服务发布。服务端部署目录中不需要包含 Web 构建产物。

服务进程的工作目录设置为 `server/`。所有相对服务端路径都以 `APPHUB_ROOT` 为基准，默认值如下：

```text
APPHUB_ROOT=.
APPHUB_DATA_DIR=data
APPHUB_RUNTIME_DIR=runtime
APPHUB_LOG_FILE=runtime/apphub.log
APPHUB_PROFILE_CERT=profile-cert.pem
APPHUB_PROFILE_KEY=profile-key.pem
APPHUB_PROJECT_NAME=MyApphub
```

因此不需要再把路径写死为 `/opt/myapphub/...`。这些路径全部属于服务端自身。

`APPHUB_PROJECT_NAME` 是部署实例的显示名称。服务端通过 `GET /api/public/config` 提供给独立部署的 Web，并将其写入 UDID 描述文件；未配置或只包含空白字符时使用 `MyApphub`。

前后端使用不同 Origin 时，通过白名单开放跨域访问：

```text
APPHUB_CORS_ORIGINS=https://hub.example.com
```

多个 Web Origin 使用英文逗号分隔。只填写 `scheme://host[:port]`，不能填写路径，也不允许使用 `*`。服务端只允许前端需要的 `GET`、`HEAD`、`POST`、`DELETE` 和 `Content-Type`/`Accept` 请求头，不允许浏览器跨域携带管理接口使用的 `Authorization` 请求头。

```bash
export APPHUB_TOKEN='至少32字节的管理Token'
export APPHUB_HMAC_SECRET='另一个至少32字节的随机密钥'
export APPHUB_ALLOW_HTTP=true
export APPHUB_PUBLIC_URL=http://127.0.0.1:8787
export APPHUB_PROJECT_NAME='My Team App Hub'

./bin/apphub-server
```

生产环境必须使用 HTTPS 反向代理，并且不应启用 `APPHUB_ALLOW_HTTP`。UDID 描述文件使用与 `apphub-server` 同级的 `profile-cert.pem` 和 `profile-key.pem`，不使用 IPA 签名私钥。未设置证书环境变量时，服务端会自动查找这两个同级文件；两者都不存在时关闭 UDID 功能。私钥文件权限应设为仅服务进程账号可读。

服务端根路径、`/app/...`、`/udid` 以及其他未注册路径统一返回 JSON `404`。Web 路由回退由 Web 自己的静态服务器配置，不能交给 Go 服务端处理。

## 数据目录

```text
server/
  data/
    apps/{slug}/
      {ios|android}/
        app.json
        icon.png|jpg
        versions/{versionId}/
          version.json
          package.ipa|apk
    .staging/
    .trash/
  runtime/
    udid/
    apphub.log
  profile-cert.pem
  profile-key.pem
```

完整备份和恢复需要包含整个 `data/` 目录。`runtime/udid` 仅存放短期临时会话。

## API

管理接口使用 `Authorization: Bearer {APPHUB_TOKEN}`：

```text
GET    /api/v1/apps
POST   /api/v1/packages
DELETE /api/v1/apps/{slug}
POST   /api/v1/apps/{slug}/versions/{version}/promote
DELETE /api/v1/apps/{slug}/versions/{version}
POST   /api/v1/apps/{slug}/versions/{version}/restore
PUT    /api/v1/apps/{slug}/password
```

公开安装与 UDID 接口：

```text
GET    /api/public/config
GET    /api/public/apps/{slug}
POST   /api/public/apps/{slug}/access
GET    /download/{slug}/{platform}/{version}?token=...
GET    /manifest/{slug}/{platform}/{version}?token=...
POST   /api/udid/sessions
GET    /api/udid/profile/{challenge}
PUT    /api/udid/callback
GET    /api/udid/result/{challenge}
DELETE /api/udid/result/{challenge}
```

### 查询公开站点配置

```http
GET /api/public/config
```

响应可公开缓存五分钟，不需要管理 Token：

```json
{"projectName":"MyApphub"}
```

### 查询公开应用

```http
GET /api/public/apps/{slug}?platform=ios|android
```

同一 `slug` 可以同时包含 iOS 和 Android。响应顶层 `app`、`version` 是按查询参数选择的平台；`variants` 包含该产品全部可安装平台。未传 `platform` 时返回第一个可安装平台。

存在当前版本时返回 `200`：

```json
{
  "app": {
    "schemaVersion": 1,
    "slug": "example",
    "name": "Example",
    "platform": "ios",
    "identifier": "com.example.app",
    "icon": "icon.png",
    "passwordProtected": false,
    "createdAt": "2026-07-22T00:00:00Z",
    "updatedAt": "2026-07-22T00:00:00Z"
  },
  "version": {
    "schemaVersion": 1,
    "id": "version-id",
    "versionName": "1.0.0",
    "buildNumber": "1",
    "packageFile": "package.ipa",
    "sha256": "...",
    "fileSize": 123456,
    "uploadedAt": "2026-07-22T00:00:00Z"
  },
  "variants": [
    {"app": {"slug":"example","platform":"ios"}, "version": {"id":"ios-version-id"}},
    {"app": {"slug":"example","platform":"android"}, "version": {"id":"android-version-id"}}
  ]
}
```

应用存在但没有当前版本时仍返回 `503`，同时附带可安全公开的应用信息；不会暴露版本文件或安装地址：

```json
{
  "code": "no_current_version",
  "message": "No installable version",
  "app": {
    "schemaVersion": 1,
    "slug": "example",
    "name": "Example",
    "platform": "ios",
    "identifier": "com.example.app",
    "passwordProtected": false,
    "createdAt": "2026-07-22T00:00:00Z",
    "updatedAt": "2026-07-22T00:00:00Z"
  }
}
```

### 获取安装权限

```http
POST /api/public/apps/{slug}/access
Content-Type: application/json

{"password":"可为空的分享密码","platform":"ios"}
```

成功返回 `200`。Android 使用 `downloadUrl`，iOS 还会返回 `installUrl`；两个地址中的短期令牌均受当前密码版本约束：

```json
{
  "downloadUrl": "https://hub.example/download/example/ios/version-id?token=...",
  "installUrl": "itms-services://?action=download-manifest&url=...",
  "expiresAt": "2026-07-22T01:00:00Z"
}
```

### UDID 会话

创建会话无需请求体内容（可发送 `{}`）：

```http
POST /api/udid/sessions
Content-Type: application/json

{}
```

成功返回 `201`：

```json
{
  "challenge": "一次性挑战值",
  "profileUrl": "/api/udid/profile/一次性挑战值",
  "resultUrl": "/api/udid/result/一次性挑战值"
}
```

轮询 `resultUrl` 时，等待中返回 `{"pending":true}`；完成后返回 `{"pending":false,"udid":"..."}`。复制完成后应调用 `DELETE /api/udid/result/{challenge}` 提前删除结果。会话默认在配置的有效期后失效。

### 公开错误

错误统一为 JSON：

```json
{"code":"错误代码","message":"可读说明"}
```

常见错误码：

| HTTP | code | 含义 |
| --- | --- | --- |
| 400 | `invalid_json` | 请求 JSON 无效或含未知字段 |
| 401 | `invalid_password` | 分享密码不正确 |
| 401 | `invalid_token` | 下载或 Manifest 令牌无效、过期或已因密码变更失效 |
| 404 | `not_found` | 应用、版本或服务端资源不存在 |
| 404 | `session_expired` | UDID 会话不存在或已过期 |
| 409 | `challenge_used` | UDID challenge 已被消费 |
| 429 | `rate_limited` | 请求超过限额 |
| 503 | `no_current_version` | 应用暂无当前版本，并附带 `app` |
| 503 | `udid_not_configured` | 尚未配置 UDID 描述文件签名 |

`429 rate_limited` 额外携带标准 `Retry-After` 响应头，值为再次提交前应等待的秒数，例如：

```http
Retry-After: 527
```

未匹配的 `/api`、`/download`、`/manifest` 和应用图标路径会返回服务端 `404`，不会回退到 Vue 页面。
