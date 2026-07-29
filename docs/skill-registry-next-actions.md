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

**K3.1 / K3.2 / K3.3 / K3.5 已实现并在真实环境验证（2026-07-29）**，详见
`knowledge-wiki-compiler-k3-v0.1.md` §13–§14。剩余 K3.4、K3.6–K3.9 仍为设计。
（K3.5 index/log 生成随 K3.2 一起落地——check 的 index 覆盖规则要求 index 由平台生成，
所以它是 check 的前置条件而不是后续步骤，原实施顺序排错了。）

**详细设计：`knowledge-wiki-compiler-k3-v0.1.md`**（数据模型、四个操作、异步 job、
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

## 2026-07-27 Memory 生命周期补齐（supersede / feedback / checkpoint）

三项此前只有数据模型没有行为，现已实现（migration `000026` 新增 `memory_feedback` 表；
supersede 与 checkpoint 无需新表）。真实链路验证通过：

- [x] supersede：`status=superseded`、`valid_to` 关闭、`projection_removed=1`；被取代条目不再出现在检索结果。
- [x] 幂等重放 200；换替代者 409；成环 409；自我取代 400。
- [x] feedback：计数器随信号移动；同会话重放 `created=false` 且计数不变（仍返回当前计数）；
      非法 signal 400；信号日志 3 条。
- [x] feedback 影响排序：`retrieval=0.9919 → score=1.0000, adj=+0.0250`，两者分开返回。
- [x] checkpoint：`resolution` 三态正确（empty / journal_only / checkpoint）；
      同内容重存 `created=false`；resume 正确返回 checkpoint 之后的 1 条活动，
      且 checkpoint 之前的 goal 事件未混入。
- [x] Context Pack 的 TASK 层已改为优先 checkpoint，note 变为
      `resumed from checkpoint saved at ...`，并附 `since_checkpoint` 条目。

验证中修掉一个真实问题：supersede 的冲突与成环原本返回 500（repo 返回裸 `fmt.Errorf`，
落到 handler 的 default 分支）。已加 `ErrSupersedeConflict` 哨兵错误，两者改为 409。
顺带让 feedback 幂等重放也返回当前 entry —— 否则调用方拿不到当前计数，看起来像没有计数。

- [ ] 仍待决策：memory entry 多数无 title，Context Pack 的 MEMORY 层条目只显示 memory_type。
- [ ] 仍待决策：`api_logs`、`retrieval_queries`、`retrieval_feedback` 三表的 `account_id`
      仍为 `SET NULL`（详见上一节）。


## 2026-07-29 K3.1 + K3.2 wiki 编译器已实现

migration `000027`；新包 `internal/llm`；`internal/knowledge/wiki_{model,compile,check,service,repo,handler,mcp}.go`；
9 个集成测试用脚本化模型驱动（不依赖真实模型可达，因为真实模型两次不会说同样的话）。
REST 8 个端点、MCP 8 个工具。

真实环境（本地 Docker + DashScope `qwen3.7-plus` + `claw-works/agentmate-demo-wiki`）验证通过：

- [x] `platform/registry` 编译出 14 page（12 内容 + index + log），`product/support` 12 page；
      两者 check 全过、自动激活、`active_build_id` 随之移动。
- [x] index 覆盖全部内容页（12/12、10/10）；log `grep '^## \['` 命中数 = event 数。
- [x] citation 全部 `document_id` 非空（0 条悬空）；link 全部闭合、出入双向可读；
      `references` / `mentions_entity` / `elaborates` 均被真实使用。
- [x] 幂等：同输入身份二次触发返回同一 build 且不调模型；`force` 才产生新 build。
- [x] diff：同一份 raw 两次编译得到 3 added / 4 removed / 9 changed / 1 unchanged
      —— 这是"wiki 不可重现、必须 immutable 且可 diff"的最直接实测证据。
- [x] 回滚：激活旧 build 后读路径立即回到旧内容；`previous_build_id` 让回滚可撤销。
- [x] 负向：未授权 401、不存在 build 404、非法 uuid 404、缺页 404、路径穿越（含编码）404、
      SQL 注入式 page path 404、激活 cancelled build 409、未配置模型 501 且不留 build。
- [x] `same_provider` 警告出现在每次 build 的 warnings 中。
- [x] 设计预期的负向结果：`product/support` 的过期声明**没有**产出 `contradicts` link，
      因为矛盾的另一端在 `platform/retrieval`（另一个 package）。K3 按 package 编译，
      跨库矛盾属 K5。§11 埋这个检验点就是为了确认边界。

本轮暴露并修掉四个真实缺陷（细节见 K3 文档 §13.3）：

1. 输出预算按"推理模型先花掉多少"估而非按 wiki 体积估：4096 与 16384 都截断，最终 32768。
2. 客户端断开留下永久 `running` 的 build：终态写入改用 `context.WithoutCancel` 并记 `cancelled`。
3. 模型自己写 `wiki/index.md` 撞名导致整个 build 被 `path_unique` 判死：改为保留路径 + 丢弃
   + 记 `page_rejected` event，`CompilerVersion`/`PromptVersion` 一并升版。
4. 模型页全被丢弃时会产出空 wiki：改为报 `no usable pages` 并 failed。

- [x] ~~未修：provider 偶发 `unexpected EOF`，客户端无重试~~ —— K3.3 已解决，传输失败进入有界重试。
- [x] ~~未修：同步编译 200–400 秒超出客户端超时~~ —— K3.3 已解决，改为 202 + 轮询。
- [ ] K3 剩余待产品决策（四项，见 K3 文档 §10）：编译与 review 各用哪个模型、编译触发时机、
      保留多少历史 build、proposal 的处置人。


## 2026-07-29 K3.3 带租约的异步编译已实现

migration `000028`；`internal/knowledge/wiki_worker.go`；`internal/llm` 加重试分类与单价。
细节与决策见 `knowledge-wiki-compiler-k3-v0.1.md` §14。

一句话：**编译从请求里搬到带租约的队列里**，上一轮记录的两个未修问题都归这里解决。

真实环境验证通过：

- [x] `POST /compile` **49 毫秒**返回 202（原 200–400 秒），队列深度随 receipt 返回。
- [x] 心跳每 15 秒推进；租约 30 分钟由 compile timeout 推导（不独立配置——租约短于编译
      会让健康 worker 的 build 被抢走，这两个值没有各自变化的自由）。
- [x] 并发 2 生效；`queued_at`/`started_at`/`finished_at` 让排队等待（2 秒）与编译耗时
      （140 秒）可分离。
- [x] **优雅重启**：`docker compose restart server`，两个在途 build 被 yield 并退还 attempt，
      新 worker 数秒内重新领取，最终 attempt 仍为 1、产出 20 页与 16 页、**page 路径零重复**。
      worker 身份带 nonce 这件事在这里得到验证：容器 PID 仍是 1，只有 nonce 区分新旧进程，
      否则新进程会继承自己的过期租约、回收机制直接失效。
- [x] migration 000028 把同步时代遗留的 2 个永久 `running` 行关成 `cancelled`，
      否则 worker 一上线就去领永远完不成的任务。
- [x] 三个 demo KB 现在都有 active wiki。

关键决策（都是"拒绝某个更省事的做法"）：

- **租约放 build 行，不建 job 表**：两张表会在崩溃后对"是否在跑"给出矛盾答案。
- **回收看租约到期，不看 worker 存活**：被分区的 worker 和崩溃的 worker 从数据库看一样。
- **attempt 记领取次数而非失败次数**：静默弄死 worker 的 build 不会自己报失败，
  否则无限重试。代价用"优雅关停退还 attempt"补偿，崩溃到不了那条路径。
- **check 失败不重试**：编译不可重现，重试确实可能"这次就过了"——那正是靠重复摇骰子
  通过门禁，等于用重试放松标准，与架构不变量冲突。这是政策而非优化。
- **截断不重试**：同预算重试还是截断，只是账单翻倍。
- **提交与成功状态合并进一个事务**：原先两步，worker 在中间被杀会留下"有页但仍 running"，
  一旦有回收就变成重复页面的来源。
- **提交受租约保护**：易主后的 worker 丢弃产物而不是抢提交；心跳发现丢租约会取消编译，
  否则它还在为无权提交的产物付费。
- **不保留同步路径**：两条路径两套行为，而其中一条已知不可用。
- **单价默认 0**：编默认单价会在账上放一个看起来权威的错数字。

本轮暴露一个真实缺陷：**Go duration 字符串当 Postgres interval 用**。
`NOW() + $n::interval` 传 `duration.String()`，Postgres 拒绝 `"1ns"`，而 `"1m0s"`
能解析纯属缩写巧合。后果是亚秒退避的 requeue 静默失败、build 永远持租约不到终态。
改为 `make_interval(secs => $n)`。生产默认值（30 秒）恰好能工作，所以只有集成测试能抓到。

- [ ] 仍未做（记录，不装作已完成）：成本**闸门**（只有记账没有预算上限，profile 的
      `max_build_tokens` 是事后 check 不是事前拦截）；provider 侧幂等（重试会重新完整调用，
      若上次其实已生成完只是返回途中断连，就是重复付费）；队列优先级/公平性
      （单一 FIFO，一个账号排 50 个会让其他账号等在后面）。
