# Skill Registry 后续操作清单

## 本地 rollout

- [ ] 登录本地 AgentMate，打开 `/skills`；所有 compile、index 和 Quality 操作都只作用于当前登录账号。
- [x] 2026-07-22 核对当前账号：2 个 active Skill（`agentmate`、`skill-evolver`），均为 direct publish；`skill_compiled_catalogs` 中已有 2 个 current artifact。Compiled artifact 是 PostgreSQL 中的可重建 L0 catalog row，不要求 Skill 挂在 Git 仓库。
- [ ] 如果当前账号需要 semantic search，再执行“索引全部 active”；按当前账号的 active versions 验证 indexed documents，不使用整库其他账号的 failed/pending 数量作为预期。
- [ ] 随机选择一个 active version，打开 Quality 页签并运行确定性检查；确认报告显示 package lint、平台契约断言、release comparison 和 version-bound telemetry。
- [ ] 后续调用 `skill_log_add` 时尽量传 `skill_version_id`；历史及未绑定日志不会被猜测归因，也不会进入版本质量报告。

## 需要凭据或产品决策后再推进

- [ ] 若启用自动 GitHub PR / GitLab MR，确定 token/OAuth 的托管方式、最小权限、目标仓库/分支规则和人工审批人。不要把 token 写入仓库、聊天记录或普通日志。
- [ ] 决定 release promotion policy：哪些 blocker/error 阻止 promotion、谁可批准例外、批准记录保存在哪里。本阶段不会自动发布、激活或索引。
- [ ] 决定是否支持 private repositories、自建 GitLab 和 provider webhook；当前远程同步仍只支持公开 GitHub/GitLab HTTPS source。
- [ ] 决定是否为前端引入测试框架，以覆盖运行 Quality POST 后切换页签/版本的 abort 与 dirty-refetch 时序。
- [ ] 可选：将前端 TypeScript 从 5.0.2 升级到 Next.js 推荐的 >=5.1，并单独验证锁文件和生产构建。


## 2026-07-27 真实环境验证结论

- [x] 用 `claw-works/agentmate-demo-skills` 与 `claw-works/agentmate-demo-knowledgebases` 跑通真实 Git sync → compile → index → 检索 → memory 全链路。真实链路暴露 4 个测试全绿但线上失效的问题，修了 3 个：GitHub tarball 拒绝 `Accept: application/octet-stream`（改 `*/*`）、tarball 首条目 `pax_global_header` 被当成 archive root、chunker 产出仅含标题行的空壳 chunk。
- [x] 第 4 个问题（中文 lexical 命中恒为 0）已在 migration `000023` 修复：bigram 投影列 + 双侧共用的 Go 投影规则。真实验证：重建前中文 lexical 命中 0 行，重建 64 行后中文查询 top-1 精准且 hybrid 融合分从恒定 0.5 升到 0.92–1.00，即两条通路都参与融合。skills 中仍有分数 0.4919 的条目，那是 migration `000018` 遗留的 `status=failed` 行走单通路，属预期。
- [x] Domain 建模（migration `000022`）：领域从 `package_path` 首段推导，修复了 basename 推名导致不同领域下同名 package 静默覆盖的缺陷。
- [ ] 升级到 `000023` 后需执行一次 `POST /api/admin/retrieval/lexical/rebuild`；此前写入的行投影为空，对 lexical 不可见。该操作不重新 embedding。

## 待办：wiki 层（K3）

方向已定：**目录按领域组织（领域优先，每个领域一个 package，自带 `raw/` + `wiki/` + `log.md`），wiki 由平台侧编译**。平台侧编译的理由是 agent 运行在客户端、不可控，不能让它写事实源。

由此产生的建模约束（K3 设计文档需覆盖）：

- wiki 是**不可重现的生成物**，不是派生缓存。与 skill compiled catalog 不同（后者 offline deterministic，可随时丢弃重建），LLM 编译同一份 raw 两次结果不同。因此 `KnowledgeBuildRevision` 必须记全出处：raw `package_hash` + compiler version + model + prompt version + 输出快照；wiki 快照须 immutable、可 diff、可导出，按客户数据对待而非缓存。
- 编译是**有状态增量**：Karpathy 的 ingest 是"一个源触及 10–15 个 wiki 页"，即在已有 wiki 上增量更新，而非全量重生成。这比 skill 的无状态 compile 复杂一个量级，需要异步 job 与成本控制。
- 还需实现 `index.md`（内容目录，query 时先读它再下钻）、`log.md`（append-only 时间线）与 lint 操作（矛盾、过期声明、orphan 页、缺失 cross-reference）。

## 待议：knowledge 域的检索粒度不对称

当前 skills 索引 L0 card（命中后按需展开 L1），knowledge 直接索引 chunk 正文、K0 catalog card 不参与检索。这个不对称是分别演进的结果而非设计，有两个真实后果：skills 的召回上限被 card 措辞锁死（查不到 `SKILL.md` 正文里的内容）；knowledge 缺少库级语义路由（KB 数量增长后 agent 无法先判断该查哪个库）。

wiki 层就位后应重新评估：Karpathy 的检索对象是 wiki pages，而当前 hybrid 检索的对象是 raw sources——加速层装在了第一层上。建议等 K3 与 K4 discovery 明确"agent 按什么粒度找知识"后再定索引粒度，那才是真正的需求方。
