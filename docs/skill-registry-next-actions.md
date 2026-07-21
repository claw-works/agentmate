# Skill Registry 后续操作清单

## 本地 rollout

- [ ] 登录本地 AgentMate，打开 `/skills`。
- [ ] 执行“编译全部 active”，确认当前 9 个 active versions 都生成 compiled artifact；预期 artifact 数由 0 变为 9。
- [ ] 执行“索引全部 active”，确认待重建的 3 个安全 lexical fallback 文档完成重索引，pending reindex 由 3 变为 0。
- [ ] 随机选择一个 active version，打开 Quality 页签并运行确定性检查；确认报告显示 package lint、平台契约断言、release comparison 和 version-bound telemetry。
- [ ] 后续调用 `skill_log_add` 时尽量传 `skill_version_id`；历史及未绑定日志不会被猜测归因，也不会进入版本质量报告。

## 需要凭据或产品决策后再推进

- [ ] 若启用自动 GitHub PR / GitLab MR，确定 token/OAuth 的托管方式、最小权限、目标仓库/分支规则和人工审批人。不要把 token 写入仓库、聊天记录或普通日志。
- [ ] 决定 release promotion policy：哪些 blocker/error 阻止 promotion、谁可批准例外、批准记录保存在哪里。本阶段不会自动发布、激活或索引。
- [ ] 决定是否支持 private repositories、自建 GitLab 和 provider webhook；当前远程同步仍只支持公开 GitHub/GitLab HTTPS source。
- [ ] 决定是否为前端引入测试框架，以覆盖运行 Quality POST 后切换页签/版本的 abort 与 dirty-refetch 时序。
- [ ] 可选：将前端 TypeScript 从 5.0.2 升级到 Next.js 推荐的 >=5.1，并单独验证锁文件和生产构建。
