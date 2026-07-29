# Knowledge Wiki Compiler（K3）设计 v0.1

**状态**：K3.1 + K3.2 + K3.3 已实现并在真实环境验证（migration 000027/000028，`internal/llm`、
`internal/knowledge/wiki_*.go`）。K3.4–K3.9 仍是 DESIGN。
**前置**：K1（source/revision/document）、K2（catalog/chunk/link/hybrid 检索）均已实现
**上层背景**：`skill-knowledge-architecture-v0.1.md` §13–§15 与其中的 K3 里程碑清单
**实现状态明细**：见 §13

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

## 2. 已定的三个决策

### 2.1 平台侧编译，不是 agent 侧回写

**决策**：wiki 由服务端编译，产物存在 AgentMate 内部，不提交回 Git。

理由：agent 运行在客户端，行为不可控。让它写事实源等于把数据质量外包给一个无法约束的
进程——版本、prompt、模型都可能不同，产物无法审计也无法复现追溯。

代价与后续：Git 不再是 wiki 层的事实源（raw 层仍然是）。因此**必须**把出处建模到位，
否则 wiki 就成了一堆来源不明的文本。基于 Git 的 wiki 导出/PR 审批留在 K5。

由此确定的写入边界：**客户端 agent 可以写 raw candidate 与 memory event，不能写
wiki page 与 KB 事实。** agent 提议，平台收编。

### 2.2 目录按领域 / 主题两级

已在 `000022` 与 `internal/pkgpath` 落地，K3 直接复用：`platform/retrieval` 的 domain 是
`platform`，source name 是 `platform-retrieval`。每个二级目录是一个独立 KB package，
也就是一个独立 wiki 与一份独立 `index`。

### 2.3 build 自动激活，不设人工审批门

**决策**：build 通过 check 后自动激活。不存在"等人点通过"的环节。

否决人工审批的理由是身份错配：知识库是用户的，但 wiki 的质量标准是平台的。用户不知道
citation 该锚到什么粒度、entity page 该在什么时候拆出来。让 SaaS 的普通用户审这个，
结果只有两种——无脑点通过（门是假的），或者卡住不动（wiki 永不更新）。

取而代之的是三层质量模型，见 §7。

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
  ├─ 1:N KnowledgeBuildEvent           → 渲染 log 的结构化来源
  └─ 1:N KnowledgeReviewFinding        → 忠实性审阅结果（不阻塞激活）

KnowledgeValidationSignal                      ← K3 新增（跨域：人类隐性行为信号）
  └─ 归因锚点 → KnowledgeResolutionRun (K4) / session + skill_version (M1)
```

### 4.1 KnowledgeProfileVersion

`KNOWLEDGE.yaml` 里的 `profile: platform-wiki-v1` 目前只是一个字符串。K3 把它变成
带版本的声明式约定，规定：

- 允许哪些 page kind，各自的 frontmatter 必填字段；
- 页面命名与路径规则；
- 允许哪些 link 类型；
- citation 强制程度（对应已有的 `citation_policy`）；
- 单 build 的 page 数上限、单 page 长度上限；
- 编译工作流提示（哪些内容优先抽成 entity page 等）；
- check 阈值（page 数相对 parent build 的允许波动范围等，见 §7.1）；
- review 标准版本（见 §7.2）。

profile 版本化的原因和 prompt 版本化一样：它会影响输出，因此必须成为出处的一部分。

### 4.2 KnowledgeBuildRevision

| 字段 | 说明 |
|---|---|
| `account_id`, `source_id` | 账号作用域 |
| `source_revision_id` | 编译所基于的 raw 快照 |
| `raw_package_hash` | 冗余存一份，raw 侧被清理后仍可追溯 |
| `profile_version_id` | 页面约定版本 |
| `compiler_version` | 编译流程实现版本 |
| `model`, `prompt_version` | 编译侧 LLM 出处 |
| `reviewer_model`, `reviewer_prompt_version` | review 侧 LLM 出处；必须与编译侧异构（§7.2） |
| `check_status` | `passed` / `failed`；唯一的门禁结果 |
| `review_status` | `clean` / `flagged` / `skipped`；不影响激活 |
| `parent_build_id` | 增量编译的基线，全量编译为 NULL |
| `mode` | `full` / `incremental` |
| `status` | `queued` / `running` / `succeeded` / `failed` / `cancelled` |
| `pages_written`, `pages_reused` | 增量编译的实际收益 |
| `input_tokens`, `output_tokens`, `cost_micros` | 编译成本 |
| `review_tokens`, `review_cost_micros` | review 成本，与编译分开记账（§8） |
| `error` | 失败原因 |
| `started_at`, `finished_at` | 时长 |

`active_build_id` 只能指向 `succeeded` 的 build。失败的 build 保留用于诊断，不影响读路径。
`review_status = flagged` **不阻止**激活——review 不可重现，不能进入阻塞路径（§7.2）。

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

### 4.7 KnowledgeReviewFinding

review 的输出（§7.2）。每条锚到一个 page 与一条 citation，记录断言、被引原文、
判定（`supported` / `unsupported` / `overstated` / `conflated`）与理由。

`reviewer_model` 与 `reviewer_prompt_version` 存在 build 上而非每条 finding 上，因为
一次 review 用同一套配置。findings 保留而不覆盖：review 标准演进后要能对比同一 page
在不同标准下的判定。

### 4.8 KnowledgeValidationSignal

validation 的落表（§7.3）。这是**跨域**的表——信号来自 skill 执行与检索使用，不属于
knowledge 域独有，因此按 `(account_id, subject_type, subject_id)` 建模，`subject_type`
可以是 page、citation、build 或 source。

| 字段 | 说明 |
|---|---|
| `signal_type` | `adopted` / `refollowed` / `requeried` / `rewritten` / `abandoned` 等 |
| `polarity` | 正向 / 负向；由类型决定，冗余存便于聚合 |
| `resolution_run_id` | 归因锚点，指向 KnowledgeResolutionRun（K4） |
| `session_id`, `skill_version_id` | 归因锚点（M1） |
| `observed_at` | 信号发生时间，不是写入时间 |

没有 `resolution_run_id` 与 session/skill 关联的信号**无法归因**，只能用于粗粒度趋势
统计。这就是 §7.4 说 K4 与 M1 是闭环必要条件的具体含义。

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

## 7. 质量：check / review / validation

三个层次，三种性质，不能混为一谈。混淆的直接后果是把不可重现的判断放到阻塞路径上，
那种系统无法运维。

| 层 | 谁做 | 性质 | 时机 | 是否阻塞 |
|---|---|---|---|---|
| **check** | 平台，确定性代码 | 客观、可重现、可解释 | build 时 | **是** |
| **review** | 平台的 agent（异构模型） | 主观、不可重现 | build 时 | 否，只标记 |
| **validation** | 人类，**隐式** | 真实但有噪声 | 使用中，持续 | 否 |

### 7.1 check：唯一的门禁

全部是计数与 SQL 能判定的不变量，不含任何模型判断：

- citation 指向的 raw document 真实存在；
- `citation_policy: required` 下不存在无 citation 的事实性断言；
- page 数相对 parent build 未异常暴涨或暴跌（阈值来自 profile）；
- lint 未新增矛盾或孤立页；
- 成本未超 profile 上限；
- `index` 覆盖全部 page；
- 全部 link 在 build 内闭合。

不通过即 build `failed`，不写入半成品。**这一层拦掉的问题比 review 多**，因为绝大多数
真正该拦的错误都是结构性的，不是语义性的。

### 7.2 review：忠实性，只标记不阻塞

review 只回答一个问题：**page 的断言是否忠实于它引用的原文**。有没有把"通常"写成
"总是"，有没有引入 citation 里不存在的因果关系，有没有把两个来源的结论错误地合并。

这件事可行的依据是判别比生成容易——给定断言与原文判断"原文是否支持它"，比写出这个
page 简单得多。

三条硬性要求：

1. **必须用异构模型**：与编译所用模型来自不同供应商/家族，并记入出处（`reviewer_model`、
   `reviewer_prompt_version`）。同模型自审会共谋——生成时犯的错，审阅时用同样的先验
   看不出来。
2. **只审变更的 page**：复用增量编译已算出的影响面，控制成本。
3. **不阻塞**：review 结论不可重现，若让它有权阻塞，同一个 build 重试两次会得到不同
   结果。它的作用是标记与度量，不是把关。

需要澄清一点：异构模型**降低**共谋概率，但主流模型训练语料高度重叠，先验相似，它们会
在同一些地方一起犯错。模型多样性不产生"公正"。真正的独立性来自两个非模型锚点——
check（完全不依赖模型判断）与 validation（产生于系统之外）。review 是第三道保险，
不是正确性的来源。

### 7.3 validation：人类的隐性确认

人不做审批动作，人的**行为**定义成功标准。使用过程本身就是判决：

```text
正向：直接采纳答案、点开 citation 后不再追问、同一 KB 被反复用于同类问题
负向：追问同一问题、换措辞重查、点开 citation 后立刻换查询、
      拿到答案后自己动手改写、某个 KB 注册后再也没被检索过
```

零成本、全量覆盖、反映真实满意度而非评分表上的满意度。

但有三个固有缺陷，必须设计进去而不是假装不存在：

- **有偏**：只有活跃用户产生信号。沉默不等于满意，也可能是已经放弃——而放弃的用户
  恰好最该被听到。
- **滞后**：一个变差的 build 可能几周后才在信号上显形，这段时间它一直在服务。
- **稀疏**：小账号与冷门 KB 几乎没有信号，但同样需要质量保障。

因此 validation 是**长期质量的度量**，不能当作**单次 build 的门禁**。门禁只能是 check。

### 7.4 归因：闭环真正的难点

信号能收集不等于能行动。用户追问了同一个问题，可能是：

```text
wiki 那一页综合错了 / 检索没召回到对的页 /
skill 的做法本身不对 / raw source 里根本没有这个事实
```

四种可能，四种完全不同的修法。归因不了，就只能得到"这个账号不太满意"这类没有行动
价值的结论。

归因依赖已在路线图上的两项能力，**它们的定位需要修正**：

| 能力 | 原定位 | 实际定位 |
|---|---|---|
| KnowledgeResolutionRun（K4） | 权限审计与复现 | **进化闭环的必要条件** |
| `skill_version_id` + `session_id` 关联（M1） | 质量遥测 | **进化闭环的必要条件** |

有了它们，一次不满意可以回溯到"`platform-retrieval` 的 build 7 的 `cjk-lexical` 页第 3 条
citation 被采用，随后用户追问"。没有它们，validation 信号无法归因，闭环断在这里。

这条依赖会改变优先级：K4 与 M1 不再是"完善功能"。

### 7.5 架构级不变量：proposal，不是 mutation

信号驱动 proposal，不驱动 mutation。

**可以全自动：**

- 从信号检测异常（某 KB 追问率上升、某 page 的 citation 从未被点开）；
- 生成 proposal：重编译某页、补充某类来源、把反复被提及的概念抽成 entity page、
  标记某条 memory 已具备晋升条件。

**不能自动：**

- 直接改写既有 page——会切断出处链，而出处是 wiki 唯一的可信性来源；
- 调整 review 的通过门槛——标准会漂移，系统慢慢接受更差的东西，而每一步都"符合当前
  标准"；
- 把 memory 自动晋升为 KB 事实。

区分可以再收紧一层：**可以自动进化的是发现问题的能力**（新增 check 项、新增 review
维度、把踩过的坑变成规则），**不可自动进化的是通过的门槛**。review 标准与 check 阈值
都要显式版本化，可人工演进，不可由系统自行放松。

这条不变量在本架构中已出现三次——query 回填只产 proposal、memory 晋升需过门槛与审批、
信号不驱动 mutation。它是架构级约定，见 `skill-knowledge-architecture-v0.1.md`。

### 7.6 完整闭环

```text
客户端 agent      产生/寻找 raw candidate、写 memory event（可提议，不可写事实源）
      ↓
平台 collect      raw source 入库，immutable
      ↓
平台 compile      wiki build（异构模型）
      ↓
check             机械不变量 → 不通过即 failed（阻塞）
review            异构模型审忠实性 → 只标记（不阻塞）
      ↓
自动激活          可 diff、可回滚
      ↓
validation        人类隐性行为信号（持续、有噪声、滞后）
      ↓
归因              ResolutionRun + skill/session 关联 → 定位到具体 page/citation
      ↓
proposal          重编译 / 补来源 / 抽 entity / memory 晋升候选
      ↓
（回到 collect 或 compile；人可否决；标准不自动放松）
```

## 8. 成本控制

LLM 编译与 review 是本设计唯一的显著变动成本，必须在设计里就有闸门：

1. **增量复用**：只重编译受影响的 page（§5.1 步骤 3）；review 同样只覆盖这批 page。
2. **单 build 上限**：page 数、token 数，来自 profile。超限即中止并报告。
3. **账号预算**：周期性 token / 金额上限，超出后拒绝新 build 而不是静默降质。
4. **成本可见**：编译与 review 的 token 与费用分开记账，可按 source / 时间聚合——
   两者会用不同供应商，混在一起无法判断哪部分在涨。

## 9. API 草案

```
POST /api/knowledge/sources/:id/builds        触发编译（body: mode, force）→ 返回 job/build
GET  /api/knowledge/sources/:id/builds        列出 build 历史（含成本与出处）
GET  /api/knowledge/builds/:id               build 详情
GET  /api/knowledge/builds/:id/pages         页面列表（不含正文）
GET  /api/knowledge/builds/:id/pages/*path   单页正文 + citation + link
GET  /api/knowledge/builds/:id/diff?from=    与另一个 build 的页面级 diff
POST /api/knowledge/builds/:id/activate      切换 active 指针（回滚也走这里）
GET  /api/knowledge/builds/:id/findings      review findings（不影响激活）
POST /api/knowledge/sources/:id/lint         发起 lint（异步）
GET  /api/knowledge/lint/:id                 lint 结果
POST /api/knowledge/wiki/search              检索 wiki page（namespace knowledge_wiki）
```

```
POST /api/knowledge/signals                  上报 validation 信号（带 resolution_run_id）
GET  /api/knowledge/proposals                待处置的 proposal
POST /api/knowledge/proposals/:id/decide     接受或否决
```

MCP 侧对应新增 `knowledge_wiki_search`、`knowledge_page_get`、`knowledge_build_status`、
`knowledge_lint_run`、`knowledge_proposals_list`。scope 沿用 `knowledge:r` / `knowledge:rw`；
触发编译与处置 proposal 属于 `rw`。

signal 上报刻意做成显式 API 而不是服务端推断：信号来自客户端 agent 的交互过程，
服务端只能看到检索请求，看不到"用户采纳了还是自己改写了"。但**信号只驱动 proposal**
（§7.5），所以客户端能上报信号不违反 §2.1 的写入边界。

## 10. 待决策（需要产品输入）

1. **编译与 review 各用哪个模型**？质量与成本的直接权衡。两者必须异构（§7.2），且都是
   出处的一部分——换模型不会自动重编译历史 build，历史判定也不会重算。
2. **编译触发时机**：同步后自动，还是显式触发？自动更符合"保持更新"，但会把成本
   与 raw 提交频率绑死。
3. **保留多少历史 build**？全部保留会持续增长；按数量或时间窗淘汰要考虑它是客户数据。
   注意 build 是回滚的依据（§7.3 的滞后性意味着问题可能几周后才发现），淘汰窗口不能
   短于 validation 信号的显形周期。
4. **proposal 的处置人**是谁。proposal 不自动生效（§7.5），那么由谁批、在哪批、
   多久不处理就过期。这条同时覆盖 query 回填与 memory 晋升候选。

已决（不再是待决策项）：**build 激活策略**——通过 check 后自动激活，不设人工审批门，
理由见 §2.3，替代方案见 §7。

## 11. 验收标准

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

质量三层各自的验收：

- **check 是真门禁**：人为构造一个 citation 指向不存在 document 的 build，必须 `failed`
  且不产生任何可见 page；page 数从 30 骤降到 3 的 build 必须被阈值拦下。
- **review 不阻塞**：`review_status = flagged` 的 build 仍能激活，且 findings 可查。
- **review 是异构的**：`reviewer_model` 与 `model` 不同供应商，两者都记在 build 上。
- **回滚可用**：激活旧 build 后读路径立即回到旧内容，新 build 保留可查。
- **归因可用**：一条负向信号能回溯到具体 build / page / citation；缺 `resolution_run_id`
  的信号被明确标记为不可归因，而不是静默计入统计。
- **proposal 不 mutate**：proposal 生成后，在被处置前 wiki 内容与 active 指针零变化。

## 12. 实施顺序

```
K3.1  profile 版本化 + build/page/citation/link 数据模型 + 全量编译（同步，小语料）
K3.2  check（机械不变量，唯一门禁）+ 自动激活 + build diff/回滚
K3.3  异步 job（租约 + 心跳 + 幂等 + 成本记账）
K3.4  增量编译（raw diff → 影响面 → 复用）
K3.5  index / log 生成
K3.6  wiki page 进检索（新 namespace）+ 两级 query
K3.7  lint
K3.8  review（异构模型忠实性审阅，只标记）
K3.9  validation 信号上报 + 归因 + proposal 生成与处置
```

K3.2 提到很前面是刻意的：**自动激活必须与 check 同时落地**。先有自动激活而没有机械门禁，
等于无条件接受一切编译输出；而 diff/回滚是这个决定的安全网，不能推后。

K3.1–K3.3 之后就有可用的东西；K3.6 之后 agent 才真正受益。lint 排在检索之后，
因为它的价值依赖 wiki 已经在被使用。

review（K3.8）排在 lint 之后：它成本高、不阻塞、且只有在有真实使用后才知道该重点审什么。

K3.9 依赖 K4 的 KnowledgeResolutionRun 与 M1 的 session/skill 关联——没有归因锚点的信号
无法行动（§7.4）。因此 **K3.9 实际上排在 K4 与 M1 之后**，本文档列出它是为了说明闭环
的终点，不代表它能在 K3 阶段独立完成。

K4（Skill-driven discovery）依赖 K3.6 就位——discovery 要选的是 wiki build，不是 raw chunk。


## 13. 实现状态与真实环境验证结果

### 13.1 已实现（K3.1 + K3.2）

| 部件 | 位置 |
|---|---|
| 对话模型客户端（compiler / reviewer 双角色） | `internal/llm/` |
| profile 版本化、build/page/citation/link/event 表 | `migrations/000027_create_knowledge_wiki.*.sql` |
| 领域模型与错误语义 | `internal/knowledge/wiki_model.go` |
| 全量编译（单次调用 + 输出规范化） | `internal/knowledge/wiki_compile.go` |
| check（12 条机械不变量，唯一门禁） | `internal/knowledge/wiki_check.go` |
| 编排（幂等复用 / 自动激活 / diff / 回滚） | `internal/knowledge/wiki_service.go` |
| 持久化 | `internal/knowledge/wiki_repo.go` |
| REST | `internal/knowledge/wiki_handler.go` |
| MCP（8 个工具） | `internal/knowledge/wiki_mcp.go` |
| 集成测试（9 个，脚本化模型驱动） | `internal/knowledge/wiki_integration_test.go` |

K3.3（带租约的异步 job、有界重试、成本记账）见 §14，位置在
`migrations/000028_add_knowledge_build_lease.*.sql` 与 `internal/knowledge/wiki_worker.go`。

未实现：K3.4（增量）、K3.6（wiki 进检索）、K3.7（lint）、K3.8（review 实际调用）、
K3.9（validation 与 proposal）。`review_status` 恒为 `skipped`，
`reviewer_*` 字段只记配置不记判定。

### 13.2 真实环境验证（本地 Docker + DashScope qwen3.7-plus + GitHub demo repo）

编译两个真实 KB（`claw-works/agentmate-demo-wiki` 的 `platform/registry` 与
`product/support`），全部通过：

- **平台生成的 index 覆盖全部内容页**：registry 12/12，support 10/10；
- **citation 全部落到真实 raw document**：registry 4 篇 raw 上 40+ 条 citation
  `document_id` 全部非空，support 同样 0 条悬空；
- **link 全部闭合**，出入双向可读，`references` / `mentions_entity` / `elaborates` 均被真实使用；
- **log 可 grep**：`grep '^## \['` 命中 15 行 = 15 条 event；
- **自动激活生效**，`knowledge_sources.active_build_id` 随之移动；
- **幂等复用**：同输入身份二次触发返回同一 build 且不调用模型（集成测试断言调用计数）；
- **diff/回滚**：激活旧 build 后读路径立即回到旧内容，`previous_build_id` 让回滚本身可撤销；
- **same_provider 警告出现在每次 build 的 warnings 里**，不需要翻配置才知道审阅不独立。

同时验证了一个**设计预期的负向结果**：`product/support/raw/limitations.md` 里的过期声明
没有产出 `contradicts` link。这是对的——矛盾的另一端在 `platform/retrieval` 这个**另一个
package**，而 K3 按 package 编译，跨库矛盾属于 K5。§11 埋这个检验点正是为了确认边界，
而不是为了让 K3 报出来。

### 13.3 真实环境暴露的四个问题（均已修）

1. **输出预算不是按 wiki 体积估，而是按"推理模型会先花掉多少"估。**
   4096 立刻截断；16384 在一个 6 KB 语料上截断，而另一个更大的语料只用了 13k。
   原因是 qwen3.7-plus 的思考 token 与正文共用 `max_tokens`，同一份输入两次的开销
   差一倍以上。默认值最终定在 32768。截断被客户端识别为**失败**而不是当作完整回复解析——
   否则会静默丢页。这条同时是"单次调用有硬上限、K3.4 增量不是优化而是必需"的实测依据。

2. **客户端断开会留下永久 `running` 的 build。**
   最初终态写入沿用请求 context，而同步编译最常见的失败恰恰是调用方等不及先挂断，
   于是终态写入也一起被取消。改为 `context.WithoutCancel` + 独立 15s 超时，并在
   `ctx.Err() != nil` 时记为 `cancelled`。一条永远撒谎的记录比一条写着"取消"的记录更糟。
   实测：20s 超时的请求现在留下 `cancelled` 而不是 `running`。

3. **模型会去写 `wiki/index.md`，撞上平台生成的同名页，整个 build 被 `path_unique` 判死。**
   这是可预见的模型行为，让它每次都失败等于系统不可用。改为保留这两个路径、丢弃模型
   在该路径上的产出，并把每次丢弃记为 `page_rejected` event——丢弃是可审计的，不是隐形的。
   同时 `CompilerVersion`/`PromptVersion` 一并升版，因为出处必须反映这次改动。

4. **模型页全被丢弃时不能产出空 wiki**，否则会得到一个"成功但什么都没有"的 build。
   现在报 `no usable pages` 并记为 failed。

另外两条**未修、已记录**的观察：

- **provider 偶发 `unexpected EOF`**：4 分钟的非流式长连接被中途断开，重试即成功。
  客户端没有重试逻辑，因此一次网络抖动会烧掉一个 build。放到 K3.3 一起做——
  在同步路径上加重试会让一次 4 分钟调用变成 8 分钟，而带租约的异步 job 本来就要处理重试。
- **同步编译单次耗时 200–400 秒**，已经超出任何合理的 HTTP 客户端默认超时。
  这不是可以调参绕过的问题，是 K3.3 必须做的直接原因。

以上两条**已在 K3.3 解决**（§14）：传输失败进入有界重试，编译进入带租约的异步队列，
`POST /compile` 现在 49 毫秒返回 202。

### 13.4 与设计的偏离

- `parent_build_id` 在 full build 上**不记录**。它参与输入身份六元组，若每次都把最近一个
  成功 build 记为 parent，身份就永远唯一、幂等复用永远不命中。full build 的血缘可以由
  source + 时序还原；该字段留给 incremental build，那里 parent 是真正的输入。
- `mode=incremental` 显式拒绝而不是降级为 full。降级会让调用方以为省了成本。


## 14. K3.3：带租约的异步编译

### 14.1 为什么队列放在 build 表里

租约字段直接加在 `knowledge_build_revisions` 上（migration 000028），不另建 job 表。
build 行本来就存在、本来就需要一个 status；再开一张表就有**两处回答"这个 build 是不是在跑"**，
而两处会在崩溃之后不一致——恰好是最需要正确答案的时刻。

代价：queue 查询与 build 历史查询共用一张会持续增长的表。用部分索引
（`WHERE status IN ('queued','running')`）压住，终态行不进索引。

### 14.2 回收依据是租约到期，不是 worker 存活

不做心跳探活判定"worker 死了没"。被网络分区的 worker 从数据库看过去和崩溃的 worker
完全一样，后果也一样：没人在推进。因此只看 `lease_expires_at`。

`attempt` 记的是**领取次数而非失败次数**。一个会静默弄死 worker 的 build（OOM、panic）
永远不会自己报告失败，如果只在失败时计数，它会被无限重试。代价是优雅关停也会消耗一次——
所以关停路径显式**退还** attempt（`YieldBuild`）：滚动发布不该烧掉所有在途 build 的重试预算，
而崩溃到不了这条路径，投毒保护仍然成立。

worker 身份是 `host/pid/nonce`。nonce 不是装饰：容器重启后 PID 仍然是 1，没有 nonce
的话新进程看起来就是同一个 owner，能继承自己的过期租约，回收机制直接失效。
真实环境已确认这一点——重启前后 owner 为 `440a8d2e2eed/1/cf21960242ae` 与
`440a8d2e2eed/1/9064d03ddbc3`，只有 nonce 不同。

### 14.3 重试分类是这一节的实质

| 失败 | 是否重试 | 理由 |
|---|---|---|
| 传输失败 / 连接被断 / 解码中断 | 是 | 上一轮记录的 `unexpected EOF` 就是这一类 |
| 429 / 408 / 5xx | 是 | provider 明确表示"稍后再来" |
| 4xx（除上述） | 否 | 会确定性复现 |
| 输出被 `max_tokens` 截断 | 否 | 同预算重试还是截断，只是账单翻倍 |
| **check 失败** | **否** | 见下 |
| 未知错误 | 否 | 编译很贵，默认值必须取便宜的那个 |

**check 失败不重试是政策决定而非优化。** 编译不可重现，所以重试确实可能"这次就过了"——
但那正是**靠重复摇骰子通过门禁**，等于用重试放松标准，与架构不变量（§7.5：可自动进化的是
发现问题的能力，不可自动放松通过门槛）直接冲突。

退避从 30s 起指数增长、上限 600s。刚断掉一个四分钟连接的 provider，一秒后通常还是不高兴；
但没有上限的话一次抖动能让 build 消失几个小时。

### 14.4 提交与成功状态合并进一个事务

原先 `CommitBuild` 写页面、`FinishBuild` 改状态，是两步。worker 在中间被杀会留下
**有页面但状态仍为 running** 的 build；一旦有了回收机制，这个窗口就变成重复页面的来源。
合并后 "这个 build 有页面" 与 "这个 build 成功了" 是同一个事实。

提交额外受租约保护（`WHERE lease_owner = $n`）：租约已经易主的 worker 拿到
`ErrLeaseLost` 并**丢弃自己的产物**，而不是与新 owner 抢着提交。理由与 check 一致——
交错两份各自完整的 wiki，得到的是一份谁都没检查过的图。

心跳发现租约丢失时会**取消正在进行的编译**。不取消的话，一个已经无权提交的 worker
还在继续为模型付费。

### 14.5 成本记账

每次尝试的 token 与成本**累加**到 build 上（`RecordAttemptUsage`）。失败两次后成功的
build 花了三次调用的钱，只报最后一次会让账单无法解释。

单价按角色配置，默认 **0**。编一个默认单价会在账上放一个看起来权威的错数字；
0 是可见的"没人告诉我们价格"。

### 14.6 API 变化

`POST /api/knowledge/compile` 从同步 200 变成 **202 + queued build**，调用方轮询
`GET /api/knowledge/builds/:id`。新增 `GET /api/knowledge/queue` 与 MCP
`knowledge_queue_stats`——没有它，"排队等待"和"worker 卡死"从外面看完全一样。

**不保留同步路径。** 两条路径意味着两套行为要维护，而其中一条已知不可用（200–400 秒）。

enqueue 时仍然做完所有便宜且确定的检查（source 有 active revision、有可索引文档、
mode 合法、输入身份复用），因为此时调用方还在，能被告知原因；排队之后再失败没人看得见。

### 14.7 真实环境验证结果（2026-07-29）

- [x] `POST /compile` **49 毫秒**返回 202（此前 200–400 秒），队列深度随 receipt 返回。
- [x] 心跳每 15 秒推进 `heartbeat_at`，租约 30 分钟（由 compile timeout 推导，不独立配置——
      租约短于编译会让健康 worker 的 build 被抢走）。
- [x] 编译完成后 `lease_owner`/`lease_expires_at` 清空，`queued_at`/`started_at`/`finished_at`
      三个时间点让**排队等待与编译耗时可分离**（排队 2 秒、编译 140 秒）。
- [x] 并发 2 生效：两个 build 同时 running，同一 worker 持有。
- [x] **优雅重启**：`docker compose restart server` 时两个在途 build 被 yield，
      新 worker（nonce 不同）在数秒内重新领取，`attempt` 退还后仍为 1 —— 重启没有花掉重试预算；
      两个 build 最终各自产出 20 页与 16 页，**page 路径零重复**。
- [x] migration 000028 把同步时代遗留的 2 个永久 `running` 行关成 `cancelled`——
      否则 worker 一上线就会去领一个永远完不成的任务。
- [x] `cost_micros = 0`（未配置单价），`input_tokens`/`output_tokens` 真实记录。
- [x] 三个 demo KB 现在都有 active wiki。

集成测试新增 6 个：重试后成功、耗尽 attempt 后放弃、终态失败不重试、
被弃 build 回收后只留一份 wiki、心跳丢租约后提交被拒、yield 退还 attempt。
另有 3 个 worker 单元测试（退避增长与上限、租约由 compile timeout 推导、worker 身份唯一）
与 2 个 llm 单元测试（重试分类、未配置单价记 0）。

### 14.8 本轮暴露的缺陷

**Go duration 字符串当 Postgres interval 用。** 原先租约与退避写成
`NOW() + $n::interval` 并传 `duration.String()`。Postgres 直接拒绝 `"1ns"`，
而 `"1m0s"` 能解析纯属缩写巧合（m→minute、s→second）。后果是亚秒退避的
`RequeueBuild` 静默失败，build 永远持着租约、永远不到终态。改为
`make_interval(secs => $n)` 传数字，不留解释空间。这个缺陷是集成测试抓到的，
生产默认值（30 秒退避）恰好能工作——它只会在有人把退避调到亚秒时爆发。

### 14.9 仍未做

- **成本闸门**：只有记账没有预算上限。profile 的 `max_build_tokens` 是事后 check，
  不是事前拦截；账号级配额与预估拦截未做。
- **provider 侧幂等**：重试会重新发起完整调用。若上一次其实已经生成完但连接在返回途中断开，
  这一次是重复付费。要真正解决需要 provider 支持幂等键。
- **优先级 / 公平性**：单一 FIFO 队列。一个账号排入 50 个 build 会让其他账号排在后面。
