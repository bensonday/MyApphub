# MyApphub

MyApphub 是一个可自行部署的开源 IPA/APK 轻量托管工具。开发者通过 CLI 上传和管理版本，安装用户通过固定分享页获取应用。仓库由三个完全独立的项目组成，根目录不是 Go module，也不负责统一构建。

部署者可以通过服务端环境变量自定义站点名称：

```bash
export APPHUB_PROJECT_NAME='My Team App Hub'
```

Web 页签、底部署名和 iOS UDID 描述文件会自动读取该名称；未配置时使用 `MyApphub`。

## 项目结构

```text
MyApphub/
  server/   纯 Go 标准库 HTTP 服务端，独立 go.mod
  cli/      Cobra 命令行工具，独立 go.mod
  web/      Vue 3 + TypeScript + Vite Web 应用
  docs/     产品需求与 Web UI 设计文档
```

- 服务端只提供 API、应用图标、安装包、Manifest 和 UDID 接口，不读取或托管前端文件。
- Web 通过 `VITE_API_BASE_URL` 调用服务端，跨域时由 `APPHUB_CORS_ORIGINS` 放行指定 Web Origin。
- CLI 通过管理 API 与服务端通信，不导入服务端源码。
- 三个项目分别构建、测试和发布。

## 本地联调

要求：

- Go 1.24 或更高版本。
- Node.js 20 或更高版本。
- npm。

### 1. 启动服务端

打开第一个终端：

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

健康检查：

```bash
curl http://127.0.0.1:8787/healthz
```

本地数据会写入 `server/data/`，日志写入 `server/runtime/apphub.log`。如果没有放置 `profile-cert.pem` 和 `profile-key.pem`，只有 UDID 功能不可用，其他功能可以正常开发。

### 2. 启动 Web

打开第二个终端：

```bash
cd web
npm ci
npm run dev
```

打开 `http://localhost:5173`。Vite 会把 `/api`、`/assets`、`/download` 和 `/manifest` 代理到 `http://127.0.0.1:8787`，本地开发不需要额外配置 API 地址或 CORS。

如果暂时不启动服务端，可以运行带示例数据的 Web：

```bash
cd web
npm run dev:demo
```

然后访问 `/app/whatsapp`、`/app/instagram` 或 `/app/example-learning`。

### 3. 使用 CLI

打开第三个终端：

```bash
cd cli
make build

export APPHUB_SERVER=http://127.0.0.1:8787
export APPHUB_TOKEN='local-development-admin-token-0001'

./bin/apphub list
```

上传真实 IPA/APK 前，还需要准备对应的本机解析工具，详见 [CLI README](cli/README.md)。

## 本地测试

每个项目的测试相互独立，不需要同时启动服务端或 Web。

```bash
# 服务端
(cd server && make test)
(cd server && make test-race)
(cd server && make vet)
(cd server && make check-deps)

# CLI
(cd cli && make test)
(cd cli && make test-race)
(cd cli && make vet)

# Web
(cd web && npm ci)
(cd web && npm test)
(cd web && npm run typecheck)
(cd web && npm run build)
```

更完整的命令和单项测试方法分别见：

- [server/README.md](server/README.md)
- [cli/README.md](cli/README.md)
- [web/README.md](web/README.md)

产品范围见 [docs/PRD.md](docs/PRD.md)，前端设计见 [docs/WEB_UI_SPEC.md](docs/WEB_UI_SPEC.md)。

## 参与贡献与安全

项目使用 [MIT License](LICENSE)。提交改动前请阅读 [CONTRIBUTING.md](CONTRIBUTING.md) 和 [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)；安全问题请按 [SECURITY.md](SECURITY.md) 私下报告。
