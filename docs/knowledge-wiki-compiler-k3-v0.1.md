# Knowledge Wiki Compiler（K3）设计 v0.1

**状态**：DESIGN（未实现）
**前置**：K1（source/revision/document）、K2（catalog/chunk/link/hybrid 检索）均已实现
**上层背景**：`skill-knowledge-architecture-v0.1.md` §13–§15 与其中的 K3 里程碑清单

## 1. 要解决的问题

K2 之后每次查询都从原始 chunk 重新拼答案，什么都不积累。同一个需要综合五篇文档的问题，
问十次就要重新拼十次；文档之间的矛盾从来没有被记下来；某个概念被反复提及却没有一个
地方定义它。

K3 引入编译出来的持久 wiki：raw source 进来时就读一遍、抽取要点、并入既有页面、
更新交叉引用、标注与旧结论的冲突。知识编译一次然后保持更新，而不是每次查询重新推导。

三层模型中的第二层（wiki）在 K3 落地：

| 层 | 拥有者 | 存放位置 | 状态 |
|---|---|---|---|
| raw sources | 人 | Git 仓库，immutable | K1 已实现 |
| wiki | 平台侧 LLM 编译 | AgentMate 内部 | **K3，本文档** |
| schema | 人与平台共同演进 | `KNOWLEDGE.yaml` 的 `profile` | K3 补齐 profile 语义 |

## 2. 已定的两个决策

### 2.1 平台侧编译，不是 agent 侧回写

**决策**：wiki 由服务端编译，产物存在 AgentMate 内部，不提交回 Git。

理由：agent 运行在客户端，行为不可控。让它写事实源等于把数据质量外包给一个无法约束的
进程——版本、prompt、模型都可能不同，产物无法审计也无法复现追溯。

代价与后续：Git 不再是 wiki 层的事实源（raw 层仍然是）。因此**必须**把出处建模到位，
否则 wiki 就成了一堆来源不明的文本。基于 Git 的 wiki 导出/PR 审批留在 K5。

### 2.2 目录按领域 / 主题两级

已在 `000022` 与 `internal/pkgpath` 落地，K3 直接复用：`platform/retrieval` 的 domain 是
`platform`，source name 是 `platform-retrieval`。每个二级目录是一个独立 KB package，
也就是一个独立 wiki 与一份独立 `index`。

## 3. 关键约束：wiki 是不可重现的生成物

这是 K3 与已有 Skill compiled catalog 最重要的区别，几乎所有设计取舍都源于它。

| | Skill compiled catalog | Knowledge wiki |
|---|---|---|
| 编译方式 | 离线确定性 | LLM |
| 同输入编译两次 | 结果相同 | **结果不同** |
| 可否随时丢弃重建 | 可以 | **不可以** |
| 数据定性 | 派生缓存 | **带出处的客户数据** |

推论，全部是硬性要求：

1. **不能用内容 hash 做幂等**。同一份 raw 编译两次得到不同 hash，hash 相等不代表逻辑等价，
   不相等也不代表输入变了。幂等必须建立在**输入身份**上（见 §6.2）。
2. **build 必须 immutable 且保留历史**。不能原地覆盖，否则一次质量变差的编译会永久毁掉
   之前的成果，且无从对比。
3. **必须可 diff、可导出**。运营者要能回答"这页为什么变成这样"，客户要能带走自己的数据。
4. **必须记全出处**：raw 的 `package_hash`、profile 版本、compiler 版本、模型、prompt 版本。
   缺任何一项，将来质量回归时都无法定位是哪个变量导致的。

## 4. 数据模型

```
KnowledgeSource (K1, 已有)
  ├─ 1:N KnowledgeSourceRevision (K1, immutable raw 快照)
  ├─ active_revision_id       → 当前 raw
  └─ active_build_id          → 当前 wiki      ← K3 新增

KnowledgeProfile / KnowledgeProfileVersion    ← K3 新增（声明式页面约定）

KnowledgeBuildRevision                         ← K3 新增（一次编译的 immutable 快照头）
  ├─ 1:N KnowledgePage
  │      ├─ 1:N KnowledgePageCitation  → raw document (+ heading/chunk)
  │      └─ 1:N KnowledgePageLink      → 同 build 内的 page（typed）
  └─ 1:N KnowledgeBuildEvent           → 渲染 log 的结构化来源
```

### 4.1 KnowledgeProfileVersion

`KNOWLEDGE.yaml` 里的 `profile: platform-wiki-v1` 目前只是一个字符串。K3 把它变成
带版本的声明式约定，规定：

- 允许哪些 page kind，各自的 frontmatter 必填字段；
- 页面命名与路径规则；
- 允许哪些 link 类型；
- citation 强制程度（对应已有的 `citation_policy`）；
- 单 build 的 page 数上限、单 page 长度上限；
- 编译工作流提示（哪些内容优先抽成 entity page 等）。

profile 版本化的原因和 prompt 版本化一样：它会影响输出，因此必须成为出处的一部分。

### 4.2 KnowledgeBuildRevision

| 字段 | 说明 |
|---|---|
| `account_id`, `source_id` | 账号作用域 |
| `source_revision_id` | 编译所基于的 raw 快照 |
| `raw_package_hash` | 冗余存一份，raw 侧被清理后仍可追溯 |
| `profile_version_id` | 页面约定版本 |
| `compiler_version` | 编译流程实现版本 |
| `model`, `prompt_version` | LLM 出处 |
| `parent_build_id` | 增量编译的基线，全量编译为 NULL |
| `mode` | `full` / `incremental` |
| `status` | `queued` / `running` / `succeeded` / `failed` / `cancelled` |
| `pages_written`, `pages_reused` | 增量编译的实际收益 |
| `input_tokens`, `output_tokens`, `cost_micros` | 成本核算 |
| `error` | 失败原因 |
| `started_at`, `finished_at` | 时长 |

`active_build_id` 只能指向 `succeeded` 的 build。失败的 build 保留用于诊断，不影响读路径。

### 4.3 KnowledgePage

`build_id` + `path` 唯一。`kind` 取自 profile 允许集合：`summary`（单个来源的摘要）、
`entity`、`concept`、`overview`、`synthesis`、`index`、`log`。

存 `content`、`frontmatter`（jsonb）、`content_hash`（仅用于 diff 与增量复用判等，
**不用于幂等**）、`derived_from_build_id`（若是从 parent build 直接复用）。

### 4.4 KnowledgePageLink（typed）

类型集合来自架构文档 §14.5：`references` / `contradicts` / `supersedes` /
`elaborates` / `mentions_entity`。

link 在 build 内闭合（不跨 build、不跨 package），这样一个 build 就是一张完整自洽的图，
可以整体回滚。跨库引用留到 K5。

### 4.5 KnowledgePageCitation

每条记录把 page 的一个断言锚到 raw document 的具体位置（document_id + heading path 或
chunk key）。这是整个设计的可信性基础：wiki 是 LLM 生成的，只有 citation 能让它被核查。

`citation_policy: required` 的 profile 下，缺 citation 的 page 视为编译失败，不是警告。

### 4.6 KnowledgeBuildEvent

append-only：ingest、page 创建/更新/复用、检测到矛盾、跳过、失败。它是结构化的，
`log` page 只是它的渲染视图——这样既能被 agent 读，也能被 SQL 统计。

## 5. 操作

### 5.1 Ingest（编译）

触发：raw source 同步出新 revision 后，显式调用或按 profile 自动触发。

流程：

1. **算 raw diff**：比较 parent build 的 `source_revision_id` 与新 revision 的 documents
   （path + sha256）→ added / modified / deleted。全量编译时全部视为 added。
2. **定影响面**：用 citation 反查引用了变更 document 的 page；再沿 link 扩展一跳
   （矛盾与 supersedes 关系可能因为一处改动而需要重判）。
3. **复用未受影响的 page**：直接从 parent build 复制，记 `pages_reused`。
   **这是成本控制的主要手段**，不是优化项。
4. **重编译受影响的 page**：按 profile 约定生成内容、frontmatter、citation、link。
5. **生成 index 与 log**：必须最后做，index 依赖全部 page 的最终状态。
6. **提交 build**：一个事务内写入全部 page/link/citation 并置 `succeeded`。
   要么整体可见，要么完全不可见——半个 wiki 比没有 wiki 更糟。
7. **切换 active 指针**（是否需要人工审批见 §9）。

### 5.2 Query

分两级，这正是 Karpathy 描述的"先读 index 再下钻"：

1. 检索 wiki page（已综合、已交叉引用）；
2. 沿 citation 下钻到 raw chunk 取原文证据。

检索层用**新的 namespace**（`knowledge_wiki`），保留现有 raw chunk 的 `knowledge`
namespace 不动。理由：raw chunk 是引用溯源的终点，必须继续可检索；而 wiki page 是新的
入口层。这同时修正了架构文档里记下的"加速层装在 raw 层上"的问题，且不破坏已验证的能力。

wiki page 同样走 CJK bigram lexical 投影（`000023`）。entity page 的页名是规范化术语锚点，
正是 lexical 精确匹配最有价值的地方。

### 5.3 Lint

只读，产出 findings，不改内容。`knowledge_lint_runs` + `knowledge_lint_findings`。

| 检查 | 判定 | 实现 |
|---|---|---|
| 矛盾 | `contradicts` 边，或同一命题的两处 citation 结论不相容 | 图查询 |
| 过期声明 | `supersedes` 指向的旧 page 未标注 | 图查询 |
| 孤立页面 | 零入链 | 聚合 |
| 缺失页面 | `mentions_entity` 指向不存在的 entity page | 反连接 |
| stale citation | citation 指向已被删除的 raw document | 反连接 |
| stale cascade | 受 stale page 影响的下游 page | recursive CTE |

这些全部是 PostgreSQL 能表达的查询。架构文档 §14 拒绝引入 Graph DB 的依据正是
"只有 KB lint 是真图查询，recursive CTE 足够"——K3 是这个判断的兑现，也是它的检验。

### 5.4 Query 结果回填

Karpathy 主张好答案回填进 wiki 让探索复利。多租户下不能直接照搬：查询结果只产生
**proposal**，需要人工或策略批准后才成为 page。这与 Memory → KB promotion 用同一道门。

## 6. 异步执行

### 6.1 必须异步（有实测依据）

2026-07-27 的真实验证中，11 个文档的**纯 chunk 索引**耗时约 110 秒，全部花在 embedding
往返；用 curl 触发时客户端超时取消了服务端 context，留下 pending 行。

LLM 编译比 embedding 慢一个量级以上。同步接口在这里不可用，不是性能优化问题。

MVP 形态：job 表 + 服务内 worker，`SELECT ... FOR UPDATE SKIP LOCKED` 取任务，
带租约与心跳，租约超时的 job 可被重新领取。不引入队列中间件——当前规模不需要，
而多一个中间件就多一处运维面。

### 6.2 幂等

因为不能用内容 hash（§3），幂等建立在输入身份上：

**输入身份** = `(source_revision_id, profile_version_id, compiler_version, model, prompt_version, parent_build_id)`

已存在同输入身份的 `succeeded` build 时，默认返回既有 build 而不重新编译；`force=true`
才重编译（用于人工怀疑上次质量不佳）。重复提交同一个 job 不会产生两个 build。

### 6.3 失败处理

- **部分失败**：默认整个 build 失败，不写入半成品。
- **可重入**：重试从 parent build 重新开始，已复用的 page 不需要重新生成。
- **供应商故障**：连续失败超阈值即中止整个 build 并记录，避免在故障期间空转烧钱
  （已有的 chunk 索引就是这个策略，实测有效）。

## 7. 成本控制

LLM 编译是本设计唯一的显著变动成本，必须在设计里就有闸门：

1. **增量复用**：只重编译受影响的 page（§5.1 步骤 3）。
2. **单 build 上限**：page 数、token 数，来自 profile。超限即中止并报告。
3. **账号预算**：周期性 token / 金额上限，超出后拒绝新 build 而不是静默降质。
4. **成本可见**：每个 build 记录 token 与费用，可按 source / 时间聚合。

## 8. API 草案

```
POST /api/knowledge/sources/:id/builds        触发编译（body: mode, force）→ 返回 job/build
GET  /api/knowledge/sources/:id/builds        列出 build 历史（含成本与出处）
GET  /api/knowledge/builds/:id               build 详情
GET  /api/knowledge/builds/:id/pages         页面列表（不含正文）
GET  /api/knowledge/builds/:id/pages/*path   单页正文 + citation + link
GET  /api/knowledge/builds/:id/diff?from=    与另一个 build 的页面级 diff
POST /api/knowledge/builds/:id/activate      切换 active 指针
POST /api/knowledge/sources/:id/lint         发起 lint（异步）
GET  /api/knowledge/lint/:id                 lint 结果
POST /api/knowledge/wiki/search              检索 wiki page（namespace knowledge_wiki）
```

MCP 侧对应新增 `knowledge_wiki_search`、`knowledge_page_get`、`knowledge_build_status`、
`knowledge_lint_run`。scope 沿用 `knowledge:r` / `knowledge:rw`；触发编译属于 `rw`。

## 9. 待决策（需要产品输入）

1. **build 激活是否需要人工审批**？自动激活省事，但一次质量下滑会直接影响所有查询。
   倾向：profile 可配，默认自动，敏感领域要求审批。
2. **用哪个模型编译**？质量与成本的直接权衡，且模型是出处的一部分，换模型不会自动
   重编译历史 build。
3. **编译触发时机**：同步后自动，还是显式触发？自动更符合"保持更新"，但会把成本
   与 raw 提交频率绑死。
4. **保留多少历史 build**？全部保留会持续增长；按数量或时间窗淘汰要考虑它是客户数据。
5. **query 回填 proposal 的审批人**是谁。

## 10. 验收标准

用已推送的 `claw-works/agentmate-demo-wiki`（commit `f7bc777`）验证，其中已经埋了
两个刻意的检验点：

- **孤立页面**：`platform/registry/raw/domain-layout.md` 出链入链均为 0（已实测确认）。
  lint 必须报出对应的 wiki page 为 orphan。
- **过期声明**：`product/support/raw/limitations.md` 仍称"中文查询无法使用全文通路"，
  而 `platform/retrieval/raw/cjk-lexical.md` 已描述 bigram 修复。这两处跨 package，
  因此 K3 的 lint 只应在**同 package 内**报矛盾——跨库检测属于 K5。
  可在 `product/support` 内再造一处同库矛盾来验证。

其余必须验证的行为：

- 全量编译后 `index` page 覆盖全部其他 page，`log` 可被 `grep` 式前缀解析；
- 每个 page 的 citation 都能定位到真实存在的 raw document；
- 改动一个 raw 文档后增量编译，`pages_reused` 显著大于 `pages_written`；
- 同输入身份重复触发不产生第二个 build；
- 编译中途杀掉 worker，job 租约超时后可被重新领取且不产生半成品 build；
- wiki page 的中文检索走双通路（融合分突破单通路上限 0.5）。

## 11. 实施顺序

```
K3.1  profile 版本化 + build/page/citation/link 数据模型 + 全量编译（同步，小语料）
K3.2  异步 job（租约 + 心跳 + 幂等 + 成本记账）
K3.3  增量编译（raw diff → 影响面 → 复用）
K3.4  index / log 生成
K3.5  wiki page 进检索（新 namespace）+ 两级 query
K3.6  lint
K3.7  query 回填 proposal
```

K3.1–K3.2 之后就有可用的东西；K3.5 之后 agent 才真正受益。lint 排在检索之后，
因为它的价值依赖 wiki 已经在被使用。

K4（Skill-driven discovery）依赖 K3.5 就位——discovery 要选的是 wiki build，不是 raw chunk。
