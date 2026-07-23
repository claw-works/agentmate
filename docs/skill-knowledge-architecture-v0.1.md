# AgentMate Skill + Knowledge Architecture v0.1

**日期**：2026-07-23  
**状态**：PROPOSED（后续实现依据；当前代码尚未实现 Knowledge Registry）  
**范围**：Skill Registry、未来 Knowledge Registry、Knowledge Compiler、运行时 Knowledge Resolution。

## 0. 决策摘要

AgentMate 将 Skill 与 Knowledge 建模为两个独立业务域：

- **Skill** 回答“何时以及如何行动”，保存 routing、instructions、constraints、tools、execution assets 与 evals。
- **KnowledgeBase（KB）** 回答“有哪些可引用的事实与综合知识”，保存 immutable raw sources、持久化 wiki builds、pages、links 与 citations。
- Skill 不固定绑定具体 KB。Skill package 声明结构化 **Knowledge Discovery Contract**，指导 Agent 在运行时发现当前 account/workspace 可访问的 KB。
- 初始模型不创建预配置 `BindingRevision`。每次执行把实际选中的 KB build、page、chunk 和 citation 固化为 **KnowledgeResolutionRun**，用于权限审计、复现和评测。
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

Knowledge source package hash 对 manifest 和 raw source 文件的 canonical path/hash/size 计算。Knowledge build identity 单独包含：

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
- active revision、reindex、freshness 和 no-store；
- Knowledge UI 与独立 MCP。

### K3：Knowledge compiler / persistent wiki

- KnowledgeProfileVersion；
- candidate KnowledgeBuildRevision；
- page/link/citation graph；
- compiler/model/prompt provenance；
- lint、contradiction、orphan/data-gap checks；
- human/policy promotion；
- query synthesis 只产生 proposal。

### K4：Skill-driven dynamic discovery

- Skill frontmatter `knowledge` contract compiler/lint；
- `knowledge_discover` 和 K0 candidate ranking；
- Agent dynamic selection、fallback 和 budgets；
- KnowledgeResolutionRun；
- strict SkillVersion + BuildRevision attribution；
- discovery/retrieval/end-to-end eval。

### K5：可选企业扩展

- private Git/provider credentials；
- object storage、大文件和多格式 parsers；
- saved Agent/Profile discovery scope；
- optional pinned build；
- external KB provider adapter；
- branch/PR-based wiki export 与审批。

## 13. 当前实现兼容说明

当前代码只实现 Skill Registry 的 L0/L1/L2 selected resource API，没有实现本设计中的 KnowledgeBase、KnowledgeBuildRevision、KnowledgeProfile 或 KnowledgeResolutionRun。

在迁移前：

- 现有 package 中 scripts/templates/schemas/tests/examples 继续作为 Skill resources。
- 现有 reference 文档仍可按现有 API 加载，但新设计不再把它们视为独立 KB；后续需要显式分类和迁移。
- 不修改 Phase 1 完整 Skill package identity，也不把尚未实现的 dynamic discovery 写成已支持能力。

后续实现以本文件为目标边界；具体 migration、REST/MCP DTO、compiler 和 UI 必须另行设计、测试和审查。
