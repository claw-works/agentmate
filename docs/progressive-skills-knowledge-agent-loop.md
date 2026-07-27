# 从“用英语编程”到可持续 Agent Loop：AgentMate 的 Skill、Knowledge 与 Memory 三维架构

**版本**：v0.2
**日期**：2026-07-18（v0.1）；2026-07-24（v0.2：Knowledge 独立建模，改写为三维渐进式披露）
**状态**：DESIGN NOTE
**关联材料**：`docs/Karpathy.txt`、`docs/skill-registry-design-v0.1.md`、`docs/skill-knowledge-architecture-v0.1.md`、`docs/memory-design-v0.3.md`

---

## 0. 摘要

Andrej Karpathy 对近期 Agent 编程的观察，可以概括为一个重要变化：

> 软件开发正在从“人逐行操作代码”，转向“人用自然语言描述目标、约束和成功标准，Agent 在工具环境中持续循环，直到结果被验证”。

真正产生杠杆的并不是一次生成更多代码，而是 Agent 能够围绕明确目标反复执行：理解任务、查找知识、选择方法、修改系统、运行测试、观察失败、修正假设，再次验证。

但模型的推理能力已经领先于周边基础设施。今天的主要瓶颈逐渐从“模型会不会写代码”转向：

- 它能否在正确时刻获得正确知识；
- 它能否选择适合当前任务的能力，而不是加载所有提示；
- 它能否识别自己的假设、混乱和失败；
- 它能否通过真实工具和测试形成可靠闭环；
- 它能否把一次任务中的经验沉淀为下一次可复用的知识；
- 它能否避免在循环中制造越来越多未经验证的复杂性。

AgentMate 对这些问题的回答是把 Agent 系统拆成四个相互独立但可以闭环的平面。其中三个是**渐进式披露的上下文维度**，第四个是控制器：

1. **Capability Plane（Skill）**：回答“该怎么做”，沿 S0–S3 渐进披露。
2. **Knowledge Plane（KnowledgeBase）**：回答“领域内什么是真的、证据在哪里”，沿 K0–K3 渐进披露。
3. **Memory Plane**：回答“我们经历过什么、学到什么”，沿 M0–M2 渐进披露。
4. **Control Plane（Agent Loop）**：决定“下一步做什么、是否成功、何时停止”。

三个上下文维度不是同一个知识库的不同视图，而是权威性来源不同的独立域：Skill 的权威来自版本评测与 promotion，Knowledge 的权威来自 source citation 与编译审查，Memory 的权威来自真实发生过的 evidence。冲突时的裁决顺序是：用户当前指令 > 当前事实（代码/工具输出/业务状态） > Knowledge > Memory。

```text
User Goal + Success Criteria
             |
             v
       Agent Control Loop
 Observe -> Recall -> Select -> Plan -> Act -> Verify
             |                                 |
   +---------+---------+                       |
   |         |         |                       v
 Skill   Knowledge   Memory  <- Reflect / Record <- Result / Feedback
 S0-S3    K0-K3      M0-M2
   |         |         |
   +---------+---------+
             |
             v
   Skill / Knowledge / Memory Evolution
```

AgentMate 的目标不是创建一个装满提示词的仓库，也不是再造一个通用向量库，而是建立三个可同步、可检索、可验证、可渐进加载和可演化的独立上下文域，并由一个可停止、可审计的 Loop 统一调度。

---

## 1. 从 Karpathy 的观察中提取设计输入

本文的“渐进式 Skill”与“AgentMate 架构”是基于 Karpathy 观察进一步推导的系统设计，不是对其原文观点的逐字转述。

### 1.1 从手工操作转向自然语言目标

Karpathy 描述自己从以手工编码和补全为主，快速转向以 Agent 编码为主。他称之为“mostly programming in English”。

这不意味着代码不再重要，而是人的主要工作层级上移了：

- 定义真正的问题；
- 说明系统边界；
- 给出成功标准；
- 识别错误假设；
- 审查架构和 trade-off；
- 判断最终结果是否值得进入生产。

因此 Agent 的输入不应该只是一条命令，而应该是一个可执行 contract：

```text
goal
constraints
success criteria
available tools
relevant context
evidence requirements
stop conditions
```

### 1.2 杠杆来自 Loop，而不是单次生成

Karpathy 强调，LLM 最有杠杆的地方是“looping until they meet specific goals”：先写测试，再让 Agent 通过测试；先写一个大概率正确的朴素实现，再在保持正确性的前提下优化。

这意味着 Agent 产品的核心抽象不应是 `prompt -> answer`，而应是：

```text
goal -> attempt -> observation -> correction -> verification -> outcome
```

模型负责提出下一动作，工具提供环境反馈，验证器决定是否达到目标，记忆系统保存有价值的经验。

### 1.3 错误从语法错误变成概念错误

能力提升后，模型不再频繁犯简单语法错误，更多问题来自：

- 未验证的假设；
- 没有主动澄清歧义；
- 没有暴露不一致；
- 过度设计和抽象膨胀；
- 忽略已有代码和注释；
- 在正交任务中产生意外副作用；
- 对用户过于迎合而不进行技术反驳。

这些错误很难仅靠“写一个更长的系统提示”解决。系统需要结构化地要求 Agent：

1. 在行动前召回相关知识和历史失败；
2. 显式列出关键假设；
3. 用最小实现验证方向；
4. 运行真实测试而不是口头宣称成功；
5. 对 diff、副作用和范围进行独立复审；
6. 在证据不足时请求澄清或停止。

### 1.4 Agent Swarm 不是默认答案

Karpathy 对当前“Agent swarm”热潮保持谨慎。模型仍然会犯错；增加 Agent 数量并不会自动增加正确性，反而可能制造更多协调成本、重复工作和未经验证的输出。

AgentMate 因此不把“大量并行 Agent”设为默认。更合理的初始结构是少量角色清晰的循环：

- **Coordinator**：维护 goal、约束、计划与停止条件；
- **Worker**：执行一个边界清晰的任务；
- **Verifier/Reviewer**：基于测试和 diff 独立验证。

只有任务可以真正分解、输入输出边界稳定且并行收益明显时，才增加并行 worker。

### 1.5 模型智能领先于工具、知识和组织流程

Karpathy 最后的判断是：模型智能已经明显领先于 integrations、tools、knowledge 和新的组织工作流。

这正是 AgentMate 的产品空间。AgentMate 不训练基础模型，而是补齐模型与真实工作之间的基础设施：

- 工具接入；
- Skill 注册与选择；
- 知识和记忆检索；
- 任务循环和 checkpoint；
- 可验证成功标准；
- 反馈、评估和能力演化。

---

## 2. 为什么 Skill、Knowledge 和 Memory 都必须渐进式披露

### 2.1 Context 不是无限仓库

把所有 Skill、文档、历史和工具说明一次性放入上下文，会造成：

- token 成本快速增长；
- 任务无关信息干扰推理；
- 多个 Skill 的规则互相冲突；
- 模型难以判断哪些知识真正重要；
- 更新一个 Skill 就需要重新分发巨大提示；
- 无法观测“哪个 Skill 被选择、使用和证明有效”。

渐进式披露的原则是：

> **先用最小信息完成路由，再按任务需要逐层加载；不要在 Agent 尚未选择能力、知识域和相关经验前就支付完整上下文成本。**

这条原则同时适用于三个维度：Skill 不应在选中前加载 instructions，KnowledgeBase 不应在命中前加载文档正文，Memory 不应在相关性确认前灌入全部历史。

### 2.2 Skill 不是一段 Prompt

一个可持续的 Skill 应是一个 versioned package：

```text
skill-package/
  SKILL.md              # 核心说明与边界
  references/           # 过渡期：与执行紧密相关的规范摘要
  templates/            # 输出或代码模板
  schemas/              # 数据和接口约束
  scripts/              # 可执行或辅助工具
  tests/                # 成功标准与回归验证
  examples/             # 少量高质量示例
```

> 边界更新（2026-07-23）：Skill package 中的资源定位为 **execution assets**。独立领域知识语料（产品文档、政策、事实库、持久化 wiki）属于独立的 Knowledge Registry（K1/K2 已实现），Skill 通过 Knowledge Discovery Contract 在运行时发现并引用，而不是打包进 Skill；见 [Skill + Knowledge Architecture v0.3](skill-knowledge-architecture-v0.1.md)。`references/` 仅保留与执行流程强绑定的紧凑规范摘要。

判断一份内容属于哪个维度，用一个问题即可：删除它之后，Agent 是“不知道怎么做”（Skill）、“不知道相关事实”（Knowledge），还是“不记得上次发生了什么”（Memory）。

根 `SKILL.md` 只负责说明：

- 何时使用；
- 何时不要使用；
- 输入与输出；
- 核心工作流；
- 资源导航；
- 验证要求。

真正复杂的知识、模板和执行资源在被需要时再加载。

### 2.3 三条独立披露轴

三个维度各有自己的层级编号，避免用同一套 L0/L1/L2 表达不同语义：

```text
Skill 轴（Capability）
  S0  Skill Card          name, purpose, triggers, constraints, cost, confidence
  S1  Core Instructions   SKILL.md 的核心流程和决策边界
  S2  Execution Assets    templates、schemas、scripts、tests、行为示例
  S3  Full Package        完整文件清单、immutable revision、Git provenance

Knowledge 轴（KnowledgeBase）
  K0  Collection Card     KB 名称、描述、领域、语言、freshness、文档数量、索引状态
  K1  Document/Section    文档标题、heading 结构、chunk 命中位置与 1-hop 链接邻居
  K2  Selected Evidence   命中 chunk 正文与 citation
  K3  Raw Source / Build  原始文档全文、source revision、build provenance

Memory 轴
  M0  Memory Card         scope、type、摘要、置信度、时间、supersede 状态
  M1  Memory Content      完整经验正文与适用条件
  M2  Evidence Chain      source events、attempt/correction/outcome 与原始证据
```

历史命名说明：Skill Registry 的 REST/MCP 接口沿用 `L0/L1/L2` 命名（catalog / instructions / resources），语义等价于此处的 S0/S1/S2，为兼容保留。

#### S0 / K0 / M0：路由层

三条轴的第 0 层都只做“是否相关”的判断，不加载正文。

Skill Card 应回答：

- 这个 Skill 能解决什么问题？
- 触发条件是什么？
- 禁止或不适用条件是什么？
- 需要哪些工具和权限？
- 预计成本和风险是什么？
- 当前 active version 和验证状态是什么？

Knowledge Collection Card 应回答：

- 这个 KB 覆盖什么领域？
- 语言和适用范围是什么？
- 数据更新到什么时候？
- 是否已完成索引、是否可检索？
- 引用策略是什么？

Memory Card 应回答：

- 这条经验属于哪个 scope？
- 它是事实、经历还是方法？
- 它是否已被更新或废弃？
- 证据强度和置信度如何？

#### S1 / K1 / M1：定位层

Skill 被选中后加载核心说明；Knowledge 命中后先看文档/章节结构与链接邻居；Memory 相关后再读完整经验正文。此时信息仍应紧凑，目标是判断“需要深入哪一部分”，而不是一次读完。

#### S2 / K2 / M2：证据与执行层

- S2 加载真正要用的执行资产：模板、脚本、schema、测试。
- K2 加载命中的 chunk 正文与 citation，作为可引用证据。
- M2 展开证据链，回到原始 event，用于判断这条经验是否真的适用当前情况。

#### S3 / K3：完整来源层

完整 Skill package 和 KB 原始文档主要用于审计、执行器取文件、compiler/linter、版本比较、回滚与复现。普通路由不应触及这一层。

#### 三轴不是同一次调用

Skill 路由发生在 Knowledge 检索之前：先确定要做什么，再决定需要哪些事实。Memory recall 与 Knowledge 检索并行但不混合——两者回答的问题不同，混合排序会让“上次踩过的坑”和“文档中的规定”竞争同一个 top-k 名额。

---

## 3. 知识库不是“把所有内容向量化”

### 3.1 六种上下文来源

Agent 执行任务时会使用六类不同信息：

| 类型 | 回答的问题 | AgentMate 载体 | 披露轴 |
|---|---|---|---|
| Parametric Knowledge | 模型通常知道什么 | 基础模型 | — |
| Skills | 这类任务应该如何执行 | Git-backed Skill Registry | S0–S3 |
| Domain Knowledge | 领域内什么是真的、证据在哪里 | Knowledge Registry（KB source/revision/document/chunk） | K0–K3 |
| Source Facts | 当前代码、数据和业务状态实际上是什么 | Git、PostgreSQL、业务 API、todos/notes 等 App Facts | 实时查询 |
| Durable Memory | 过去学到了哪些可复用经验 | Memory entries + evidence | M0–M2 |
| Working Memory | 当前任务进行到哪里 | events（含 checkpoint 事件类型） | 会话内 |

它们不能被混成同一个检索命名空间。方法、领域事实、当前状态、长期经验和执行状态拥有不同生命周期、权威性和更新规则。

具体隔离方式：Skill、Knowledge、Memory 共享同一套 retrieval 基础设施（PostgreSQL 事实 + 单个 Qdrant collection），但按 account + namespace 严格隔离，检索时不跨 namespace 混合排序。App Facts（todos/notes/expenses）随时可变，走实时查询而不进入 embedding 索引——否则索引永远落后于用户刚写的内容。

### 3.2 PostgreSQL 是事实源，向量索引是派生物

AgentMate 的原则是：

- PostgreSQL 保存可审计事实、关系、状态和证据；
- Git 保存 Skill package 和代码内容；
- Qdrant 保存可重建的 embedding 索引；
- 删除向量索引不会丢失 registry 或 memory；
- 检索结果必须能够返回来源和命中原因。

这避免把“向量相似”错误地当作“事实正确”。

### 3.3 三维 Context Compilation

一个好的上下文层不是简单执行 top-k，而是为当前 Loop 从三条轴分别编译最小充分上下文，再合并：

```text
Task
  -> Skill 轴：intent/capability 路由 -> 选中 SkillVersion -> S1 instructions -> 按需 S2 assets
  -> Knowledge 轴：按 Skill 声明的知识需求发现 KB -> K0 candidates -> K1 命中定位 -> K2 evidence + citation
  -> Memory 轴：scope + 任务关键词 -> M0 cards -> M1 相关经验 -> 必要时 M2 证据链
  -> Facts：exact filters（account / repository / entity / status）实时查询
  -> 各轴内部：lexical + semantic 混合，temporal/lifecycle 过滤（active / superseded / recent）
  -> 各轴分别 budget，避免一轴挤占另一轴
  -> deduplicate & 合并为 Context Pack
```

Context Pack 中每条内容都必须带来源标签，因为模型需要知道权威性差异：

```text
Context Pack
├── [TASK]      goal contract、success criteria、checkpoint
├── [SKILL]     选中的 S1 instructions 与关键约束、必要 S2 assets
├── [KNOWLEDGE] K2 evidence 与 citation（可回溯到 source revision）
├── [MEMORY]    相关经验、已知失败尝试、用户纠正
└── [FACTS]     当前代码/状态/todos 等实时事实
```

没有标签的合并上下文会让 Agent 无法裁决冲突：文档说 A、上次经验说 B、当前代码是 C 时，应按“当前事实 > Knowledge > Memory”处理，而不是取相似度最高的一条。

当前实现状态：三条轴的检索能力分别可用（Skill catalog/search、Knowledge catalog/search、Memory search），统一的 Context Pack 编译 API 尚未实现。

### 3.4 Consult before write

Agent 形成新记忆、发布新 Skill 或提名新 KB 页面前，应先检索是否已有等价内容：

- 防止重复 memory；
- 防止把同一 package 复制成多个 release；
- 防止把同一事实同时写进 Memory 和 KB 两个域；
- 发现新结论与旧事实冲突；
- 决定是更新投影、建立 supersede 关系，还是保留独立版本。

写入之后还必须有反馈：检索结果是否被使用、是否有帮助、是否造成错误。没有反馈，知识库无法判断内容质量。

---

## 4. Agent Loop：从目标到可验证结果

### 4.1 Loop contract

进入 Loop 前，Coordinator 应尽量形成以下 contract：

```yaml
goal: 要达到的结果
scope: 允许修改或操作的边界
constraints: 不可违反的条件
success_criteria: 可由工具验证的完成标准
budget: 时间、token、工具调用、成本上限
approval_policy: 哪些动作需要人确认
stop_conditions: 成功、阻塞、预算耗尽或风险升级
```

如果 contract 中最关键的信息缺失，Agent 应澄清，而不是替用户做高影响假设。

### 4.2 标准 Loop

```text
1. Observe
   获取当前代码、状态、错误和用户输入

2. Recall
   三轴分别检索：Skill Card（S0）、Knowledge collection/命中（K0/K1）、
   Memory 相关经验与失败历史（M0/M1）；另加实时 source facts

3. Select
   先选最小 Skill 集合，再按 Skill 的知识需求选定 KB 与证据；必要时组合 Skill

4. Plan
   把目标拆成可验证的小步骤，标记假设和风险

5. Act
   调用工具完成一个有边界的动作

6. Verify
   运行测试、查询状态、比较 diff 或检查真实输出

7. Reflect
   判断失败类型：实现错误、错误假设、知识缺失、工具失败或目标不清

8. Record
   记录重要 decision、attempt、outcome、correction 和 checkpoint

9. Continue or Stop
   达标则结束；可恢复失败则进入下一轮；高风险或信息不足则请求人介入
```

### 4.3 验证是 Loop 的控制信号

没有验证器，Loop 只是不断生成文本。验证器可以是：

- unit/integration/e2e tests；
- compiler、type checker、lint；
- 数据库约束和查询结果；
- 浏览器截图与 UI 行为；
- API response schema；
- benchmark 与成本预算；
- 独立 reviewer 对 diff 的检查；
- 用户明确验收。

“模型认为完成”不是完成证据。

### 4.4 失败分类

Agent 不应对所有失败机械重试。每轮失败应至少归入一类：

| 类型 | 下一动作 |
|---|---|
| Transient tool failure | 有退避和上限地重试 |
| Implementation defect | 定位最小原因并修复 |
| Wrong assumption | 回到 Observe/Recall，更新假设 |
| Missing knowledge | 扩大或改变检索策略 |
| Ambiguous goal | 请求用户澄清 |
| Policy/risk boundary | 停止并请求批准 |
| Repeated dead end | 读取失败历史，改变方法而不是重复 |

### 4.5 Checkpoint 与可恢复性

长任务不能依赖完整聊天上下文。每个重要阶段应形成 checkpoint：

- goal 和当前状态；
- 已完成步骤；
- 关键 decision 及理由；
- 修改的资源；
- 已验证证据；
- 失败尝试；
- 未解决问题；
- 下一步。

Working Memory 保存执行状态，Durable Memory 只接收未来仍可能影响决策的经验。

---

## 5. AgentMate 如何连接四个平面

### 5.1 Capability Plane（Skill）

Git-backed Skill Registry 提供：

- GitHub/GitLab source 与 local snapshot；
- immutable package revision 和 release；
- active version 单一指针；
- compiled Skill Card 与 execution asset manifest；
- S0–S2 progressive disclosure（完整 package 仅通过 version files 列表与单资源取用暴露，无独立 S3 API）；
- 离线 deterministic lint / platform contract eval / release comparison；
- version-bound telemetry；
- 后续 PR/MR 演化工作流（未实现）。

Capability Plane 回答“这类问题应该怎样做”。

### 5.2 Knowledge Plane（KnowledgeBase）

Knowledge Registry 提供：

- git/local knowledge source 与根 `KNOWLEDGE.yaml` manifest；
- immutable source revision 与 canonical package hash；
- document snapshots 与 active 指针；
- K0 collection cards；
- format-aware Markdown chunking 与包内文档链接图；
- account-scoped hybrid 检索、K2 evidence 与 1-hop 邻居；
- 后续 knowledge build / persistent wiki / promotion（未实现）。

Knowledge Plane 回答“领域内什么是真的、证据在哪里”。它与 Memory 共享 retrieval 基础设施，但 identity、生命周期和 promotion 规则不同；Skill 通过 Knowledge Discovery Contract 在运行时发现 KB，而不是把知识打包进 Skill package。详见 [Skill + Knowledge Architecture v0.3](skill-knowledge-architecture-v0.1.md)。

### 5.3 Memory Plane

AgentMate Memory 提供：

- append-only event journal；
- semantic、episodic、procedural durable memory；
- evidence 关联与 supersede 数据模型（supersede 操作未实现）；
- PostgreSQL FTS + Qdrant semantic retrieval；
- checkpoint 事件类型（Working Memory session / resume 未实现）；
- 反馈信号表已就绪但尚未接线（未实现）。

Memory Plane 回答“以前发生过什么、学到了什么”。它有四个运行时角色：执行前 recall 注入经验、执行中 journal 留证、为 Skill 演化提供证据包、作为 KB 的 promotion 原料。

Memory 与 Knowledge 的关键区别是策展程度：Memory 门槛低、可快速积累、account 私有；Knowledge 门槛高、需要 citation 与审查、可共享。因此 Memory → KB 的 promotion 必须过使用信号门槛与审批，不能自动写入，否则会把幻觉复利化。

### 5.4 Control Plane（Agent Loop）

Agent Loop 使用：

- goal contract；
- task list 和 checkpoint；
- Skill search/select；
- Knowledge catalog/search（discover 未实现）；
- Memory recall/record；
- tool execution；
- success criteria verifier；
- reviewer/subagent；
- budget、approval 和 stop policy。

Control Plane 回答“此刻应该做什么以及是否已经完成”。

### 5.5 统一而不混同

四者共享 account、retrieval、telemetry 和反馈基础设施，但保留独立业务模型：

- Skill 不是 Knowledge；Skill 是经过版本管理的执行方法。
- Knowledge 不是 Skill；Knowledge 是有引用的领域事实，不执行动作。
- Memory 不是 Knowledge；Memory 是有证据的经历，未经审查不具备共享权威性。
- Loop 不是长期知识；Loop 是当前任务的控制状态。
- Retrieval 不是事实源；Retrieval 是把相关内容送入当前决策的机制。

---

## 6. 一个完整示例：实现 GitHub Skill 同步

假设目标是：

> 为 AgentMate 实现公共 GitHub repository Skill 同步；相同 package 并发重试必须幂等，不允许破坏 local snapshot。

### 6.1 Goal contract

```text
goal:
  Git source 能从 ref 解析 commit、下载 archive、提取 package 并创建 release

constraints:
  不新增 Git SDK 依赖
  不修改 local snapshot 语义
  仅支持 public github.com
  archive 解析必须有文件数和大小上限

success criteria:
  provider URL tests pass
  archive extraction tests pass
  concurrent integration test pass
  go test ./... and go vet ./... pass
  reviewer reports no blocker
```

### 6.2 三轴渐进式选择

Skill 轴：

1. S0 搜索到 `git-provider-sync`、`go-web-service`、`postgres-concurrency` 三个 Skill Card。
2. Coordinator 选择 `git-provider-sync` 作为主 Skill。
3. S1 加载 provider 工作流和 archive 安全边界。
4. S2 在实现 archive parser 时加载对应测试模板。
5. 完成审计时才读取 S3 完整 package/diff。

Knowledge 轴：

1. 按 Skill 声明的知识需求，K0 发现 `go-stdlib-reference` 与 `provider-api-notes` 两个 KB。
2. K1 定位到 `archive/tar` 与 GitHub archive endpoint 相关章节。
3. K2 只加载命中 chunk 正文与 citation，不拉整份文档。

Memory 轴：

1. M0 命中“snapshot alias 问题”“advisory lock key 编码”两条经验卡片。
2. M1 读取完整经验正文与适用条件。
3. 对与当前设计冲突的一条，M2 展开证据链确认它是否已被 supersede。

### 6.3 三轴合并后的 Context Pack

```text
[TASK]      goal contract 与 success criteria
[SKILL]     git-provider-sync S1 工作流 + archive 边界约束
[KNOWLEDGE] tar/gzip 与 provider archive 端点的 K2 evidence + citation
[MEMORY]    上次 review 的 snapshot alias 失败、用户“小批次并频繁汇报”的 correction
[FACTS]     当前 internal/skills 代码、immutable revision/version 数据库约束
```

这些内容来源不同、权威性不同，但都直接影响当前动作；带标签合并后，Agent 在冲突时知道以当前代码为准，而不是以文档或旧经验为准。

### 6.4 Loop

```text
Observe: 读取 source model 和 service wiring
Select: 选择 provider parser 子任务
Act: 实现 URL parser 和 tests
Verify: targeted tests pass
Checkpoint: 汇报第一小批完成

Observe: 读取 checkpoint
Act: 实现 commit resolution client
Verify: httptest 覆盖 GitHub responses
Checkpoint

Act: 实现 bounded archive extraction
Verify: traversal/symlink/size tests

Act: 接入 canonical ingest
Verify: PostgreSQL concurrency integration

Review: 独立 reviewer 检查 identity 和副作用
Stop: 所有 success criteria 有工具证据
```

这比一次要求 Agent“把 Git sync 全做完”更可靠，也比无边界启动十个 Agent 更容易审查。

---

## 7. Skill、Knowledge、Memory 和 Loop 如何共同学习

### 7.1 先记录事实，再提炼经验，最后才编译知识

每次执行产生事件：

```text
goal
selected_skill
selected_knowledge
retrieved_context
attempt
tool_result
verification
correction
outcome
```

事件是审计事实。三种沉淀路径的门槛依次升高：

```text
event（发生即记录）
  -> durable memory（可能影响未来决策，需 evidence）
  -> KB candidate page（反复被用且有价值，需 citation + lint + 审批）
```

例如：

> PostgreSQL advisory lock key 不能包含 NUL 字节；应使用长度前缀的文本编码。

该 memory 必须引用失败和修复证据，而不是只保存模型总结。只有当它被反复 recall 且反馈有用时，才提名进入 KB 成为团队级知识。

### 7.2 三维 telemetry

每次执行记录：

Skill 侧：

- 是否被检索和选择；
- 是否真正参与行动；
- 加载了哪些资源层级（S1/S2/S3）；
- 是否发生用户纠正。

Knowledge 侧：

- 发现了哪些 KB 候选、最终选中哪些；
- 命中哪些文档/chunk，是否被实际引用；
- 证据是否足够、是否过期。

Memory 侧：

- recall 了哪些经验、是否被采纳；
- 是否与当前事实冲突；
- 是否需要 supersede。

公共：任务是否成功、token/延迟/工具成本、哪个 verifier 证明完成。

这可以区分：

- 路由失败：正确 Skill 没被选中；
- 说明失败：选中了 Skill，但流程导致错误；
- 知识失败：缺少必要领域事实或引用错误；
- 记忆失败：重复踩坑，或采纳了已过期经验；
- 工具失败：方法正确但执行环境失败；
- 目标失败：success criteria 本身不清晰。

### 7.3 三维演化

三个维度都不在线直接修改 active 内容，但演化路径不同：

```text
Skill 演化
  telemetry + corrections + failed attempts
    -> compiler/evolver proposal -> lint + regression eval
    -> Git branch + PR/MR -> human review
    -> merge -> sync -> immutable release

Knowledge 演化
  new source revision 或 Memory promotion
    -> candidate knowledge build -> citation/contradiction lint
    -> human/policy approval -> active build

Memory 演化
  event -> evidence-backed entry
    -> 使用反馈 -> 冲突检测
    -> supersede 或提名进 KB
```

Git 继续是 Skill 与 KB 原始内容的事实源，AgentMate 负责发现问题、生成证据、提出修改并评估新版本。

### 7.4 防止 Slopacolypse

如果 Agent 可以低成本生成大量 Skill、知识页面、记忆和代码，系统必须提高进入 active 内容的门槛。三个维度各有门槛：

Skill：

- package provenance 与 ownership；
- lint 和 schema validation；
- 最小 eval suite；
- 明确适用/禁用条件；
- 与已有 Skill 的重复检测；
- active promotion policy 与可回滚 immutable release。

Knowledge：

- 每条 claim 必须有 citation 指回 source revision；
- contradiction 与 orphan lint；
- freshness 与来源覆盖；
- candidate build 需审批才能成为 active。

Memory：

- 必须有 source event 或 evidence；
- 不把猜测写成事实，保守置信度；
- 冲突时 supersede 而非堆叠；
- 只有反复被用且有价值才提名进 KB。

生成成本下降，不代表验证成本消失。高质量系统的价值将更多来自筛选、证据和生命周期管理。

---

## 8. 人与 Agent 的新分工

### 8.1 人负责 Macro

人更适合：

- 选择值得解决的问题；
- 定义系统目标和边界；
- 处理组织、产品和用户 trade-off；
- 判断风险和可接受成本；
- 审查概念正确性；
- 为高影响动作授权；
- 决定何时发布。

### 8.2 Agent 负责受约束的 Micro Loop

Agent 更适合：

- 搜索代码和知识；
- 执行机械修改；
- 生成测试和候选实现；
- 运行工具并归纳错误；
- 比较多个实现；
- 保持长时间尝试；
- 记录结构化进度；
- 在明确 verifier 下收敛。

### 8.3 IDE 和可观察性仍然重要

Agent 能力增强并没有消除 IDE、diff、日志、测试报告和人工审查。相反，因为错误更偏概念层，它们变得更重要。

AgentMate 应让用户看到：

- 当前 goal 和计划；
- Agent 选中了哪个 Skill、哪些 KB 证据、哪些历史经验；
- 每条上下文的来源标签与引用；
- 哪些是假设；
- 执行了哪些工具；
- 哪些验证已经通过；
- 哪些内容仍不确定；
- 为什么 Loop 继续或停止。

---

## 9. 评估指标

### 9.1 不能只测代码生成速度

Karpathy 指出，LLM 的影响不仅是原任务加速，也包括以前不值得做或不会做的任务现在变得可行。因此评估应同时考虑速度和能力边界扩张。

### 9.2 Agent Loop 指标

- task success rate；
- success criteria coverage；
- 首次验证通过率；
- 平均 loop 次数；
- 重复失败率；
- 用户纠正率；
- 未授权副作用率；
- blocked task recovery rate；
- 人工介入时间。

### 9.3 Skill 指标

- routing precision/recall；
- Skill 被选择后的成功率；
- silent bypass rate；
- 资源层级（S1/S2）加载分布；
- 每次成功的 token/tool 成本；
- version regression rate；
- correction-to-PR conversion；
- active promotion 与 rollback 次数。

### 9.4 Knowledge 指标

- K0 发现召回：任务相关 KB 是否进入候选；
- KB selection usefulness：选中的 KB 是否真的贡献了被使用的证据；
- K2 evidence 有用率与 retrieved-but-unused rate；
- missing-context failure rate；
- citation 覆盖率与引用正确性；
- stale/superseded 命中率与 freshness；
- 索引完整性（indexed / partial / failed 分布）；
- context token cost。

### 9.5 Memory 指标

- recall precision@k 与 useful recall；
- 相关经验被采纳率；
- 有害或过期 memory 命中率；
- evidence coverage（有源事件比例）；
- supersede 及时性；
- Memory → KB promotion 通过率与被拒原因分布。

### 9.6 三维联合归因

单一总分无法定位问题来源。失败必须能归因到具体维度：

| 失败表现 | 归因维度 |
|---|---|
| 正确 Skill 未被选中 | Skill routing |
| Skill 选对但流程导致错误 | Skill instructions |
| 缺少领域事实或引用错误 | Knowledge（发现或证据） |
| 重复踩已知的坑 | Memory recall |
| 经验已过期却仍被采纳 | Memory 生命周期 |
| 方法正确但环境失败 | Tool/环境 |
| success criteria 本身不清 | Goal contract |

因此每次执行的 outcome 应同时关联 `skill_version_id`、所用 KB build/document 与 memory entry IDs。

---

## 10. 实施路线

路线按三个维度并行推进，编号与各自设计文档一致。

注意：本节 K1–K5 与 M1–M3 是**实施里程碑编号**，与 §2.3 的**披露层级** K0–K3 / M0–M2 无关。

### Skill 维度（Phase 1–4 已实现）

- Phase 1 package identity：canonical package hash、immutable revision/release、active 唯一性；
- Phase 2 public Git sync：provider、ref→commit、bounded archive、错误恢复；
- Phase 3 compiled catalog 与渐进披露：deterministic Skill Card compiler、S0/S1/S2 API、compiled card 索引；
- Phase 4 离线 deterministic quality：package lint、platform contract eval、same-skill release comparison、version-bound telemetry；
- 后续：PR/MR 演化、human-approved promotion、DAG 组合（未实现）。

### Knowledge 维度（里程碑 K1–K2 已实现）

- K1 source 与 immutable identity：git/local knowledge source、`KNOWLEDGE.yaml`、source revision、document snapshots；
- K2 catalog 与检索：K0 cards、Markdown chunking、文档链接图、hybrid 检索、K2 evidence、1-hop 邻居、reindex；
- K3 compiler / persistent wiki：KnowledgeProfileVersion、candidate build、typed link graph、entity 锚点、synthesis pages、contradiction/orphan lint、promotion（未实现）；
- K4 Skill-driven dynamic discovery：Knowledge Discovery Contract、`knowledge_discover`、KnowledgeResolutionRun（未实现）；
- K5 企业扩展：private credentials、object storage、多格式 parser、external KB provider（未实现）。

### Memory 维度（基础已实现；里程碑 M1–M3 未实现）

- 已实现：append-only event journal、evidence-backed durable memory、hybrid recall（FTS + Qdrant）；supersede 仅数据模型、checkpoint 仅事件类型、使用反馈未接线；
- M1：`skill_logs` 与 memory events 按 `skill_version_id` + `session_id` 关联，使质量报告可链接证据正文（未实现）；
- M2：Context Pack API，一次调用返回带来源标签的多维最小上下文（未实现）；
- M3：Memory → KB promotion 管道、notes 作为个人 KB source（未实现）。

### Control 维度

- 已实现：goal contract 实践、task list、checkpoint、verifier 使用、独立 reviewer 流程；
- 未实现：verifier registry、approval/stop policy 引擎、三维联合归因 telemetry。

### 受控多 Agent（未实现）

- Coordinator/Worker/Reviewer contract；
- 可证明独立的并行任务；
- dependency DAG；
- shared checkpoint；
- duplicate-work suppression；
- cost-aware scheduling。

只有三条上下文轴都形成稳定数据和 verifier 后，多 Agent 才能从“更多并发生成”升级为“更可靠的并行执行”。

---

## 11. 最终观点

Karpathy 所描述的变化，不只是“AI 可以写更多代码”，而是软件工作的控制方式正在改变：

```text
过去：人执行步骤，工具响应命令
现在：人定义目标，Agent 执行循环，人审查证据
未来：组织维护目标、知识、能力和验证体系，Agent 在其中持续工作
```

模型智能只是其中一部分。一个可靠 Agent 系统还需要三条独立且可渐进披露的上下文轴，以及一个能停止的控制器：

- 可选择、可版本化、可评测的 Skill（怎么做）；
- 有引用、可审查、可重建的 Knowledge（什么是真的）；
- 有证据、可失效、可提名的 Memory（经历过什么）；
- 有成功标准和停止条件的 Loop；
- 真实工具反馈与可恢复 checkpoint；
- 独立验证和人工授权；
- 从失败与纠正中演化的机制。

三条轴混成一个"知识库"会同时丢掉三样东西：Skill 的版本治理、Knowledge 的引用可信度、Memory 的时效与证据链。

AgentMate 的定位可以浓缩为一句话：

> **把 Git 中可协作的能力、可引用的领域知识、有证据的执行记忆，以及工具环境中可验证的 Agent Loop 连接起来，让 LLM 从"会回答"升级为"能够持续、可靠地完成工作"。**
