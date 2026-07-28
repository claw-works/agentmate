# AgentMate Skill + Knowledge Architecture v0.4

**日期**：2026-07-23（v0.1 初稿）；2026-07-23（v0.2 增补 Memory Plane）；2026-07-24（v0.3 增补 Graph 模型与 GraphRAG 评估）；2026-07-27（v0.4 增补写入边界与演进不变量）  
**状态**：IN PROGRESS（K1 已实现；K2–K5 与 Memory M1–M3 为后续实现依据）  
**范围**：Skill Registry、未来 Knowledge Registry、Knowledge Compiler、运行时 Knowledge Resolution、Memory Plane 集成、Graph 模型。

## 0. 决策摘要

AgentMate 将 Skill 与 Knowledge 建模为两个独立业务域：

- **Skill** 回答“何时以及如何行动”，保存 routing、instructions、constraints、tools、execution assets 与 evals。
- **KnowledgeBase（KB）** 回答“有哪些可引用的事实与综合知识”，保存 immutable raw sources、持久化 wiki builds、pages、links 与 citations。
- Skill 不固定绑定具体 KB。Skill package 声明结构化 **Knowledge Discovery Contract**，指导 Agent 在运行时发现当前 account/workspace 可访问的 KB。
- 初始模型不创建预配置 `BindingRevision`。每次执行把实际选中的 KB build、page、chunk 和 citation 固化为 **KnowledgeResolutionRun**，用于权限审计、复现、评测，以及作为演进闭环的归因锚点（见 §0.2）。
- Git/repository directory 是第一版 Skill/KB source provider 和 package boundary，不是 SaaS 控制面 identity。PostgreSQL 保存 registry 事实；Qdrant、全文索引和导航卡片是可重建派生物。

最小可复现执行单元是：

```text
SkillVersion
+ KnowledgeResolutionRun
+ selected KnowledgeBuildRevision(s)
```

而不是：

```text
one Skill <-> one dedicated KB
```

### 0.1 写入边界

客户端 agent **可以**写 raw candidate、memory event 与 validation signal；**不可以**写
wiki page 与 KB 事实。agent 提议，平台收编。

理由：agent 运行在客户端，版本、prompt、模型都不可控。让它写事实源等于把数据质量外包
给一个无法约束的进程。wiki 因此由平台侧编译，见 `knowledge-wiki-compiler-k3-v0.1.md` §2.1。

### 0.2 演进不变量：proposal，不是 mutation

自动化产生的信号与判断只驱动 **proposal**，不直接修改事实源。这条不变量在本架构中出现
四次，形态不同但原则相同：

| 场景 | 约束 |
|---|---|
| Memory → KB 晋升 | 必须过使用信号门槛 + 审批，禁止自动写入 |
| Query 结果回填 wiki | 只产生 proposal（K3 §5.4） |
| Validation 信号驱动改进 | 只产生 proposal，不改写既有 page（K3 §7.5） |
| Skill/KB 质量门槛 | 可以自动进化**发现问题的能力**，不可自动放松**通过的门槛** |

最后一条最容易被忽略：如果审批器能自行调整标准，就会出现标准漂移——系统慢慢接受更差
的东西，而每一步都"符合当前标准"。因此 check 阈值与 review 标准都必须显式版本化，
可人工演进，不可由系统自行放松。

质量分三层——check（确定性、阻塞）、review（异构模型、只标记）、validation（人类隐性
行为、持续），详见 K3 设计文档 §7。人工审批**不在**其中：SaaS 场景下知识库属于用户而
质量标准属于平台，让普通用户审编译质量是身份错配。

## 1. 为什么必须分域

Skill 和 Knowledge 都可能表现为 Markdown，但生命周期不同：

| 维度 | Skill | KnowledgeBase |
|---|---|---|
| 核心问题 | 如何做、何时做 | 什么是真的、证据在哪里 |
| 选择方式 | intent/capability/trigger routing | entity/topic/time/authority retrieval |
| 更新原因 | 行为、流程、工具或约束变化 | source、事实、综合结论或时效变化 |
| 评价方式 | routing、task outcome、tool correctness、safety | coverage、freshness、citation、contradiction、retrieval usefulness |
| 副作用 | 可以调用工具执行动作 | 默认只提供 evidence；写入经过 proposal/promotion |
| identity | immutable Skill package | immutable source revision + immutable knowledge build |

判断内容归属时使用：

> 删除该内容后，Agent 是“不知道怎么做”，还是“不知道相关事实”？

- 不知道怎么做：Skill 或 Skill Asset。
- 不知道事实：Knowledge。

边界示例：

- “生产删除必须人工确认”是 Skill constraint；“哪些账号是生产账号”是 Knowledge。
- 通用故障处理流程是 Skill；当前拓扑、owner 和最近告警是 Knowledge。
- 固定输出模板、schema、脚本和定义行为的 few-shot 是 Skill Asset；大规模案例、产品文档、政策和 API reference 是 Knowledge。

## 2. Karpathy LLM Wiki 的 SaaS 化映射

Karpathy 的 LLM Wiki 包含三层：raw sources、LLM-maintained wiki、schema/agent instructions。个人 repo 可以把 schema 和工作流放在同一个 `AGENTS.md`，SaaS 必须拆开：

| LLM Wiki | AgentMate SaaS |
|---|---|
| Raw sources | `KnowledgeSourceRevision`：immutable、只读事实来源 |
| LLM-maintained wiki | `KnowledgeBuildRevision`：持久化 pages/links/citations 的编译快照 |
| Schema 的页面约定 | `KnowledgeProfileVersion`：声明式 page/link/frontmatter/citation 规则 |
| Schema 的 ingest/query/lint 工作流 | 可版本化通用或领域 Skill |
| `index.md` | K0 collection cards + K1 page catalog/search projection |
| `log.md` | append-only knowledge events/runs |
| 本地 Markdown 搜索 | account-scoped lexical + semantic hybrid retrieval |

Raw source 是权威输入。Knowledge build 是重要的持久化综合产物，但不能伪装成未经推断的原始事实；每个 claim/page 必须保留 citation 和 build provenance。

LLM 输出不是确定性编译产物。同一 source revision 可生成多个 candidate build；每个 build 必须记录：

- 输入 source revisions；
- compiler/profile/model/prompt 版本；
- page 与 link hashes；
- citations；
- lint/contradiction 结果；
- reviewer、promotion 和 active 状态。

查询产生的新综合不能直接写回 active wiki。它先成为 candidate page/proposal，经 citation、lint 和审批后再 promotion，避免错误答案反馈污染知识库。

## 3. 最小领域单元

### 3.1 KnowledgeBase

一个文件只是 Knowledge Document，不是 KB。最小 KB 是一个有独立 identity、ACL、source、revision、profile 和 active build 的 Knowledge Space。

第一版以 `repository_url + package_path` 作为 source boundary：

```text
knowledge-repo/
  product-support/
    KNOWLEDGE.yaml
    raw/
      product-guide.md
      faq.md
      troubleshooting.md
```

最小 manifest：

```yaml
name: product-support
description: 产品文档、FAQ 和故障排查知识
profile: linked-wiki-v1
language: zh-CN
include:
  - "raw/**/*.md"
exclude:
  - "raw/drafts/**"
citation_policy: required
```

同一 repo 可以包含多个 KB directory；每个 directory 注册为独立 `KnowledgeSource`。repo/path 可以迁移，不能作为永久主键；控制面使用稳定 UUID 和 account-scoped logical name。

### 3.2 Skill

最小 Skill 是独立行为契约：

```text
SkillVersion
  L0 routing metadata
  L1 instructions
  tool/permission requirements
  constraints
  input/output contract
  execution assets
  evals
  optional Knowledge Discovery Contract
```

Skill 可以完全不依赖 KB。需要 Knowledge 的 Skill 只声明抽象 requirement/slot，不写死具体 KB ID。

### 3.3 KnowledgeResolutionRun

运行时 discovery 的结果必须固化，但不需要预配置 Binding：

```text
KnowledgeResolutionRun
  account/workspace scope
  skill_version_id
  requirement_id
  discovery request/fingerprint
  catalog/policy version
  candidate summaries
  selected knowledge_base_id/build_revision_id
  retrieved page/chunk IDs
  citations
  selection reason/confidence
  created_at
```

它是 execution evidence，不是可变绑定配置。

## 4. Skill 内的 Knowledge Discovery Contract

只写“自行寻找相关知识”无法 lint、授权、预算或评测。Skill 应同时包含结构化 contract 和自然语言说明：

```yaml
knowledge:
  mode: discover
  requirements:
    - id: primary-domain
      purpose: 找到与用户问题直接相关的领域知识
      required: true
      match:
        capabilities:
          - factual-reference
          - entity-lookup
        languages:
          - zh-CN
          - en-US
      retrieval:
        max_knowledge_bases: 3
        top_k_per_base: 8
        freshness: active
        citations: required
      fallback:
        on_no_match: ask_user
        on_ambiguous: search_multiple
```

Contract 属于 Skill package identity；改变 discovery 语义会产生新 Skill version。它描述“需要什么以及如何发现”，不描述“必须使用哪个具体 KB”。

后续可支持三种模式，但默认从 `discover` 开始：

- `discover`：在授权范围内动态选择。
- `scoped_discover`：workspace/tags/approved state 限定范围后动态选择。
- `pinned`：少数合规或严格复现场景固定 KB/build；不是初始模型的前提。

Skill instructions 不能扩大权限。account/workspace ACL、候选上限、成本预算、active build 和 write permission 始终由平台在 discovery/search API 中强制执行。

## 5. 组合基数

Skill 与 KB 是多对多，不是一对一：

```text
SkillVersion N -- runtime discovery --> M KnowledgeBase/BuildRevision
```

一个通用 `grounded-answer` Skill 可发现多个不同领域 KB；同一个产品 KB 也可被 answer、compare、incident-analysis、report-generation 和 knowledge-gap-analysis 等多个 Skill 使用。

每个 KB 不需要专属 Skill。平台提供可复用的 Knowledge lifecycle Skills：

- knowledge-ingest；
- knowledge-compile；
- knowledge-query；
- knowledge-lint；
- knowledge-reconcile；
- knowledge-gap-analysis。

每个 KB 需要声明式 `KnowledgeProfileVersion`。只有医疗、法律、金融、代码等领域存在不同的证据、时效、关系或编译工作流时，才增加可复用的领域 Skill，而不是为每个 KB 复制一个 Skill。

## 6. 两套渐进式披露轴

Skill 和 Knowledge 都使用 progressive disclosure，但优化目标不同，必须使用不同命名：

```text
Skill
  S0 Skill Card
  S1 Core Instructions
  S2 Selected Skill Assets
  S3 Complete Package / Provenance

Knowledge
  K0 KnowledgeBase / Collection Card
  K1 Page / Section Card
  K2 Selected Evidence + Citation
  K3 Raw Source / Full Build Provenance
```

现有 Skill REST 中的 L0/L1/L2 名称保持兼容，但语义收窄：Skill resource manifest 表示 scripts、templates、schemas、tests 和 behavior examples 等 execution assets，不再把通用领域文档称为 Skill knowledge。

未来 Knowledge discovery 先返回 bounded K0 cards；Agent 根据 Skill contract 选择一个或多个 KB，再查询 K1/K2。普通执行不加载完整 K3。

## 7. 运行时流程

```text
1. 用户请求
2. Skill search 返回 S0 cards
3. Agent 选择 SkillVersion，加载 S1
4. 读取 Knowledge Discovery Contract
5. knowledge_discover 在 account/workspace ACL 内返回 K0 candidates
6. Agent 选择一个或多个 active KnowledgeBuildRevision
7. knowledge_search 返回 K1/K2 evidence 和 citations
8. Agent 执行 Skill；需要时追加 discovery/search
9. 保存 KnowledgeResolutionRun 与 Skill execution outcome
```

发现失败必须分类：

- 没有授权 KB；
- 没有 metadata 匹配的 KB；
- KB 匹配但 evidence 不足；
- 多个候选歧义；
- active build 过期或失败；
- budget 用尽。

这些状态不能统一伪装成“没有答案”。

## 8. SaaS 控制面与数据面

### 8.1 建议领域模型

```text
KnowledgeSource
  1:N KnowledgeSourceRevision

KnowledgeBase
  1:N KnowledgeBuildRevision
  1:N KnowledgePage
  1:N KnowledgeEvent/CompileRun/LintRun

KnowledgeProfile
  1:N KnowledgeProfileVersion

KnowledgeResolutionRun
  N:1 SkillVersion
  N:M selected KnowledgeBuildRevision/Page/Chunk
```

初期不引入显式 `BindingRevision`。若未来保存可复用 Agent/Profile/Deployment，它可以提供 optional discovery scope/default preference，但 runtime 仍记录最终 resolution。

### 8.2 事实与派生物

- Git/local snapshot 保存 immutable raw source package。
- PostgreSQL 保存 source/build/profile/page/link/citation/resolution 的 account-scoped 事实和状态。
- 大文件内容未来可放 object storage，PostgreSQL 保存 hash、locator 和 provenance。
- Qdrant、FTS projection、K0/K1 catalog、chunk embedding 是可重建派生索引。
- Active source/build/profile 是可变指针，不覆盖 immutable revision。

### 8.3 安全边界

- 所有 source/base/build/page/chunk/resolution 查询必须包含 account scope；workspace 只能缩小范围。
- Knowledge content 是不可信输入；页面或 source 中的指令不能改变 Skill、系统 policy 或工具授权。
- Agent 只能通过结构化 tool result 获得 source/page content，响应携带 provenance 和 content classification。
- discovery metadata 与 evidence body 均有大小、数量和 token budget。
- write/compile/promotion 与 read/search 使用不同 scope；默认 query Skill 只有 read 权限。
- cross-tenant catalog、embedding hydration 和 citations 必须在数据库回表时再次验证 ownership。

## 9. Git package 与 identity

第一版复用 Skill Git provider 的安全能力：public GitHub/GitLab HTTPS parsing、immutable commit resolution、bounded archive extraction、path/traversal/link/entry/byte limits。SkillSource 与 KnowledgeSource 是独立业务记录，即使指向同一 repo/commit。

Knowledge source package hash 对 manifest 及 manifest 选中的 raw source 文件的 canonical path/hash/size 计算；未被选中的文件不参与 identity。Knowledge build identity 单独包含：

- input source revision IDs/hashes；
- profile version；
- compiler/model/prompt version；
- canonical output page/link/citation manifests。

Knowledge 文件变化只产生 Knowledge source/build revision，不产生 Skill version；只有 Skill instructions、assets 或 discovery contract 改变才产生 Skill package identity。

## 10. API/MCP 方向（未实现）

建议独立模块和 endpoint，不把 Knowledge tools 塞进 Skills MCP：

```text
REST
  POST /api/knowledge/sources
  POST /api/knowledge/sources/:id/sync
  GET  /api/knowledge/bases
  GET  /api/knowledge/bases/:id
  POST /api/knowledge/discover
  POST /api/knowledge/search
  GET  /api/knowledge/pages/:id
  GET  /api/knowledge/evidence/:id
  GET  /api/knowledge/resolutions/:id

MCP /mcp/knowledge
  knowledge_catalog_list
  knowledge_discover
  knowledge_search
  knowledge_get
  knowledge_source_sync
```

`knowledge_discover` 输入 Skill requirement、task query 和 platform scope，返回 bounded K0 candidates；`knowledge_search` 必须接收已选 KB/build IDs，不默认搜索整个 tenant。

## 11. 评测与遥测

分开评测后再做 end-to-end attribution：

- Skill：routing precision、discovery contract correctness、tool/safety/task outcome。
- Knowledge discovery：候选召回、KB selection usefulness、ambiguity/no-match。
- Knowledge build：citation coverage、contradiction、orphan link、freshness、source coverage。
- Retrieval：selected evidence usefulness、citation correctness、budget/latency。
- End-to-end：本次 outcome 同时关联 `skill_version_id` 和 `knowledge_resolution_run_id`。

不创建把 Skill 与 Knowledge 混成一个不可解释数字的综合总分。

## 12. 渐进实施路线

以下 K1–K5 是实施里程碑编号，与第 6 节的知识披露层级 K0–K3 无关。

### K1：Knowledge source 与 immutable identity

- `internal/knowledge` 独立模块；
- `KNOWLEDGE.yaml`、Git/local source、repo/path boundary；
- source revision、canonical package hash、account/workspace ACL；
- Markdown/text document snapshots 与 provenance；
- 复用 bounded Git archive 能力。

### K2：Knowledge catalog 与检索

- K0 collection cards、K1 document/section cards；
- format-aware Markdown heading/paragraph chunks；
- PostgreSQL lexical + Qdrant semantic hybrid；
- selected K2 evidence/citation fetch；
- 1-hop 邻居扩展（K2 阶段基于 Markdown 原始文档间链接；K3 后扩展到编译页面，见 §14.5）；
- active revision、reindex、freshness 和 no-store；
- Knowledge UI 与独立 MCP。

### K3：Knowledge compiler / persistent wiki

详细设计见 `knowledge-wiki-compiler-k3-v0.1.md`（含平台侧编译决策、不可重现生成物的
建模约束、异步 job 与成本控制、验收标准与实施顺序）。要点清单：

- KnowledgeProfileVersion；
- candidate KnowledgeBuildRevision；
- typed page/link/citation graph（`references/contradicts/supersedes/elaborates/mentions_entity`，见 §14.5）；
- entity pages 作为规范锚点与 entity ID exact-match 入口；
- 编译期 synthesis/overview pages；
- compiler/model/prompt provenance；
- lint、contradiction、orphan/data-gap、stale cascade checks（recursive CTE）；
- human/policy promotion；
- query synthesis 只产生 proposal。

### K4：Skill-driven dynamic discovery

- Skill frontmatter `knowledge` contract compiler/lint；
- `knowledge_discover` 和 K0 candidate ranking；
- Agent dynamic selection、fallback 和 budgets；
- KnowledgeResolutionRun；
- strict SkillVersion + BuildRevision attribution；
- discovery/retrieval/end-to-end eval。

K4 与 M1 的定位已修正：ResolutionRun 与 `skill_version_id` + `session_id` 关联不只是审计
与遥测能力，它们是**演进闭环的必要条件**——validation 信号缺少这两个归因锚点就无法定位
到具体 build/page/citation，只能得到"这个账号不太满意"这类没有行动价值的结论。因此
K3.9（信号 → 归因 → proposal）实际排在 K4 与 M1 之后，见 K3 设计文档 §7.4 与 §12。

### K5：可选企业扩展

- private Git/provider credentials；
- object storage、大文件和多格式 parsers；
- saved Agent/Profile discovery scope；
- optional pinned build；
- external KB provider adapter；
- branch/PR-based wiki export 与审批。

## 13. Memory Plane 与四层信息模型（v0.2 增补）

Skill 和 KnowledgeBase 之外，AgentMate 已实现的 Memory 模块与 todos/notes 等业务模块构成完整的四层信息模型。四层回答不同问题，权威性来源不同，不能共享同一 retrieval namespace（当前实现为同一 Qdrant collection 内按 account + namespace payload 过滤隔离），但共享同一套 retrieval 基础设施。

### 13.1 四层定义

| 层 | 回答的问题 | 生命周期 | 策展程度 | 权威性来源 | 现状 |
|---|---|---|---|---|---|
| Skill | 该怎么做 | versioned release + activation（promotion 规划中） | 高 | 版本评测与 promotion | 已实现（promotion 除外） |
| KnowledgeBase | 领域内什么是真的 | source revision + compiled build | 高 | source citation 与编译审查 | 里程碑 K1/K2 已实现（compiled build 未实现） |
| Memory | 经历过什么、学到什么 | event 永久追加；durable memory 可 supersede | 低-中 | evidence（真实发生的事件） | 已实现（supersede 仅数据模型、反馈未接线） |
| App Facts（todos/notes/expenses/bookmarks） | 用户当前事务状态 | 用户 CRUD，随时可变 | 无 | 用户最新写入 | 已实现 |

冲突裁决顺序：用户当前指令 > 当前事实（App Facts/代码/工具输出） > KnowledgeBase > Memory。

### 13.2 Memory 的四个运行时角色

1. **Recall**：执行前检索相关经验、已知失败和用户纠正，位置在 Knowledge discover 之后、执行之前。KB 提供“文档说应该怎样”，Memory 提供“上次实际发生了什么”。
2. **Journal**：执行中的 append-only 审计流。`skill_logs` 是逐次执行的结构化信号（含简短 `failure_reason`/`user_correction` 字段）；Phase 4 质量报告只取计数与 log IDs，不复制正文。更丰富的证据正文（完整失败过程、纠正上下文、决策链）归 memory events。
3. **Evidence for Skill evolution**：未来 Skill 演化提案的证据包 = version-bound telemetry 计数 + 通过 `skill_version_id`/`session_id` 关联的 memory correction/failure 正文。
4. **Promotion source**：KB 的原料，见 13.3。

### 13.3 Memory → KB promotion 管道

Karpathy 模型中“好答案写回 wiki、知识复利”的 SaaS 化落点。Agent 不直接写 KB：

```text
执行产生 episodic/semantic memory（带 evidence）
  ↓ 反复被 recall 且反馈 useful，达到 promotion 门槛
KB candidate page proposal（citation 指向 memory entries + events）
  ↓ lint / 矛盾检测 / 人工或策略审批
KB build 正式 page
  ↓
原 memory entry 标记 promoted / superseded-by-knowledge
```

约束：

- **门槛是使用信号，不是生成信号**：写入不代表值得进 KB；多次 recall 且反馈 useful 才是候选，与 Phase 4 telemetry 的样本门槛哲学一致。
- **promotion 跨信任边界**：Memory 是 account 私有；KB 可能 workspace 共享。私有经验进入共享知识必须过审批，这是权限模型要求。
- **反向矛盾检测**：promotion 后若后续 memory correction 与 KB 页面矛盾，触发 KB contradiction lint。

### 13.4 App Facts 的两条利用路径

- **运行时 source facts（轻，先做）**：Skill 执行时按需实时查询 todos/notes；不进入 embedding 索引，避免任务状态索引永远过期。
- **Notes 作为个人 KB raw source（重，K3 之后）**：新增 `KnowledgeSource type: app_notes`，把选定 tag/时间范围的 notes 作为 raw source 编译成个人 wiki build。Todos 是任务状态而非知识，不走此路径。

### 13.5 Skill contract 的 memory 段

对齐 Knowledge Discovery Contract，Skill 可声明记忆需求：

```yaml
memory:
  recall:
    scopes: [repository, project]
    include: [correction, failure, procedural]
    top_k: 5
  journal:
    required_events: [decision, outcome]
  promote:
    allowed: true   # 本 Skill 的经验是否可提名进 KB
```

平台强制 account scope 与 `memory:r/rw` 权限；contract 只声明行为，不扩权。lint 可检查声明与实际记录行为的一致性。

### 13.6 Context Pack

执行时由 Context Compiler 合成四层最小上下文，每条内容带来源标签，供模型区分权威性：

```text
Context Pack
├── [SKILL]     S1 instructions + 选中 assets
├── [KNOWLEDGE] K2 evidence + citations
├── [MEMORY]    相关经验 + 失败史 + 纠正
├── [FACTS]     相关 todos / notes / 业务状态
└── [TASK]      goal contract + checkpoint
```

### 13.7 反模式

1. **Memory 自动进 KB**：没有使用反馈门槛和审批的 promotion 会把幻觉复利化。
2. **App Facts 向量化进知识索引**：任务状态随时变化，应走实时查询。
3. **用 Memory 替代 KB**：Memory 无编译、无 citation graph、无共享治理；把它当大 KB 用会退化为无结构碎片。

### 13.8 Memory 侧实施增量

与 K1–K5 主线并行，不互相阻塞：

- **M1**（关联查询已实现，migration `000024`）：`skill_logs` 与 memory events 通过 `skill_version_id` + `session_id` 关联查询。这层关联是 validation 信号的归因锚点之一。剩余项：Quality suggestion 链接证据正文。
- **M2**（K2 之后）：Context Pack API，一次调用返回带来源标签的四层最小上下文。
- **M3**（K3 之后）：Memory → KB promotion 管道；notes 作为个人 KB source。

## 14. Graph 模型与 GraphRAG 评估（v0.3 增补）

### 14.1 结论

- **Graph 作为数据模型**：采纳。本体系本质上已是一张图，边应在 PostgreSQL 中显式化为一等公民。
- **Graph DB 作为基础设施**：现阶段不引入。它会制造第二个有状态事实源或又一个需同步的派生物，且当前规模下 recursive CTE 足够。
- **GraphRAG 作为查询管道**：不采纳其管道形态（查询期 LLM 建图 + 社区摘要）；其核心思想已由 compiled wiki 以更可审计的方式覆盖，四个具体技术作为特性吸收进 K2/K3。

### 14.2 GraphRAG 与 compiled wiki 的对照

| GraphRAG 组件 | 本体系对应 | 差异 |
|---|---|---|
| LLM 抽取 entities/relations 建图 | LLM 编译 entity/concept pages 与互链 | 节点是人类可读、带引用的页面，不是未经审查的三元组 |
| Community detection + 分层摘要 | 编译期 synthesis/overview pages | 编译一次并持久化、版本化，不在查询期 map-reduce |
| 查询期图遍历 | Agent 沿 wiki links 逐跳加载（K0→K1→K2） | 遍历者是 Agent，本身带任务上下文和披露预算 |
| 图谱作为事实 | pages 强制 citation 指回 raw source | 图谱声明可回溯验证，进入 active build 前经 lint/审批 |

查询期自动建图且不经审批直接服务查询，违反本设计的 citation/promotion 治理，因此明确不采纳。

### 14.3 本设计中的六张图（1/2/6 属规划中的 Knowledge Registry）

```text
1. KB link graph        page ↔ page（references/contradicts/elaborates 等）
2. Citation graph       page → source revision
3. Provenance chain     version → revision → source → commit
4. Skill dependency     skill → skill（frontmatter dependencies，未来 DAG）
5. Memory graph         entry → evidence → event；supersede 链
6. Resolution graph     outcome → skill version + KB builds + pages used
```

### 14.4 查询分类

| 场景 | 图深度 | 判断 |
|---|---|---|
| KB lint：orphan/hub 检测、矛盾传播、source 更新的 stale cascade | 递归、不定深度 | 真图查询，最高价值；单租户千页量级下 PostgreSQL recursive CTE 足够 |
| 多跳问答（A 经 B 到 C） | 2-3 跳 | Agent 沿 link 的 agentic traversal 优于一次性图查询：每跳可判断是否继续，契合渐进披露 |
| 检索扩展：命中后给 1-hop 邻居 metadata | 1 跳 | 普通 JOIN，便宜且高价值 |
| Provenance 审计 | 固定 3-4 跳 | 固定深度 JOIN |
| Skill DAG 组合（未来） | 小图、环检测 | SQL/内存即可 |
| Memory supersede 链、失败归因 | 浅链/固定 JOIN | SQL 即可 |

只有 KB lint 是真正的图形查询，其余为固定深度 JOIN 或 Agent 自主遍历。

### 14.5 吸收为特性的四个 GraphRAG 技术

全部落在 PostgreSQL，不引入新基础设施（括号中 K2/K3 为 §12 实施里程碑编号，非披露层级）：

1. **Typed edges 一等公民化**（里程碑 K3）：`knowledge_links` 保存边类型——`references / contradicts / supersedes / elaborates / mentions_entity`；矛盾传播与 stale cascade lint 依赖边类型。
2. **Entity pages 作为规范锚点**（里程碑 K3）：entity page 即图的规范节点，frontmatter 带 entity ID，为 Context Compiler 提供 exact-match 入口（先实体精确匹配，再语义扩展）。
3. **1-hop 邻居扩展**（里程碑 K2）：检索命中卡片附带出/入链摘要（仅 metadata，不含正文；里程碑 K2 为原始文档级链接，里程碑 K3 起为编译页面级），Agent 决定是否跟进；一个 JOIN 的成本换 agentic traversal 的入口。
4. **编译期 synthesis pages**（里程碑 K3）：compiler 按链接聚类生成 overview/synthesis 页并强制引用，覆盖“全局性问题”，替代查询期社区摘要。

### 14.6 Graph DB 的重新评估触发条件

满足任一条件时重新评估，且届时只作为从 PostgreSQL 边表重建的**可重建投影**引入，永远不是事实源（与 Qdrant 地位一致）：

- 跨 KB / 跨租户 entity graph 成为产品功能（如组织知识图谱视图）；
- 真实负载下 recursive CTE lint 的 p99 超出可接受阈值（如单租户页面数达到十万级）；
- 图分析本身（centrality、社区演化）成为付费能力。

## 15. 当前实现兼容说明

里程碑 K1 已实现（migration `000020`、`internal/knowledge`、`internal/gitfetch`）：knowledge source 注册（git/local）、根 `KNOWLEDGE.yaml` manifest、immutable source revision 与 canonical package hash、document snapshots、active 指针、REST/MCP 与 `knowledge:r/rw` scopes。K1 的 package identity 语义：manifest 与 manifest 选中的文件参与 canonical hash；未被 include/exclude 选中的文件不进入 ingest，也不参与 identity。

里程碑 K2 已实现（migration `000021`、`internal/knowledge` catalog/chunker、`retrieval.NamespaceKnowledge`）：K0 collection cards（manifest 元数据、文档计数、索引状态）、fence-aware Markdown heading/paragraph chunking（8000 runes/chunk、256 chunks/文档、稳定 chunk key）、包内 Markdown 文档链接图（`knowledge_document_links` 派生表，ingest 事务内构建、reindex 幂等重建）、account-scoped hybrid 检索（lexical + semantic，`source_ids` 所有权校验、include_content 门控、1-hop 邻居 metadata 上限 16）、reindex 与失败容忍（embed/Qdrant 失败保留 lexical fallback，旧 revision/尾部 chunk 清理，stale vector 不可 hydration）、Knowledge UI 与独立 MCP。chunk 正文保存在 retrieval 投影中是 K2 evidence 检索的设计内行为，且可从 `knowledge_documents` 完整重建。

尚未实现：KnowledgeBuildRevision、KnowledgeProfile、knowledge compiler / persistent wiki、`knowledge_discover` 与 KnowledgeResolutionRun（K3–K5）。

M1 归因锚点（migration `000024`、`internal/memory`）：`memory_events` 新增可空 `skill_version_id`。仅有 `session_id` 不足以归因——一个会话通常跑多个 skill，会话级关联无法判断某个 event 由哪次执行产生。字段可空是刻意的：并非所有 event 都源自 skill 执行（用户手写的笔记就没有），强制取值会迫使调用方编造归因，比诚实记录"来源未知"更糟。外键是 account-scoped 复合键，跨账号归因在数据库层即不可能；service 层的存在性检查只为把约束违反转成可读错误。`skill_version_id` 参与幂等 hash：否则新增归因的重放会静默返回原来那条无归因记录，调用方会以为归因成功了。

对外提供 `GET /api/memory/timeline`（skill 执行与 memory event 的时序合并，必须带 `session_id` 或 `skill_version_id` 锚点，并显式报告 `unattributed_count` 与 `truncated`）与 `GET /api/memory/entries/:id/attribution`（反向解析 entry → source event → skill version，用 `resolution` 报告链路走到哪一步：`skill_version` / `session_only` / `event_only` / `none`）。union 在数据库内完成而非在 Go 里合并两个结果集，因为 LIMIT 必须作用于合并后的排序——先各自 LIMIT 再合并会静默丢掉交错的尾部。

账号删除的残留修复（migration `000025`）：`skill_versions` 与 `skill_logs` 的 `account_id` 外键原为 `ON DELETE SET NULL`，导致两个真实后果——（1）账号可能删不掉，因为 `idx_skill_versions_global_active` 是 `UNIQUE(skill_name) WHERE account_id IS NULL AND is_active`，第二个持有同名 active skill 的账号被删时会与第一个的孤儿行冲突；（2）客户内容在账号删除后残留（`skill_versions` 存完整 `SKILL.md` 正文，`skill_logs` 存 `trigger_text` 与 `user_correction`）。已改为 `CASCADE`。所有 skill 查询均按 `account_id = $1` 过滤，无任何路径把 `account_id IS NULL` 当作全局可见，因此这从未构成跨账号泄漏，只是保留问题与删除阻塞。既有孤儿行刻意不自动清理：它们已无法归属到账号，静默删除无主的客户数据是更差的默认行为。

Domain 建模（migration `000022`、`internal/pkgpath`）：Skill 与 Knowledge package 按领域组织在仓库目录下（`platform/retrieval`、`product/faq`）。领域从 `package_path` 首段推导并落成 `skill_sources.domain` / `knowledge_sources.domain` 列，规则只在 `internal/pkgpath` 实现一份，两个 registry 共用——否则领域分组在两侧含义不同。约定：单段路径**没有** domain（扁平 package 未按领域组织，把自身名当领域会凭空造出一个分组），未分类记为 `''` 而非 NULL，避免三值逻辑。source name 由完整路径段拼接（`platform/retrieval` → `platform-retrieval`），因为 `knowledge_sources` 唯一键是 `(account_id, name)`，此前用 basename 推导会让不同领域下同名子目录静默互相覆盖。domain 不接受客户端输入，一律由 package 位置推导。`knowledge_catalog_list` 返回 domain 与全账号 domain 清单（含 collection 计数），`knowledge_search` 支持 domain 过滤并与 `source_ids` 取交集（只能收窄，不能放宽）。

Source 注册的撞名保护：注册按 name upsert，而 name 由 `package_path` 推导，因此两个**不同的** package 仍可能推导出同一个 name——例如扁平路径 `product-support` 与领域路径 `product/support`。此前第二次注册会静默把第一个 source 改指到另一个仓库，使该 source 的 revision 历史横跨两个互不相关的来源，审计时无法区分。现在 upsert 的 `ON CONFLICT` 子句带 `WHERE type/repository_url/package_path` 相同的条件：同一个 package 才原地更新，否则拒绝并报出占用该名字的来源，要求显式指定不同的 name。守卫放在 conflict 子句而不是先 SELECT 再判断，是为了不与并发注册竞态。

CJK lexical 检索（migration `000023`、`internal/retrieval/lexical.go`）：PostgreSQL 的 `simple` 配置不对 CJK 分词，整段中文会成为单个 token，因此在此之前**中文查询的 lexical 命中恒为 0**，hybrid 退化为纯 semantic（RRF 融合分上限恒为单通路的 0.5），文档中"embedding 或向量库失败时降级 lexical fallback"对中文语料并不成立。

现采用 bigram 投影修复：`retrieval_documents.lexical_text` 存 title 与 content 的重叠字符 bigram 投影（CJK 连续段切 bigram，ASCII 段保留整词并小写），GIN 索引建在 `to_tsvector('simple', lexical_text)` 上；查询侧由同一个 Go 函数生成 tsquery——CJK bigram 之间 OR，ASCII 整词之间 AND。索引侧与查询侧必须共用这套规则，这是方案的正确性前提，因此规则只在 Go 中实现一份，不在 SQL 里重写。

刻意不做词典分词（如 jieba/gse）。取舍：分词 precision 更高、存储更省，但（1）自造术语需靠词典覆盖，而 wiki 层演进会持续新增术语，词典变更会使既有索引与查询的切分口径不一致且静默失效，需全量重建；（2）分词绑定长词后无法用部分词命中（"披露"查不到"渐进披露"），而部分词导航是主要用法。bigram 规则永恒、召回无漏洞，precision 由 RRF + topK 兜住——lexical 在 hybrid 中只是候选生成器，不决定最终排序。代价是投影文本约为原文 3 倍字节。`lexical_text` 是派生列，可随时由 `LexicalProjection` 重算，因此该选择可逆：将来若语料转向通用中文词汇（词典覆盖率高）或存储压力显现，可用真实查询日志对比两种投影的召回率后替换，只需换函数并重建一列。

绕过 Go 写入路径的行（例如历史 migration 直接 UPDATE 的 retrieval document）投影为空，对 lexical 不可见，须调用 `POST /api/admin/retrieval/lexical/rebuild` 重算；该操作从已存 title/content 派生，不重新 embedding、不触碰 Qdrant。

在后续迁移前：

- 现有 Skill package 中 scripts/templates/schemas/tests/examples 继续作为 Skill resources。
- 现有 reference 文档仍可按现有 API 加载，但新设计不再把它们视为独立 KB；后续需要显式分类和迁移。
- 不修改 Phase 1 完整 Skill package identity，也不把尚未实现的 dynamic discovery 写成已支持能力。

后续实现以本文件为目标边界；具体 migration、REST/MCP DTO、compiler 和 UI 必须另行设计、测试和审查。
