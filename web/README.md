# MyApphub Web

Vue 3 + TypeScript + Vite 提供定稿的公开安装页、UDID 页面和 `/admin` 管理端。公开页视觉规范见 [Web UI 说明](../docs/WEB_UI_SPEC.md)。

## 本地开发

先按 [项目 README](../README.md) 初始化本地 D1 并启动 Worker，再运行 Vite：

```bash
cd web
npm ci
npm run dev
```

打开 `http://localhost:5173`。Vite 将 `/api`、`/assets`、`/download` 和 `/manifest` 转发至 `http://127.0.0.1:8787`。`/admin` 使用 `.dev.vars` 中的 `APPHUB_TOKEN` 登录。

仅预览公开页示例数据时可运行 `npm run dev:demo`，访问 `/app/whatsapp`、`/app/instagram` 或 `/app/example-learning`。管理端和上传流程仍需 Worker。

## 验证和构建

```bash
npm test
npm run typecheck
npm run build
```

生产构建输出到 `web/dist`，由项目根目录的 Cloudflare Worker Static Assets 托管。页面与 API 使用同一域名，无需设置 `VITE_API_BASE_URL`。生产环境不启用示例数据。
