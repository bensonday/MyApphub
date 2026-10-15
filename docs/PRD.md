# MyApphub 产品需求 v2

## 定位与架构

MyApphub 用于通过固定链接私有分发 IPA/APK。开发者在本机运行 Go CLI 解析、验证和上传安装包；管理员在 `/admin` 管理应用、版本、发布状态和分享密码；安装用户只访问公开安装页与 `/udid`。线上由 TypeScript Cloudflare Worker 提供全部 API，D1 保存元数据，私有 R2 保存安装包和图标。公开页的视觉与布局以 [WEB_UI_SPEC.md](WEB_UI_SPEC.md) 为定稿基线。

## 功能范围

- CLI 校验 ZIP 安全性、IPA/APK 签名及元数据，计算 SHA-256，以分块方式上传至 R2。iOS 接受 Enterprise、Ad Hoc 和包含已注册设备的 Development 包。
- 同一 slug 支持 iOS 和 Android 两个平台；每个平台有独立当前版本与版本历史。
- 管理端支持登录、版本发布及回滚、七天回收站、分享密码和分享地址。上传使用 CLI。
- 公开页按设备优先选择平台，提供 Android APK 下载或 iOS OTA Manifest 安装。
- 可选的应用级分享密码保护安装链接；修改密码立即撤销旧链接。
- UDID 会话在 30 分钟后过期。Worker 为每次会话生成并签署 `.mobileconfig`，验证设备签名回调，并在复制后删除结果。
- D1 和 R2 的过期上传、UDID 会话及七天以上回收站内容定时清理。

## 数据与安全

- D1 表：`apps`、`variants`、`versions`、`uploads`、`udid_sessions`、`rate_limits`。
- R2 对象键不可变；桶不公开。安装和 Manifest 令牌按用途、应用、平台、版本和密码版本签名。
- 管理端使用至少 32 字节管理令牌登录，浏览器只持有短期 HttpOnly、SameSite 会话 Cookie。CLI 使用同一令牌访问上传 API。
- 各环境分别配置管理令牌、HMAC 密钥和可选 UDID 证书；密钥不得写入源码、日志或公开 API。
- 当前仓库旧 `server/data/` 是测试数据，v2 不迁移。

## 验收

1. 公开安装页与 UDID 页在桌面、手机上的视觉与定稿版本一致。
2. CLI 上传有效 Enterprise、Ad Hoc、Development IPA 和 APK；损坏或不受支持的包在本机拒绝。Development 包提供 IPA 下载入口供 Xcode 或 Apple Configurator 安装。
3. 管理端可发布、回滚、删除、恢复和设置密码；无授权请求被拒绝。
4. 受密码保护的应用在验证前不暴露更新说明和安装地址；旧令牌在密码变更后立即失效。
5. APK 与 IPA 支持受控下载和 Range；真实 iPhone 完成 OTA 安装。
6. 真实 iPhone Safari 完成 UDID 描述文件安装、签名回调、复制和删除。
7. 本地测试、类型检查、Worker 打包、D1 迁移和部署后冒烟检查通过。
