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

## 2026-07-27 领域目录改造后的真实验证

demo 仓库已改为 **领域 / 主题两级**并重推：

- `claw-works/agentmate-demo-skills`（commit `679c020`）：`knowledge-ops/grounded-answer`、`knowledge-ops/kb-lint`（新增，对应 wiki 的 lint 操作）、`release/release-notes`。
- `claw-works/agentmate-demo-wiki`（commit `f7bc777`）：`platform/registry`、`platform/retrieval`、`product/support`，各含 `KNOWLEDGE.yaml` + `raw/`。**仓库里只有 raw sources**——既然选了平台侧编译，wiki 层就是平台产物，不进 git。`claw-works/agentmate-demo-knowledgebases` 已可删除，其遗留 source 已从本地库清掉。

验证结果（本地 Docker + 真实 GitHub）：

- [x] domain 推导与 name 拼接：`platform/registry` → `domain=platform` / `name=platform-registry`，`knowledge-ops/grounded-answer` → `domain=knowledge-ops`，扁平路径 domain 为空。
- [x] catalog 分组：`domains=[{platform,2},{product,1}]`；`?domain=platform` 只返回 2 个 collection。
- [x] 检索：3 个中文长句查询与 1 个标识符查询均 top-1 精准，融合分 0.99–1.00；`domain=platform` 与 `domain=product` 各自只返回本领域命中。
- [x] exclude 隔离：`raw/drafts/**` 未进入 documents，草稿里的独特词串在 chunk 正文与 lexical 投影中均为 0 命中。
- [x] 孤立页面：`platform/registry/raw/domain-layout.md` 出链入链均为 0，可用于验证 lint。
- [x] skills 检索：`kb-lint` 与 `release-notes` 在各自查询上 top-1。
- [x] 最终状态：3 个 KB source / 15 文档 / 25 链接 / 73 chunk 全部 indexed，投影 100% 覆盖；`/knowledge` 与 `/skills` 均 200。

本轮暴露并修复的真实缺陷：**source 撞名静默覆盖**。注册按 name upsert，而 name 由 `package_path` 推导，于是扁平路径 `product-support` 与领域路径 `product/support` 推导出同一个 name，第二次注册把第一个 source 改指到了另一个仓库，使其 revision 历史横跨两个互不相关的来源。修复为在 `ON CONFLICT` 子句上加同一 package 判定，冲突时拒绝并报出占用者。

另外观察到的非缺陷现象：

- DashScope embedding 偶发超时（9 个 chunk 首轮失败），重跑索引即补齐。容错按设计生效——失败行保留 `status=failed` 且仍可被 lexical 命中。
- 索引耗时较长（11 个文档约 110 秒，全部花在 embedding 往返）。用 curl 触发时客户端超时会取消服务端 context 并留下 pending 行，需要设足够的超时或改为后台任务。**这是 K3 需要异步 job 的直接证据。**
- skills 仍有 3 行 `status=failed`，是 migration `000018` 遗留的 safe fallback 行，属预期。


## 待办：wiki 层（K3）

**详细设计已完成：`knowledge-wiki-compiler-k3-v0.1.md`**（数据模型、四个操作、异步 job、
成本闸门、API 草案、质量三层模型、演进闭环、四项待产品决策、验收标准、K3.1–K3.9 实施顺序）。

已定的质量与演进模型（2026-07-27 讨论结论）：

- **build 自动激活，不设人工审批门**。SaaS 场景下知识库属于用户而质量标准属于平台，
  让普通用户审编译质量是身份错配，结果只能是无脑通过或永久卡住。
- 质量分三层：**check**（机械不变量，确定性，唯一门禁，阻塞）→ **review**（异构模型审
  忠实性，不可重现故只标记不阻塞）→ **validation**（人类隐性行为信号，持续但有偏、滞后、
  稀疏，是长期度量而非单次门禁）。
- **异构模型降低共谋但不产生"公正"**：主流模型语料高度重叠、会在同一些地方一起犯错。
  真正的独立性来自两个非模型锚点——check 与 validation。
- **归因是闭环真正的难点**：一次追问可能源于 wiki 综合错、检索未召回、skill 做法不对或
  raw 里本无此事实，四种修法完全不同。因此 **K4 的 ResolutionRun 与 M1 的
  session/skill 关联从"审计功能"重新定位为"演进闭环的必要条件"**。
- **架构级不变量：proposal 不是 mutation**（已提升到架构文档 §0.2）。可自动进化的是
  发现问题的能力，不可自动进化的是通过的门槛——否则标准会漂移，系统慢慢接受更差的东西
  而每一步都"符合当前标准"。
- **写入边界**（架构文档 §0.1）：客户端 agent 可写 raw candidate、memory event、
  validation signal；不可写 wiki page 与 KB 事实。agent 提议，平台收编。

方向已定：**目录按领域组织（领域优先，每个领域一个 package，自带 `raw/` + `wiki/` + `log.md`），wiki 由平台侧编译**。平台侧编译的理由是 agent 运行在客户端、不可控，不能让它写事实源。

由此产生的建模约束（K3 设计文档需覆盖）：

- wiki 是**不可重现的生成物**，不是派生缓存。与 skill compiled catalog 不同（后者 offline deterministic，可随时丢弃重建），LLM 编译同一份 raw 两次结果不同。因此 `KnowledgeBuildRevision` 必须记全出处：raw `package_hash` + compiler version + model + prompt version + 输出快照；wiki 快照须 immutable、可 diff、可导出，按客户数据对待而非缓存。
- 编译是**有状态增量**：Karpathy 的 ingest 是"一个源触及 10–15 个 wiki 页"，即在已有 wiki 上增量更新，而非全量重生成。这比 skill 的无状态 compile 复杂一个量级，需要异步 job 与成本控制。
- 还需实现 `index.md`（内容目录，query 时先读它再下钻）、`log.md`（append-only 时间线）与 lint 操作（矛盾、过期声明、orphan 页、缺失 cross-reference）。

## 待议：knowledge 域的检索粒度不对称

当前 skills 索引 L0 card（命中后按需展开 L1），knowledge 直接索引 chunk 正文、K0 catalog card 不参与检索。这个不对称是分别演进的结果而非设计，有两个真实后果：skills 的召回上限被 card 措辞锁死（查不到 `SKILL.md` 正文里的内容）；knowledge 缺少库级语义路由（KB 数量增长后 agent 无法先判断该查哪个库）。

wiki 层就位后应重新评估：Karpathy 的检索对象是 wiki pages，而当前 hybrid 检索的对象是 raw sources——加速层装在了第一层上。建议等 K3 与 K4 discovery 明确"agent 按什么粒度找知识"后再定索引粒度，那才是真正的需求方。


## 2026-07-27 M1 归因锚点已实现

`memory_events.skill_version_id`（migration `000024`）+ 两个查询接口。真实链路验证通过：

- [x] 同一会话跑两个 skill：`timeline?session_id=` 返回 8 项（2 skill_log + 6 event），
      `unattributed_count=3` 如实反映手写 event 无归因。
- [x] `timeline?skill_version_id=` 收窄到单个 skill 的 2 项（1 log + 1 event）——这是
      `session_id` 单独做不到的事，也是本功能存在的理由。
- [x] 反向归因四种情况全部正确：`skill_version` / `session_only` / `event_only` / `none`。
- [x] 不存在或跨账号的 `skill_version_id` 返回 400；归因变更的重放返回 409 而非静默返回旧行。

同时修复一个既有缺陷（migration `000025`）：`skill_versions` 与 `skill_logs` 的 `account_id`
外键为 `ON DELETE SET NULL`，导致账号删除时客户内容残留，且第二个持有同名 active skill 的
账号会因 `idx_skill_versions_global_active` 唯一索引冲突而**删除失败**。已改为 `CASCADE`。
这个缺陷由 M1 的集成测试撞出来——测试跨多个临时账号复用相同 skill 名。

- [ ] 库中若已有 `account_id IS NULL` 的孤儿 skill 行需人工处置（本地为 0 行）：
      `SELECT count(*) FROM skill_versions WHERE account_id IS NULL`。这些行已无法归属，
      未自动删除。

待决策：其余三张表的 `account_id` 仍为 `SET NULL`——`api_logs`、`retrieval_queries`、
`retrieval_feedback`。它们不会阻塞账号删除（无相关唯一索引），但 `retrieval_queries.query`
存的是用户查询原文，属客户数据。若这三张表的 `SET NULL` 是为了在账号注销后保留聚合统计，
则需明确该保留是否可接受；否则应一并改为 `CASCADE`。


## 2026-07-27 M2 Context Pack 已实现

`internal/contextpack`，`POST /api/context/pack` 与 MCP `context_pack`。五层齐备，真实链路验证通过：

- [x] 完整 pack：SKILL 1364 字 / KNOWLEDGE 5 条带精确 citation / MEMORY 5 条 / TASK goal，
      总计 2712 字（预算 12000），`render=true` 输出的五个标签齐全且 citation 保留。
- [x] 紧预算（800 字）：SKILL 截断并标记，KNOWLEDGE 丢弃 4 条，无任何层超预算，总量不超限。
- [x] 层选择：`layers=[KNOWLEDGE,TASK]` 时配额合计 10500+1500=12000，未选层的份额被让出。
- [x] `knowledge_domain` 收窄正确传递：`platform` 只回 platform-registry，`product` 只回 product-support。
- [x] 指定 skill 生效；不存在的 skill 名返回 warning 而非静默换一个 skill。
- [x] **按层授权**：只有 `memory:r`+`skills:r` 的受限 key 得到 SKILL/MEMORY/TASK，
      KNOWLEDGE 与 FACTS 返回空并附 `insufficient scope` note，调用仍 200。
- [x] MCP 端到端通过（initialize → tools/list → tools/call）。
- [x] 输入校验：空 task、`max_chars` 越界、未知层、`top_k` 过大均 400。

验证中修掉一个真实问题：空层的 `items` 序列化为 `null` 而非 `[]`，会迫使每个客户端处理两种
"无内容"形态。已加回归测试锁住。

观察到但不属于 M2 的问题：skill 检索选择质量有限。task 写"修复中文检索…需要知道 bigram 投影的
设计取舍"时选中的是 `kb-lint` 而非更合适的 `grounded-answer`。原因是 skill 检索只命中 L0 card
（正文不进索引，见架构文档的粒度不对称待议项），card 措辞决定了召回上限。K4 的 discovery
contract 会正面解决选择问题。

- [ ] Memory entry 多数没有 title，pack 的 MEMORY 层条目只显示 memory_type。是否要求
      `memory_store` 生成或强制 title 需产品决策。
