# AgentMate Memory 产品与技术设计

**版本**：v0.3  
**日期**：2026-07-14  
**状态**：PROPOSED  
**范围**：AgentMate 通用记忆系统；Skill Registry 继续复用同一检索基础设施，但保持独立业务命名空间。

---

## 0. 执行摘要

AgentMate Memory 不应只是“把对话切块后做 embedding”，也不应一开始就变成昂贵的全自动知识图谱。

建议采用以下架构：

> **PostgreSQL 保存可审计事实与当前投影，Qdrant 保存可重建的派生索引；写入以事件为起点，检索采用确定性、全文、向量、时序和反馈融合；Working Memory 管理执行状态，长期 Memory 保存可复用经验；主动干预和“做梦”建立在证据、成本预算与反馈闭环之上。**

关键决策：

1. **统一检索内核，不统一业务模型**：Skill 与 Memory 共用 embedding、Qdrant、查询日志、反馈和重排能力，但分别使用 `skills`、`memory` namespace。
2. **事件不可变，记忆投影可演化**：原始事件 append-only；当前记忆可以合并、替代、失效和归档，所有变化可追溯、可重放。
3. **Working Memory 与长期 Memory 分离**：执行状态不是普通 Note，也不应直接塞入长期向量库。
4. **确定性优先，LLM 后台增强**：同步写入不调用 LLM；便宜 LLM 仅用于隐式信息提取、冲突判断、多跳检索与离线 consolidation。
5. **Qdrant 不是事实源**：删除索引不会丢记忆，所有向量均可从 PostgreSQL 重建。
6. **先采集反馈，再做学习**：没有“检索后是否有用”的数据，就不应急于做 Bandit、GRPO 或自动修改记忆结构。

---

## 1. 八篇报告的设计结论

这些论文笔记提供了八个互补维度，但实验规模、任务环境和成本口径并不一致，结果应作为方向性证据，而不是直接拼装成一个系统。

| 工作 | 可采用的核心思想 | 不应直接照搬 |
|---|---|---|
| Proactive Memory Agent | 记忆必须在正确时机重新影响行动；“保持沉默”是显式决策 | 每 N 步都调用独立大模型 |
| AutoMem | `consult-before-write`；记忆结构和操作规则可根据反馈优化 | MVP 阶段训练专属 LoRA 或自动改生产 schema |
| projectmem | append-only 事件、证据可读、失败历史的动作前门禁 | 完全排斥向量检索；AgentMate 需要跨来源语义召回 |
| AdMem | 语义、情节、过程记忆分层；从成功和失败中学习 | 每一步并行 Actor/Critic/Memory 三套 LLM |
| MRAgent | 复杂查询需要迭代重建，而非固定一次 top-k | 所有查询都走多轮 LLM 图遍历 |
| Mage | Working Memory 应保存执行路径；错误分支不能污染 active path | 一开始实现完整状态树和复杂回滚引擎 |
| Agent Memory 系统分析 | 构建成本通常比检索成本更危险；必须测全生命周期成本 | 只用单一 benchmark 的精度决定架构 |
| TokenMizer | 决策、状态、替代关系必须类型化；按 token 预算输出恢复块 | 直接实现 OpenAI 代理层或 14 类完整知识图 |

综合后，AgentMate 需要同时覆盖五种能力：

1. **Capture**：可靠记录发生过什么。
2. **Consolidate**：从事件形成可复用记忆。
3. **Recall**：按任务需要召回证据。
4. **Resume**：恢复正在执行的目标、决策和未解决问题。
5. **Intervene**：在重复失败或违反约束前主动提醒。

---

## 2. 对 v0.2 设计的审查

v0.2 已正确识别主动干预、Working Memory、生命周期和构建成本，但仍有以下结构性缺口。

### 2.1 资源模块不等于记忆类型

`Notes = Semantic`、`Reports = Episodic`、`Skills = Procedural` 过于直接。

Notes、Reports、Bookmarks、Skills、对话和工具轨迹都是**来源资源**；Semantic、Episodic、Procedural 是从来源中提炼出的**记忆投影**。一篇 Report 可以同时产生：

- 一个情节记忆：某次部署发生了什么；
- 一个语义记忆：生产端口是 26001；
- 一个过程记忆：部署前必须先构建前端静态资源；
- 一个失败记录：某条命令在该环境不可用。

### 2.2 Working Memory Schema 过早膨胀

把 `trace_nodes`、`summary_nodes`、`decisions`、`issues`、`attempts` 全部放进单个对象，会产生：

- 高频更新和并发覆盖；
- 单行无限增长；
- 难以幂等重试；
- 无法按事件重放；
- 不利于局部索引和审计。

应改为 `session + append-only events + checkpoints/projections`。

### 2.3 检索设计不完整

原来的“Tag -> Vector -> LLM”缺少：

- PostgreSQL 全文检索；
- 精确资源、实体、作用域和失败状态过滤；
- 词法与向量融合；
- 时间、重要度、置信度、成功/失败反馈；
- 查询规划与停止条件；
- 证据和命中原因；
- 检索后是否真正被使用的反馈。

### 2.4 Phase 顺序存在依赖倒置

动作前门禁依赖可靠的 `attempt/decision/issue` 数据。如果先做门禁、后做事件采集，系统没有足够数据可警告。正确顺序应是：

> 事件基底 -> 显式记忆 -> 混合检索 -> Working Memory -> 门禁 -> consolidation/dream -> 主动干预。

### 2.5 缺少多租户和索引生命周期

所有记忆、事件、索引、查询和反馈必须同时关联：

- `account_id`：数据隔离边界；
- `user_id`：行为主体；
- `key_id`：写入来源和审计；
- `scope_type/scope_key`：global、project、repository、agent、session 等作用域；
- embedding 模型、维度、内容哈希和索引版本。

---

## 3. 总体架构

```text
对话 / 工具调用 / Notes / Reports / Bookmarks / Skills
                         |
                         v
              [1] Memory Event Journal
              append-only, idempotent, auditable
                         |
              +----------+----------+
              |                     |
              v                     v
    [2] Working Projection   [3] Durable Memory Projection
    session state/path       semantic/episodic/procedural
              |                     |
              +----------+----------+
                         |
                         v
              [4] Unified Retrieval Core
       exact + PostgreSQL FTS + Qdrant dense + fusion
                         |
              +----------+----------+
              |                     |
              v                     v
       Resume / Search       Precheck / Notify
                         |
                         v
              [5] Feedback & Consolidation
        used / ignored / helpful / harmful / superseded
```

### 3.1 存储职责

| 存储 | 职责 |
|---|---|
| PostgreSQL | 事件事实源、当前记忆投影、Working Memory、关系、证据、查询日志、反馈、任务队列 |
| Qdrant | `retrieval_documents` 的派生向量索引；按 account/namespace/scope/type 过滤 |
| 对象或原始资源 | 完整会话、报告、文件等大对象；Memory 只保存引用、证据摘录与结构化结论 |

暂不引入图数据库。关系规模在 MVP 阶段可由 PostgreSQL `memory_relations` 表承担；只有确认出现高频深图遍历和性能瓶颈后再评估图存储。

---

## 4. 核心数据模型

现有 `memory_entries`、`memory_evidence`、`retrieval_documents`、`retrieval_queries/results/feedback` 可以继续使用。新增模型如下。

### 4.1 `memory_events`：不可变事实日志

```text
id, account_id, user_id, key_id
scope_type, scope_key
session_id, sequence_no
event_type:
  goal | observation | action | decision | issue |
  attempt | outcome | correction | checkpoint | note
payload JSONB
source_type, source_id, occurred_at
idempotency_key, content_hash
created_at
```

约束：

- `(account_id, idempotency_key)` 唯一；
- event 只追加，不原地修改；
- correction/supersede 通过新事件表达；
- payload 保留结构化字段，长原文只保存 source reference 和 excerpt。

### 4.2 `memory_entries`：可复用长期记忆

保留现有表，扩展：

```text
memory_type: semantic | episodic | procedural
status: active | superseded | invalidated | archived | expired
valid_from, valid_to
superseded_by
extraction_method: explicit | rule | llm | dream
extractor_version
access_count, useful_count, harmful_count
```

每条长期记忆必须至少有一条 `memory_evidence`。没有证据的 LLM 推断只能进入 `pending`，不能直接成为 active。

### 4.3 `memory_relations`：轻量关系层

```text
from_memory_id, to_memory_id
relation_type:
  related_to | depends_on | caused_by | contradicts |
  supersedes | supports | derived_from
weight, metadata
```

### 4.4 Working Memory

```text
working_sessions:
  id, account_id, user_id, key_id
  external_session_id, agent_id, project_key
  goal, status, current_checkpoint_id
  started_at, expires_at, completed_at

working_events:
  id, session_id, sequence_no
  event_type, payload, parent_event_id, branch_key
  outcome, created_at

working_checkpoints:
  id, session_id, through_sequence_no
  goal, active_plan, decisions, open_issues
  failed_attempts, recent_context, token_count
  created_at
```

MVP 不实现完整 Mage 树，只保留 `parent_event_id + branch_key`，足以区分 active path 和失败 sibling branch。

### 4.5 `memory_interventions`

记录每一次主动提醒或保持沉默：

```text
session_id, trigger_type, candidate_memory_ids
decision: notify | silent
message, score, policy_version
accepted, outcome, latency_ms, created_at
```

这张表是未来优化干预阈值的训练与评估数据。

---

## 5. 写入与提取管道

### 5.1 同步路径：便宜、可靠

1. 接收显式事件或资源变更；
2. 校验 account/scope/schema；
3. 通过 `idempotency_key` 去重；
4. 写入 `memory_events`；
5. 更新最小 Working Projection；
6. 投递后台 extraction/index job；
7. 立即返回。

同步路径不调用 LLM。

### 5.2 三档提取

| 档位 | 方法 | 使用场景 |
|---|---|---|
| L0 Explicit | Agent 直接调用 `record_decision/attempt/outcome` | 最可信，优先使用 |
| L1 Deterministic | 规则、字段映射、状态机、资源 metadata | 默认开启，零 LLM 成本 |
| L2 Enriched | DeepSeek Flash 等便宜 LLM | 隐式决策、摘要、冲突候选、关系提取 |

L2 必须异步、批量、可限流，并保存：

- 模型与 prompt 版本；
- 输入事件范围；
- 输出 schema 校验结果；
- token、费用、延迟；
- 失败重试和 dead-letter 状态。

### 5.3 `consult-before-write`

创建长期记忆前，先在同 account/scope/type 下执行：

1. 内容哈希去重；
2. 精确实体/资源匹配；
3. 全文与向量近邻查询；
4. 判断 `create | merge | supersede | contradict | ignore`。

MVP 使用确定性阈值；LLM 只处理模糊冲突。

---

## 6. 统一检索系统

### 6.1 共享内核

Skill Registry 和 Memory 复用：

- OpenAI 兼容 embedding client；
- Qdrant collection 管理；
- `retrieval_documents` 与索引任务；
- 查询日志、结果阶段和反馈；
- account/key 归属；
- 未来 cheap LLM query planner/reranker。

业务隔离：

- Skill：`namespace=skills`；
- Memory：`namespace=memory`；
- 任何查询必须强制过滤 `account_id + namespace`；
- Memory 额外过滤 scope、memory_type、status、时间和实体。

### 6.2 多通道候选召回

```text
A. Deterministic
   resource/entity/session/decision/failed-attempt 精确命中

B. Lexical
   PostgreSQL FTS，保留代码符号、路径、错误文本的精确性

C. Dense Semantic
   Qdrant + text-embedding-v4，处理表达差异

D. Structural/Temporal
   relation、active path、recency、validity、supersede 状态
```

候选先分别取 top-N，再用 RRF 融合。最终分数由以下因素组成：

```text
final =
  fusion_rank
  * validity_weight
  * scope_weight
  * confidence_weight
  * feedback_weight
  + importance_boost
  + recency_boost
```

不能把向量相似度直接当作最终可信度。

### 6.3 四种检索模式

| 模式 | 目标 | 默认策略 |
|---|---|---|
| `resume` | 恢复当前执行状态 | session 精确查询 + checkpoint |
| `recall` | 找相关知识与经验 | hybrid retrieval + fusion |
| `precheck` | 动作前识别冲突和失败历史 | deterministic first，不依赖 LLM |
| `reconstruct` | 多跳、时间、跨事件问题 | cheap LLM 规划 2~3 轮受控检索 |

只有 `reconstruct` 默认允许多步 LLM。系统应有最大轮数、候选数、token、延迟和费用预算。

### 6.4 Token 预算输出

借鉴 TokenMizer，提供三档 context pack：

- `critical`：目标、硬约束、active decision、阻塞问题，约 150 tokens；
- `standard`：再加关键证据、近期尝试，约 500 tokens；
- `full`：完整 checkpoint 与相关长期记忆，约 1200 tokens。

输出必须附带 `memory_id`、来源证据、命中原因和状态，Agent 才能判断可信度。

---

## 7. Working Memory 与长期记忆的边界

### 7.1 Working Memory 保存

- 当前目标和计划；
- 已确认决策及其替代历史；
- open issues；
- failed/partial attempts；
- 最近 observation/action；
- active branch 和 checkpoint。

### 7.2 长期 Memory 保存

- 可跨 session 复用的事实；
- 有明确时间和结果的情节；
- 可指导未来行动的过程经验。

### 7.3 Session 完成时

1. 写入最终 checkpoint；
2. 生成候选 episodic memory；
3. 从高价值 decision/outcome 中生成 semantic/procedural 候选；
4. 关联原始 event evidence；
5. 通过 consult-before-write 去重和冲突判断；
6. Working Session 归档，原始事件按 retention policy 保留。

不是每个 action 都应进入长期记忆。

---

## 8. 动作前门禁与主动干预

### 8.1 Precheck：确定性治理

输入：

```json
{
  "session_id": "...",
  "action_type": "edit_file",
  "resources": ["internal/reports/repo.go"],
  "intent": "allow report content updates"
}
```

输出：

- 相关 failed/partial attempts；
- 未解决 issue；
- 当前有效决策和硬约束；
- 冲突警告；
- 证据链接。

Precheck 应在 tool call 边界调用，不应依赖周期性轮询。

### 8.2 Notify：选择性注入

触发条件：

- checkpoint 后；
- 计划或资源发生变化；
- 连续失败；
- context 即将压缩；
- 检索到高重要度且与下一动作强相关的记忆。

决策顺序：

1. 硬规则判断必须提醒；
2. 计算相关性、重要度、时效和干扰成本；
3. 边界样本才调用 cheap LLM；
4. 输出 `notify` 或显式 `silent`；
5. 记录是否被采用及后续结果。

---

## 9. “做梦”机制：Evidence-bound Consolidation

“做梦”不应是自由生成新事实，而是低优先级、证据约束的离线 consolidation。

### 9.1 可执行任务

- 重复记忆聚类和合并建议；
- 冲突、过期和 supersede 检测；
- 多个 episodic memory 中提炼稳定 semantic memory；
- 从成功/失败 attempt 中提炼 procedural memory；
- 降低长期未使用或负反馈条目的权重；
- 为缺失摘要、tags、relations 生成候选；
- 发现 schema 或操作规则导致的重复写入、空检索和错误干预。

### 9.2 安全边界

- 所有结论必须引用 evidence；
- 默认生成 proposal，不直接覆盖 active memory；
- 高置信度确定性合并可自动执行；
- LLM 生成内容先进入 `pending`；
- 每次运行有 account 级费用、token、条目数和时间预算；
- 所有变更写成新 event，可回滚、可比较；
- 同一批数据和策略版本应可重复运行。

### 9.3 运行节奏

- 高频轻任务：去重、TTL、索引修复；
- 每日：冲突候选、弱记忆降权；
- 每周：跨 session 模式提炼、scaffold 分析；
- 手动：高成本重新提取或全量 reindex。

---

## 10. API 与 MCP 草案

### 10.1 REST

```text
POST /api/memory/events
GET  /api/memory/entries
GET  /api/memory/entries/:id
POST /api/memory/search
POST /api/memory/precheck
POST /api/memory/feedback

POST /api/memory/sessions
POST /api/memory/sessions/:id/events
POST /api/memory/sessions/:id/checkpoints
GET  /api/memory/sessions/:id/context?budget=critical|standard|full
POST /api/memory/sessions/:id/complete

POST /api/memory/jobs/consolidate
POST /api/memory/jobs/reindex
GET  /api/memory/jobs/:id
```

### 10.2 MCP

MVP 保持工具数量克制：

```text
memory_resume
memory_search
memory_record
memory_checkpoint
memory_precheck
memory_feedback
```

`notify` 是宿主 Agent 的集成回调，不要求 Action Agent 主动调用。

---

## 11. 分阶段开发计划

### Phase 1：事件基底与显式记忆

目标：先可靠地“记住”，暂不追求智能。

- `memory_events`、relations 和索引 migration；
- 显式 record API/MCP；
- memory entry CRUD 与 evidence；
- memory entry -> retrieval document -> Qdrant 索引；
- PostgreSQL FTS + Qdrant dense + RRF；
- account/key/scope 强制隔离；
- 查询和反馈日志。

验收：

- 重复请求幂等；
- 任一记忆可追溯到证据；
- 删除 Qdrant collection 后可全量重建；
- 跨 account 泄漏测试为零。

### Phase 2：Working Memory、Resume 与 Precheck

- session/events/checkpoints；
- critical/standard/full context pack；
- decision、issue、attempt、outcome 状态机；
- session 完成归档；
- failed attempt / decision conflict 的确定性 precheck。

验收：

- 新 session 可恢复目标、有效决策、open issues 和失败历史；
- active path 不包含 invalidated branch；
- 重复失败率可量化。

### Phase 3：后台提取与“做梦”

- extraction/index job worker；
- DeepSeek Flash 结构化提取；
- consult-before-write；
- merge/supersede/contradict proposals；
- 每日/每周 consolidation；
- 成本预算和审计。

验收：

- 每条 LLM 记忆有 evidence 和 extractor version；
- 自动合并可回滚；
- 构建成本、失败率、队列延迟可观测。

### Phase 4：主动干预与多步重建

- trigger policy；
- notify/silent 记录；
- 2~3 轮受控 reconstruct；
- cheap LLM rerank/planner；
- 干预反馈和阈值调整。

验收：

- 干预 precision、接受率和 harmful rate 可测；
- p95 延迟和单次费用不超过预算；
- silent 是可观测决策。

### Phase 5：学习型优化

只有积累足够 feedback 后再做：

- procedural memory 的反馈权重或 Bandit；
- 自动 scaffold proposal；
- 个性化检索权重；
- SFT/LoRA/GRPO 可行性评估。

---

## 12. 评估与可观测性

### 12.1 质量

- Resume completeness：目标、决策、issue、attempt 恢复完整率；
- Recall precision/recall、nDCG；
- Evidence coverage；
- contradiction/stale rate；
- repeated-failure avoidance rate；
- notify precision、accepted rate、harmful rate；
- 检索结果 used / ignored / corrected。

### 12.2 系统

- 写入、构建、检索、生成四阶段 p50/p95；
- 每 1,000 events 的 embedding 与 LLM 成本；
- index lag、失败重试、dead-letter 数；
- PostgreSQL/Qdrant 存储增速；
- 每个 account 的预算使用；
- 跨租户隔离和越权测试。

### 12.3 MVP 基线

必须保留三条简单基线进行对照：

1. 最近 N 条原始事件；
2. PostgreSQL FTS；
3. 单次 Qdrant top-k。

复杂系统只有在质量或成本指标显著优于基线时才保留。

---

## 13. 主要风险

| 风险 | 控制 |
|---|---|
| LLM 提取把推测写成事实 | evidence 强制、pending 状态、schema 校验 |
| 写入成本随历史超线性增长 | append-only + 增量投影 + consult-before-write + 批处理 |
| 向量召回相关但不可信 | validity/scope/confidence/feedback 融合，返回证据 |
| Working Memory 无限增长 | checkpoint、token budget、TTL、session completion |
| 主动提醒干扰主任务 | hard-rule first、显式 silent、阈值与 harmful feedback |
| 自动 consolidation 误合并 | proposal 默认、可回滚事件、策略版本 |
| 多租户数据泄漏 | PostgreSQL account 条件 + Qdrant account payload 双层过滤 |
| embedding 模型升级造成不一致 | model/dimension/version 记录，双索引迁移与 reindex job |

---

## 14. 最终结论

AgentMate 已经拥有正确的基础零件：PostgreSQL、Qdrant、OpenAI 兼容 embedding、便宜 LLM 配置、retrieval documents、查询日志、反馈表、memory entries/evidence，以及 account/key 归属。

下一步不是引入更多概念，而是把这些零件闭环：

> **事件可追溯，记忆有证据，索引可重建，检索可解释，反馈可学习，干预可评估，成本可约束。**

建议立即开始 Phase 1。Working Memory、主动干预和“做梦”都应建立在这一基底之上。
