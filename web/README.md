# MyApphub Web

Vue 3 + TypeScript + Vite 实现的公开安装页。视觉和交互规范见 [`../docs/WEB_UI_SPEC.md`](../docs/WEB_UI_SPEC.md)。

## 环境要求

- Node.js 20 或更高版本。
- npm。

## 本地运行

### 连接本地 Go 服务

先启动 `server/`，然后在另一个终端执行：

```bash
cd web
npm ci
npm run dev
```

打开 `http://localhost:5173`。Vite 默认把 `/api`、`/assets`、`/download` 和 `/manifest` 代理到 `http://127.0.0.1:8787`，因此本地联调时不要设置 `VITE_API_BASE_URL`。

停止开发服务器使用 `Ctrl+C`。

### 只运行前端示例

无需启动 Go 服务即可查看示例数据：

```bash
cd web
npm ci
npm run dev:demo
```

然后访问：

```text
http://localhost:5173/app/whatsapp
http://localhost:5173/app/instagram
http://localhost:5173/app/example-learning
```

## 前后端分离配置

前后端部署在不同 Origin 时，在构建前配置 Go 服务的公开地址：

```bash
VITE_API_BASE_URL=https://api.hub.example.com npm run build
```

该地址会同时用于 JSON API、站点名称、应用图标和 UDID 描述文件。Go 服务端的 `APPHUB_CORS_ORIGINS` 必须包含 Web 页面 Origin，例如 `https://hub.example.com`。站点名称只需在服务端设置 `APPHUB_PROJECT_NAME`，Web 会在启动后读取；读取失败时回退为 `MyApphub`，不会阻塞安装页。

如果静态服务器配置了 CSP，`connect-src` 和 `img-src` 也必须包含 API Origin，否则浏览器会在 CORS 之前阻止请求。例如：

```text
Content-Security-Policy: default-src 'self'; script-src 'self'; connect-src 'self' https://api.hub.example.com; img-src 'self' data: https://api.hub.example.com; style-src 'self' 'unsafe-inline'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'
```

## 本地测试与构建

Web 测试使用 jsdom 和模拟 API，不需要启动 Go 服务：

```bash
cd web
npm ci

# 全部 Vitest 测试
npm test

# Vue 和 TypeScript 类型检查
npm run typecheck

# 生产构建
npm run build
```

只运行指定测试：

```bash
npm test -- src/api/client.spec.ts
npm test -- src/views/AppInstallView.spec.ts
```

生产构建输出到 `web/dist`，由 Nginx、Caddy 或其他静态服务器独立托管。Go 服务端不会读取这些文件。生产环境不启用示例数据。
