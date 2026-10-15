# MyApphub

MyApphub 是私有 IPA/APK 分发工具。公开安装页沿用定稿的 Vue UI；新增 `/admin` 管理端；在线 API 使用 TypeScript Cloudflare Worker，元数据存 D1，安装包与图标存私有 R2。Go CLI 只在开发机解析、校验和上传 IPA/APK，线上不运行 Go 服务。

当前线上地址：[apphub.bensonday.cc](https://apphub.bensonday.cc)。管理端为 [/admin](https://apphub.bensonday.cc/admin)。本次部署生成的管理令牌保存在本机 `.secrets/production-admin-token`，该目录不纳入 Git；请另行安全备份。UDID 描述文件采用独立签名证书；首次安装时请核对签名者提示。

## 目录

```text
web/          Vue 公开安装页、UDID 页和管理端
worker/       Cloudflare Worker API、签名和清理逻辑
migrations/   D1 数据库迁移
cli/          本机安装包解析、签名校验与分块上传
scripts/      本地端到端验证脚本
docs/         产品需求与定稿 UI 说明
```

## 本地开发

需要 Node.js 20+、npm、Go 1.24+ 和 Wrangler 4.45+。先构建 Web，再初始化本地 D1、启动 Worker：

```bash
cd web && npm ci && npm run build && cd ..
cp .dev.vars.example .dev.vars
# 将 .dev.vars 中的 APPHUB_TOKEN 和 APPHUB_HMAC_SECRET 换成两个不同的随机值
wrangler d1 migrations apply myapphub --local
wrangler dev --local
```

打开 `/app/{slug}`、`/udid` 或 `/admin`。管理端登录使用 `APPHUB_TOKEN`，登录后通过 HttpOnly 会话 Cookie 访问 API。没有配置描述文件证书时，UDID 接口返回 `udid_not_configured`，其他功能可用。本地数据由 Wrangler 保存在 `.wrangler/`，与旧 `server/data/` 完全无关。

CLI 连接同一个 Worker：

```bash
cd cli && make build
export APPHUB_SERVER=http://127.0.0.1:8787
export APPHUB_TOKEN='与 .dev.vars 一致的管理令牌'
./bin/apphub upload ./App.ipa
```

CLI 在本机使用 macOS `codesign`、`security`、`plutil` 或 Android SDK `apkanalyzer`、`apksigner` 检查包，计算 SHA-256，按 8 MiB 分块上传。详见 [CLI 说明](cli/README.md)。

线上上传时，仓库内的 `cli/bin/apphub` 默认连接 `https://apphub.bensonday.cc` 并读取 `.secrets/production-admin-token`。旧的 `APPHUB_PUBLIC_URL` 不再影响 CLI；本地联调须显式设置 `APPHUB_SERVER=http://127.0.0.1:8787` 和对应的 `APPHUB_TOKEN`。

iOS Development 测试包可以上传，但仅能在描述文件包含的设备上运行，且设备需启用开发者模式。安装页仅提供网页安装入口；若设备无法通过网页安装，开发人员可在本机使用 Xcode 或 Apple Configurator。

## Cloudflare 部署

`wrangler.jsonc` 描述 Worker、静态资源、D1、R2 和十分钟一次的过期清理任务。首次发布时，Wrangler 会按配置创建缺少的 D1/R2 资源；随后应用远端迁移，再设置 Worker secrets：

```bash
cd web && npm ci && npm run build && cd ..
wrangler login
wrangler deploy
wrangler d1 migrations apply myapphub --remote
wrangler secret put APPHUB_TOKEN
wrangler secret put APPHUB_HMAC_SECRET
```

两个令牌请分别使用独立的高强度随机值，并妥善保存管理令牌，CLI 和管理端登录都需要它。首次部署到迁移与 Secret 设置完成期间，API 会暂时不可用；完成后再运行线上冒烟检查。

| Secret | 用途 |
| --- | --- |
| `APPHUB_TOKEN` | CLI 与管理端登录共用的至少 32 字节随机令牌 |
| `APPHUB_HMAC_SECRET` | 管理会话和下载链接签名，至少 32 字节，必须与管理令牌不同 |
| `APPHUB_PROFILE_CERT_B64` | UDID 描述文件签名证书 PEM 的 Base64 |
| `APPHUB_PROFILE_KEY_B64` | 对应 PKCS#8 RSA 私钥 PEM 的 Base64 |
| `APPHUB_DEVICE_CA_B64` | 用于验证设备回调证书链的可信 CA 证书 PEM 的 Base64 |

后三项缺失时 UDID 功能关闭。设备 CA 使用 Apple 官方文档提供的 Apple iPhone Device CA，存于 `worker/certs/apple-iphone-device-ca.pem`；部署时将证书和签名私钥分别转换为 Base64 后写入 Secret。当前签名证书为独立生成的自签名证书，iOS 安装时可能显示未验证签名；更换为受信任的签名证书后应重新配置前两项 Secret。私钥必须以 Secret 配置，不能提交到仓库。`APPHUB_PROJECT_NAME` 是非敏感站点名称，可在配置中修改。部署和远端迁移需要已登录且有相应权限的 Cloudflare 账号；切勿将本地 `.dev.vars` 上传。

生产域名为 `https://apphub.bensonday.cc`，使用 HTTPS。UDID 描述文件签名和回调验证已通过本地模拟；仍需用真实 iPhone Safari 验证完整安装流程及 iOS OTA 安装。R2 桶保持私有；安装包通过 Worker 的短期令牌接口下载。版本删除进入七天回收站，定时任务到期后删除 D1 记录与 R2 对象。

## 验证

```bash
(cd web && npm test && npm run typecheck && npm run build)
web/node_modules/.bin/tsc -p worker/tsconfig.json
(cd cli && make test && make vet)
wrangler deploy --dry-run
```

在本地 Worker 与模拟 D1/R2 已启动后，可运行 `python3 scripts/local-smoke.py`。配置本地测试证书后可运行 `python3 scripts/local-udid-smoke.py`。

产品范围见 [产品需求](docs/PRD.md)，公开页定稿视觉要求见 [Web UI 说明](docs/WEB_UI_SPEC.md)。安全问题请按 [SECURITY.md](SECURITY.md) 报告。
