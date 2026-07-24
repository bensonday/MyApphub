# 为 MyApphub 贡献代码

感谢你参与 MyApphub。提交改动前，请先搜索已有 Issue，避免重复工作。较大的功能或协议变更应先通过 Issue 说明使用场景和兼容性影响。

## 开发约定

- 保留 `server/`、`cli/`、`web/` 三个项目的独立构建边界。
- 服务端只使用 Go 标准库；CLI 可以使用 Cobra；Web 只通过公开 HTTP API 访问服务端。
- 不提交安装包、构建产物、运行数据、证书、密钥或真实 Token。
- 新功能应包含对应测试，并同步更新公开 API 或配置文档。
- 不破坏已有 `apphub` 命令、`APPHUB_*` 环境变量和 `/api` 路径，除非变更经过明确讨论。

## 提交前检查

```bash
(cd server && make test && make test-race && make vet && make check-deps)
(cd cli && make test && make test-race && make vet)
(cd web && npm ci && npm test && npm run typecheck && npm run build)
```

Pull Request 应说明变更目的、用户可见影响、测试结果，以及是否涉及配置、数据格式或兼容性。
