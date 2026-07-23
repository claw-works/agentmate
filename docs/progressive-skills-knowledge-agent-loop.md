# 从“用英语编程”到可持续 Agent Loop：AgentMate 的渐进式 Skill 与知识架构

**版本**：v0.1
**日期**：2026-07-18
**状态**：DESIGN NOTE
**关联材料**：`docs/Karpathy.txt`、`docs/skill-registry-design-v0.1.md`、`docs/memory-design-v0.3.md`

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

AgentMate 对这些问题的回答是把 Agent 系统拆成三个相互独立但可以闭环的平面：

1. **Knowledge Plane**：提供事实、上下文、历史和证据。
2. **Capability Plane**：通过渐进式 Skill 描述“如何做”。
3. **Control Plane**：通过 Agent Loop 决定“下一步做什么、是否成功、何时停止”。

```text
User Goal + Success Criteria
             |
             v
       Agent Control Loop
 Observe -> Recall -> Select Skill -> Plan -> Act -> Verify
    ^                                               |
    |                                               v
 Memory / Evidence <- Reflect / Record <- Result / Feedback
             |
             v
   Skill & Knowledge Evolution
```

AgentMate 的目标不是创建一个装满提示词的仓库，而是建立一个可同步、可检索、可验证、可渐进加载和可演化的 Agent 能力系统。

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

## 2. 为什么 Skill 必须渐进式披露

### 2.1 Context 不是无限仓库

把所有 Skill、文档、历史和工具说明一次性放入上下文，会造成：

- token 成本快速增长；
- 任务无关信息干扰推理；
- 多个 Skill 的规则互相冲突；
- 模型难以判断哪些知识真正重要；
- 更新一个 Skill 就需要重新分发巨大提示；
- 无法观测“哪个 Skill 被选择、使用和证明有效”。

渐进式 Skill 的原则是：

> **先用最小信息完成路由，再按任务需要逐层加载；不要在 Agent 尚未选择能力前就支付完整上下文成本。**

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

> 边界更新（2026-07-23）：Skill package 中的资源定位为 **execution assets**。独立领域知识语料（产品文档、政策、事实库、持久化 wiki）属于未来独立 Knowledge Registry，Skill 通过 Knowledge Discovery Contract 在运行时发现并引用，而不是打包进 Skill；见 [Skill + Knowledge Architecture v0.2](skill-knowledge-architecture-v0.1.md)。`references/` 仅保留与执行流程强绑定的紧凑规范摘要。

根 `SKILL.md` 只负责说明：

- 何时使用；
- 何时不要使用；
- 输入与输出；
- 核心工作流；
- 资源导航；
- 验证要求。

真正复杂的知识、模板和执行资源在被需要时再加载。

### 2.3 五级披露模型

```text
L0  Skill Card
    name, purpose, triggers, constraints, cost, confidence

L1  Core Instructions
    SKILL.md 的核心流程和决策边界

L2  Task-specific References
    与当前执行流程直接相关的规范摘要、示例和 schema；
    独立领域知识语料走 Knowledge Registry 的 K0/K1/K2 轴

L3  Executable Resources
    scripts、templates、tests、tool definitions

L4  Full Package / Source
    完整文件清单、immutable revision、Git provenance
```

#### L0：路由

Agent 先搜索轻量 Skill Card，而不是加载完整 Skill。Card 应回答：

- 这个 Skill 能解决什么问题？
- 触发条件是什么？
- 禁止或不适用条件是什么？
- 需要哪些工具和权限？
- 预计成本和风险是什么？
- 当前 active version 和验证状态是什么？

#### L1：执行方法

只有 Skill 被选中后，Agent 才加载核心说明。此时信息仍应保持紧凑，重点是流程、约束和验证，而不是百科全书。

#### L2：执行相关规范摘要

Agent 根据任务中的实体、错误、代码模块和阶段，选择性加载与执行流程强绑定的规范摘要。比如 Git 同步任务只需要 provider 同步规范摘要、archive 边界和 package identity 约束，不需要加载未来的 eval/compiler 全部设计。独立领域知识语料（完整 API 文档、产品手册、事实库）走 Knowledge Registry 的 K0/K1/K2 轴。

#### L3：行动资源

当 Loop 决定执行具体动作时，再加载模板、脚本、测试和工具 schema。

#### L4：完整来源

完整 package 主要用于：

- 审计；
- 执行器获取二进制或脚本；
- compiler/linter；
- 版本比较；
- 回滚与复现。

普通路由不应使用 L4。

---

## 3. LLM 知识库不是“把所有内容向量化”

### 3.1 五种上下文来源

Agent 执行任务时会使用五类不同信息：

| 类型 | 回答的问题 | AgentMate 载体 |
|---|---|---|
| Parametric Knowledge | 模型通常知道什么 | 基础模型 |
| Source Facts | 当前代码、文档和数据实际上是什么 | Git、PostgreSQL、业务 API（规划：Knowledge Registry） |
| Skills | 这类任务应该如何执行 | Git-backed Skill Registry |
| Durable Memory | 过去学到了哪些可复用经验 | Memory entries + evidence |
| Working Memory | 当前任务进行到哪里 | events + checkpoints |

它们不能被混成同一个向量集合。事实、方法、长期经验和当前执行状态拥有不同生命周期、权威性和更新规则。

### 3.2 PostgreSQL 是事实源，向量索引是派生物

AgentMate 的原则是：

- PostgreSQL 保存可审计事实、关系、状态和证据；
- Git 保存 Skill package 和代码内容；
- Qdrant 保存可重建的 embedding 索引；
- 删除向量索引不会丢失 registry 或 memory；
- 检索结果必须能够返回来源和命中原因。

这避免把“向量相似”错误地当作“事实正确”。

### 3.3 知识库的核心是 Context Compilation

一个好的 LLM 知识库不是简单执行 top-k，而是为当前 Loop 编译最小充分上下文：

```text
Task
  -> exact filters: account / repository / skill / entity / status
  -> lexical search: identifiers / errors / paths / symbols
  -> semantic search: concepts / intent / analogous experience
  -> temporal & lifecycle filters: active / superseded / recent
  -> evidence ranking: authority / confidence / usefulness
  -> deduplicate & budget
  -> Context Pack
```

Context Pack 应包含：

- 当前目标和成功标准；
- 已选 Skill Card 与核心约束；
- 少量直接相关事实；
- 已知失败尝试和用户纠正；
- 下一动作需要的工具 schema；
- 每条关键信息的来源。

### 3.4 Consult before write

Agent 形成新记忆或发布新 Skill 前，应先检索是否已有等价内容：

- 防止重复 memory；
- 防止把同一 package 复制成多个 release；
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
   检索 source facts、相关 Skill、memory 和失败历史

3. Select
   选择最小能力集合；必要时组合 Skill

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

## 5. AgentMate 如何连接三者

### 5.1 Knowledge Plane

AgentMate Memory 提供：

- append-only event journal；
- semantic、episodic、procedural durable memory；
- evidence 与 supersede 生命周期；
- PostgreSQL FTS + Qdrant semantic retrieval；
- Working Memory checkpoint；
- 使用、忽略、纠正和有害反馈。

Knowledge Plane 回答“当前真实状态是什么”和“以前发生过什么”。

Knowledge Plane 未来将扩展为两个子域：**Memory**（执行经验与证据，当前已实现）与 **Knowledge Registry**（领域事实语料与持久化 wiki builds，规划中）。二者共享 retrieval 基础设施，但 identity、生命周期和 promotion 规则不同；Skill 通过 Knowledge Discovery Contract 在运行时发现 KB，而不是把知识打包进 Skill package。详见 [Skill + Knowledge Architecture v0.2](skill-knowledge-architecture-v0.1.md)。

### 5.2 Capability Plane

Git-backed Skill Registry 提供：

- GitHub/GitLab source；
- immutable package revision 和 release；
- active version；
- Skill Card 与资源 manifest；
- progressive disclosure；
- package lint、tests 和 eval metadata；
- 后续 PR/MR 演化工作流。

Capability Plane 回答“这类问题应该怎样做”。

### 5.3 Control Plane

Agent Loop 使用：

- goal contract；
- task list 和 checkpoint；
- Skill search/select；
- tool execution；
- success criteria verifier；
- reviewer/subagent；
- budget、approval 和 stop policy。

Control Plane 回答“此刻应该做什么以及是否已经完成”。

### 5.4 统一而不混同

三者共享 account、retrieval、telemetry 和反馈基础设施，但保留独立业务模型：

- Skill 不是 Memory；Skill 是经过版本管理的执行方法。
- Memory 不是 Skill；Memory 是有证据的事实或经验。
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

### 6.2 Progressive Skill selection

1. L0 搜索到 `git-provider-sync`、`go-web-service`、`postgres-concurrency` 三个 Skill Card。
2. Coordinator 选择 `git-provider-sync` 作为主 Skill。
3. L1 加载 provider 工作流和 archive 安全边界。
4. L2 只加载 provider 同步规范摘要与现有 package identity 设计。
5. L3 在实现 archive parser 时加载对应测试模板。
6. 完成审计时才读取 L4 完整 package/diff。

### 6.3 Knowledge recall

Context Compiler 同时检索：

- 当前 `internal/skills` 代码；
- immutable revision/version 数据库约束；
- 上次 review 发现的 snapshot alias 问题；
- Go 标准库 tar/gzip API；
- 用户要求“小批次实现并频繁汇报”的 correction。

这些内容来源不同，但都直接影响当前动作。

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

## 7. Skill、Knowledge 和 Loop 如何共同学习

### 7.1 先记录事实，再提炼经验

每次执行产生事件：

```text
goal
selected_skill
retrieved_context
attempt
tool_result
verification
correction
outcome
```

事件是审计事实。系统可以从多次事件中提炼 durable memory，例如：

> PostgreSQL advisory lock key 不能包含 NUL 字节；应使用长度前缀的文本编码。

该 memory 必须引用失败和修复证据，而不是只保存模型总结。

### 7.2 Skill telemetry

每次 Skill 使用记录：

- 是否被检索和选择；
- 是否真正参与行动；
- 任务是否成功；
- 是否发生用户纠正；
- 加载了哪些资源层级；
- token、延迟和工具成本；
- 哪个 verifier 证明完成。

这可以区分：

- 路由失败：正确 Skill 没被选中；
- 说明失败：选中了 Skill，但流程导致错误；
- 知识失败：缺少必要事实；
- 工具失败：方法正确但执行环境失败；
- 目标失败：success criteria 本身不清晰。

### 7.3 Skill 演化

AgentMate 不直接在线修改 active Skill。演化流程应是：

```text
telemetry + corrections + failed attempts
              |
              v
      compiler/evolver proposal
              |
      lint + regression eval
              |
              v
       Git branch + PR/MR
              |
         human review
              |
              v
    merge -> sync -> immutable release
```

Git 继续是内容事实源，AgentMate 负责发现问题、生成证据、提出修改并评估新版本。

### 7.4 防止 Slopacolypse

如果 Agent 可以低成本生成大量 Skill、文档和代码，系统必须提高进入 active catalog 的门槛：

- package provenance；
- ownership；
- lint 和 schema validation；
- 最小 eval suite；
- 明确适用/禁用条件；
- 与已有 Skill 的重复检测；
- 真实使用反馈；
- active promotion policy；
- 可回滚 immutable release。

生成成本下降，不代表验证成本消失。高质量 registry 的价值将更多来自筛选、证据和生命周期管理。

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
- Agent 使用了哪些 Skill 和知识；
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

### 9.3 Knowledge 指标

- precision@k / useful recall；
- retrieved-but-unused rate；
- missing-context failure rate；
- stale/superseded 命中率；
- evidence coverage；
- context token cost；
- memory harmful feedback rate。

### 9.4 Skill 指标

- routing precision/recall；
- Skill 被选择后的成功率；
- silent bypass rate；
- 资源层级加载分布；
- 每次成功的 token/tool 成本；
- version regression rate；
- correction-to-PR conversion；
- active promotion 与 rollback 次数。

---

## 10. 实施路线

### Phase 1：可验证基础

- Git-backed immutable Skill Registry；
- package identity；
- local/Git source sync；
- active version；
- event journal 和 evidence-backed memory；
- targeted/full/integration verifier；
- checkpoint。

### Phase 2：渐进式能力目录

- Skill Card compiler；
- trigger/capability/constraint schema；
- resource manifest；
- L0/L1/L2 context API；
- Skill search/select telemetry；
- MCP resource fetch。

### Phase 3：Context Compiler 与 Working Loop

- exact + lexical + semantic + temporal fusion；
- token-budgeted Context Pack；
- Working Memory checkpoint/resume；
- failed-attempt precheck；
- verifier registry；
- approval/stop policies。

### Phase 4：Eval 与演化闭环

- Skill lint；
- regression eval；
- route/usage feedback；
- compiler/evolver proposal；
- GitHub PR / GitLab MR；
- human-approved release promotion。

### Phase 5：受控多 Agent

- Coordinator/Worker/Reviewer contract；
- 可证明独立的并行任务；
- dependency DAG；
- shared checkpoint；
- duplicate-work suppression；
- cost-aware scheduling。

只有前三个阶段形成稳定数据和 verifier 后，多 Agent 才能从“更多并发生成”升级为“更可靠的并行执行”。

---

## 11. 最终观点

Karpathy 所描述的变化，不只是“AI 可以写更多代码”，而是软件工作的控制方式正在改变：

```text
过去：人执行步骤，工具响应命令
现在：人定义目标，Agent 执行循环，人审查证据
未来：组织维护目标、知识、能力和验证体系，Agent 在其中持续工作
```

模型智能只是其中一部分。一个可靠 Agent 系统还需要：

- 正确且可追溯的知识；
- 可选择、可版本化的 Skill；
- 有成功标准和停止条件的 Loop；
- 真实工具反馈；
- 可恢复 checkpoint；
- 独立验证和人工授权；
- 从失败与纠正中演化的机制。

AgentMate 的定位可以浓缩为一句话：

> **把 Git 中可协作的能力、知识库中有证据的上下文，以及工具环境中可验证的 Agent Loop 连接起来，让 LLM 从“会回答”升级为“能够持续、可靠地完成工作”。**
