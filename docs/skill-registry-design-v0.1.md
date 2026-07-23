# AgentMate Git-backed Skill Registry 设计

**版本**：v0.1
**日期**：2026-07-18
**状态**：IN PROGRESS（Phase 1-4 核心已实现）
**范围**：`internal/skills`、`migrations/000017`-`000019`、Skills REST/MCP 与检索集成。

---

## 0. 执行摘要

AgentMate Skill Registry 不是新的 Git 文件托管服务，也不是把全部 Skill 文档切块后放进向量库的通用 RAG。

它是建立在 GitHub/GitLab 之上的 Skill 控制面：

> **Git 仓库保存实际 Skill package；AgentMate 管理 source 同步、不可变 package revision/release、active version、编译后的路由索引、渐进式披露、version-bound 遥测，以及离线 deterministic lint/eval/comparison。**

当前阶段的核心成果是修正 Skill 身份模型：完整 package，而不是根目录中的 `SKILL.md`，决定一个 Skill version 的不可变身份。

关键决策：

1. **Git 是内容事实源**：AgentMate 保存确定性快照和索引，不替代 GitHub/GitLab。
2. **完整 package 是身份边界**：脚本、资源或文档变化必须产生不同 `package_hash`，即使根 `SKILL.md` 完全相同。
3. **revision 与 version 一一对应**：一个 source revision 对应一个 immutable version；数据库和应用层同时验证双向关系。
4. **active 是可变指针，不是版本属性历史**：每个 `account + skill_name` 同时只能有一个 active version。
5. **PostgreSQL 是 registry 事实源**：Qdrant/retrieval document 是可重建的派生路由索引。
6. **渐进式披露优先**：搜索先返回轻量 Skill Card，只有被选中的 Skill 才加载说明或资源。
7. **先支持公开 GitHub/GitLab**：初始同步不处理私有仓库凭据、自建 GitLab 或任意 Git 协议。
8. **Skill 与 Knowledge 是独立业务域**：Skill package 保存行为、约束、工具和 execution assets；领域事实、文档语料与持久化 wiki 属于独立 Knowledge Registry。
9. **动态发现而非固定绑定**：未来 Skill 只声明 Knowledge Discovery Contract，由 Agent 在 account/workspace 授权范围内运行时选择 KB，并把最终选择保存为 KnowledgeResolutionRun；初始模型不要求显式 BindingRevision。

完整目标架构见 [Skill + Knowledge Architecture v0.1](skill-knowledge-architecture-v0.1.md)。

---

## 1. 目标与非目标

### 1.1 目标

- 注册 `local` 或 `git` Skill source。
- 从本地客户端 snapshot 或公共 GitHub/GitLab 仓库获得完整 package。
- 对 package 文件集合计算确定性 identity。
- 保存 immutable revision、version 和文件清单。
- 支持并发安全、幂等重放和 active version 切换。
- 将 active version 编入统一 retrieval 基础设施。
- 通过 REST 和 MCP 提供搜索、同步、选择和渐进加载能力。
- 为未来 eval、lint、编译、PR 演化和反馈学习保留稳定的数据基础。

### 1.2 非目标

- 不在 AgentMate 中实现 Git object database、分支管理或代码评审系统。
- 不复制 GitHub/GitLab 的仓库权限和协作模型。
- 不把任意文档目录都解释为 Skill。
- 不把 Skill Registry 用作通用领域知识库：领域文档、事实语料和持久化 wiki 属于未来独立的 Knowledge Registry（见 [Skill + Knowledge Architecture v0.1](skill-knowledge-architecture-v0.1.md)）。
- 第一阶段不支持私有 repository token、SSH、Git LFS 或 submodule。
- 第一阶段不自动修改上游仓库；后续 compiler/evolver 只通过 Pull/Merge Request 提交建议。
- 第一阶段不实现完整 DAG 编译器或自动训练路由模型。

---

## 2. 领域模型

```text
SkillSource
  GitHub/GitLab repository 或 local directory 的注册信息
        |
        | 1:N
        v
SkillSourceRevision
  某个完整 package 的 immutable canonical revision
        | 1:1
        v
SkillVersion
  可发布、可激活、可索引的 Skill release
        | 1:N
        v
SkillVersionFile
  package 文件清单与可选文本快照

SkillVersion(active)
        |
        v
RetrievalDocument(namespace=skills)
  可重建的路由/搜索索引

SkillLog
  执行结果、失败、纠正和性能遥测；可选绑定 immutable SkillVersion

SkillQualityRun
  单个 version 的离线确定性 lint/eval/comparison/telemetry 审计报告
```

### 2.1 `SkillSource`

表示内容来源，而不是具体版本。

关键字段：

- `type`：`local` 或 `git`。
- `repository_url`：本地逻辑地址或 GitHub/GitLab HTTPS URL。
- `package_path`：仓库内单个 Skill package 的相对路径。
- `default_ref`：Git source 默认 branch/tag/ref。
- `sync_mode`：local 使用 `client_push`，git 使用 `server_pull`。
- `status`：`active`、`disabled`、`error`。
- `metadata`：provider 等可扩展元数据。

当前约束是：**一个 source 对应 repository 中的一个 `package_path`**。同一 monorepo 中的多个 Skill 应注册为多个 source。这样 source、package identity、active release 和错误状态都保持独立。

### 2.2 `SkillSourceRevision`

表示已经确定内容的 immutable package revision。

关键字段：

- `revision_key`：source 内稳定且唯一的 revision identity。
- `commit_sha`：Git provenance；local source 为空。
- `local_snapshot_id`：客户端幂等键；Git source 为空。
- `tree_hash`：来源树或 canonical package tree identity。
- `package_hash`：AgentMate 根据 package 文件集合计算的 canonical hash。
- `skill_version_id`：对应的 immutable version。

`revision_key` 不是用户可修改标签。local snapshot 当前使用 `package:<package_hash>`。Git 同步也必须以 canonical package 为最终去重边界；commit SHA 是来源证据，而不是创建重复 package release 的理由。

历史 Git 数据可能保留 `commit:<sha>` revision key。迁移会折叠同 source 下 package 内容相同的重复 commit revision。若未来需要完整保存“多个 commit 指向同一 canonical package”的关系，应增加独立 alias/provenance 表，而不是复制 version。

### 2.3 `SkillVersion`

表示 Agent 可选择、激活和索引的 release。

关键字段：

- `source_id`、`source_revision_id`：package 来源。
- `skill_name`、`version`：面向调用者的名称与显示版本。
- `content`：根 `SKILL.md` 的兼容内容。
- `content_hash`：只对根 `SKILL.md` 计算，保留用于查询和兼容。
- `package_hash`：完整 package identity。
- `is_active`：当前路由是否使用该 release。

`content_hash` 不再承担唯一身份。相同 `SKILL.md` 配合不同脚本或资源会产生相同 `content_hash`、不同 `package_hash`，因此必须成为不同 version。

### 2.4 `SkillVersionFile`

保存 package 文件清单：

- canonical 相对 `path`；
- `sha256` 与 `size_bytes`；
- `kind`、`mime_type`、`indexable`；
- 文本文件的可选 `content_snapshot`；
- `source_revision_id` 与 `version_id`。

二进制文件参与 package identity，但不要求保存完整内容。Git 仍然是完整原始内容的事实源。

---

## 3. 核心不变量

### 3.1 Package identity

canonical package hash 对排序后的全部文件计算：

```text
SHA256(
  sort(
    relative_path + NUL + file_sha256 + NUL + size_bytes
  ).join("\n")
)
```

因此：

- 文件内容变化会改变 identity；
- 文件路径变化会改变 identity；
- 增删文件会改变 identity；
- 文本和二进制文件都参与 identity；
- 文件枚举顺序不会改变 identity。

### 3.2 Revision/version 一一对应

数据库使用以下机制共同保证：

- `skill_versions.source_revision_id` 唯一；
- `skill_source_revisions.skill_version_id` 唯一；
- 两组 reciprocal composite foreign key 强制两个方向指向同一对记录；
- 约束为 deferred，使单事务内可以先创建 revision、再创建 version、最后补齐反向引用。

应用重放时还会校验：

- version 的 `source_id` 等于当前 source；
- version 的 `source_revision_id` 等于当前 revision；
- version 与 revision 的 `package_hash` 相同。

### 3.3 单 active version

部分唯一索引保证：

```text
(account_id, skill_name) WHERE is_active = true
```

发布、ingest 和显式 activate 使用 PostgreSQL transaction advisory lock，锁顺序固定为：

1. `account + skill_name`；
2. `source_id`。

唯一索引是最终正确性边界，advisory lock 用于让并发切换产生稳定、可重试的行为。

### 3.4 幂等与身份冲突

local snapshot 同时具有：

- canonical `revision_key`；
- 客户端 `local_snapshot_id`。

规则：

- 两者都命中同一 revision：返回已有 version/files；
- 两者命中不同 revision：冲突；
- package key 已存在，但请求携带新的 snapshot ID：冲突，不静默创建 alias；
- snapshot ID 已存在，但 revision key 不同：冲突；
- 已有 identity 对应不同 package/tree：冲突。

若未来需要一个 package 对应多个外部幂等键，应增加显式 alias 表，不能只在响应中假装接受而不持久化映射。

### 3.5 多租户

所有 registry 查询和写入以 `account_id` 为隔离边界；`user_id` 和 `key_id` 用于主体与来源审计。文件读取同时过滤 version 和 file 的 `account_id`。

---

## 4. 当前写入流程

### 4.1 Local snapshot（已实现）

```text
Client
  -> POST /api/skills/sources/:id/snapshots
  -> validate source(type=local, status!=disabled)
  -> normalize paths/files
  -> verify supplied sha256/size/package_hash/tree_hash
  -> compute canonical package_hash
  -> derive revision_key = package:<hash>
  -> transaction lock(account+skill, source)
  -> replay/conflict detection
  -> insert revision
  -> insert version referencing revision
  -> update revision back-reference
  -> insert files
  -> optionally activate
  -> optionally index active version
```

成功 ingest 会把处于 `error` 的 source 恢复为 `active`；`disabled` source 不允许继续 ingest。

### 4.2 Git sync（已实现）

当前流程：

```text
Client / Agent
  -> POST /api/skills/sources/:id/sync
  -> validate source(type=git, status!=disabled)
  -> parse public GitHub/GitLab URL
  -> resolve requested/default ref to immutable commit SHA
  -> download commit tar.gz archive
  -> locate package_path below archive root
  -> normalize and bound archive entries
  -> require root SKILL.md inside package_path
  -> compute canonical package_hash
  -> reuse immutable revision/version ingest transaction
  -> optionally activate and index
  -> return provider/ref/commit/revision/version/files
```

已实现公共 URL/provider 解析、默认分支与指定 ref 解析、immutable commit SHA、受限 tar.gz 下载和 `package_path` 提取：

- `https://github.com/<owner>/<repo>[.git]`
- `https://gitlab.com/<namespace...>/<project>[.git]`

拒绝 HTTP、URL credentials、query/fragment、仓库子页面、自建域名和非 GitHub/GitLab provider。archive 解析拒绝 traversal、link 和多 root，并限制下载大小、文件数、单文件及 package 总大小。

同步成功会持久化 `metadata.git_sync` 并恢复 source 为 `active`；provider、archive、normalization 或 ingest 失败会将 source 标记为 `error`，后续成功同步可恢复。相同 package 在不同 commit 上幂等复用 canonical revision/version。

---

## 5. Git provider 设计

### 5.1 GitHub

初始公共 API 流程：

1. 解析 `owner/repo`。
2. 若请求和 source 都未指定 ref，读取 repository `default_branch`。
3. 调用 commits API 将 branch/tag/ref 解析为完整 commit SHA。
4. 以 commit SHA 下载 tarball。

### 5.2 GitLab

初始公共 API 流程：

1. 解析任意深度的 `namespace/project`。
2. URL encode project path 调用 GitLab v4 projects API。
3. 解析默认 branch 或指定 ref 对应的 commit ID。
4. 以 commit ID 下载 repository archive。

### 5.3 HTTP 与 archive 边界

provider client 使用标准库 `net/http`、`archive/tar` 和 `compress/gzip`，不新增 Git SDK 依赖。

必须执行的边界：

- context cancellation 和 HTTP timeout；
- 非 2xx 响应映射为明确 provider 错误；
- 限制 archive 下载字节数；
- 限制文件数量、单文件大小和总解压大小；
- 拒绝 absolute path、`..`、symlink、hardlink 和设备文件；
- 只提取 `package_path` 子树；
- 去除 provider tarball 自动添加的单层仓库根目录；
- `SKILL.md` 必须位于 package 根目录；
- 二进制文件只保存 hash/size，允许文本文件进入 `content_snapshot`。

初始阶段只支持公开 SaaS provider。私有仓库认证、自建 GitLab allowlist 和网络出口策略属于后续独立设计。

---

## 6. REST 与 MCP

### 6.1 当前 REST

| 接口 | 状态 | 作用 |
|---|---|---|
| `POST /api/skills/sources` | 已实现 | 注册或更新 local/git source |
| `GET /api/skills/sources` | 已实现 | 列出 source |
| `GET /api/skills/sources/:id` | 已实现 | 获取 source |
| `GET /api/skills/sources/:id/revisions` | 已实现 | 列出 immutable revisions |
| `POST /api/skills/sources/:id/snapshots` | 已实现 | 推送 local package snapshot |
| `POST /api/skills/sources/:id/sync` | 已实现 | 拉取并同步 Git source |
| `GET /api/skills/versions` | 已实现 | 列出 release versions |
| `GET /api/skills/versions/active` | 已实现 | 获取 active version |
| `GET /api/skills/versions/:id/files` | 已实现 | 获取内部 package 文件记录（兼容接口） |
| `POST /api/skills/versions/:id/activate` | 已实现 | 切换 active version，并尽力刷新 artifact |
| `POST /api/skills/compile` | 已实现 | 编译/重编译单个 version，或回填全部 active versions |
| `GET /api/skills/catalog` | 已实现 | 稳定分页和 query 的 active L0 catalog |
| `GET /api/skills/versions/:id/instructions` | 已实现 | 加载 L1 instructions，响应禁止缓存 |
| `GET /api/skills/versions/:id/resources` | 已实现 | 加载无正文的 L2 resource manifest |
| `GET /api/skills/versions/:id/resources/:file_id` | 已实现 | 严格 account/version/file 绑定后加载单个文本 resource |
| `POST /api/skills/versions/:id/quality-runs` | 已实现 | 同步运行离线确定性质量检查，可选同 skill baseline |
| `GET /api/skills/versions/:id/quality-runs` | 已实现 | 按 created_at/id 稳定分页列出报告 |
| `GET /api/skills/quality-runs/:run_id` | 已实现 | 读取 account-scoped 报告，禁止缓存 |
| `POST /api/skills/index` | 已实现 | 用 compiled L0 card 重建 active Skill 路由索引 |
| `POST /api/skills/search` | 已实现 | 搜索 L0 cards，兼容 `include_content` |

### 6.2 MCP

当前 MCP 覆盖日志、直接发布、Git sync、active version、统计、信号、搜索、索引和渐进式披露。Phase 3 新增：

- `skill_catalog_list`：稳定分页查询 active L0 cards；
- `skill_compile`：编译一个 version，或回填全部 active versions；
- `skill_version_instructions`：加载 L1 `SKILL.md`；
- `skill_version_resources`：加载不含正文的 L2 manifest；
- `skill_resource_get`：通过 account-scoped version/file 对加载单个文本 resource。

Phase 4 只新增：

- `skill_quality_run`：运行同步、离线、确定性的质量检查；
- `skill_quality_get`：按 account 读取一条质量审计记录。

`skill_log_add` 增加可选 `skill_version_id`。显式 ID 必须属于当前 account 且匹配 `skill_name`，服务端以 version 表中的标签覆盖调用方文本标签；未传 ID 的旧日志继续兼容，但保持 unassigned，不进入 Phase 4 telemetry。

---

## 7. 检索与渐进式披露

当前 active version 会将 compiled L0 card 写入 `retrieval_documents` 的 `skills` namespace。索引 metadata 包括：

- `skill_name`、`version`、`version_id`；
- `source_id`、`source_revision_id`、`package_hash`；
- description、triggers、capabilities、constraints、dependencies；
- compiler name/version、compiled time、resource count、published time。

索引正文只包含 L0 card，不包含完整 `SKILL.md` instructions 或 resource 正文。`000018` 会把升级前可能含完整 instructions 的 Skill retrieval document 改写为 bounded basic card，并把状态设为 `failed`，使旧 Qdrant point 无法回表参与 vector hydration，同时保留安全的 PostgreSQL lexical fallback。升级后应依次调用 compile 与 index 重建 artifact 和 embedding。显式 `include_content=true` 会在搜索选中后按 account/version 从 PostgreSQL 加载 L1，不会把正文重新放回 retrieval index。

披露层级：

```text
L0 Catalog / Skill Card
  name, description, capabilities, constraints, cost, package identity

L1 Instructions
  选中后加载 SKILL.md

L2 Selected Resources
  按任务需要加载 Skill 执行资产：templates、schemas、tests、行为示例或脚本说明

L3 Complete Package
  仅执行器或审计流程需要完整文件清单/原始 Git 内容
```

L2 resource manifest 的语义是 **Skill execution assets**，不是通用领域知识。领域文档和事实语料属于未来独立 Knowledge Registry 的 K0/K1/K2 披露轴；过渡期内既有 reference 文件仍可通过现有 API 加载，但不应再向 Skill package 中新增独立知识语料（见 [Skill + Knowledge Architecture v0.1](skill-knowledge-architecture-v0.1.md)）。

Phase 3 deterministic compiler 已从 `SKILL.md` frontmatter 和文件快照构建：

- Skill Card；
- triggers、capabilities、constraints、dependencies；
- 稳定排序且不包含根 `SKILL.md` 的 resource manifest；
- compiler name/version 与 input package hash；
- compiled L0 routing index document。

artifact 不保存 resource 正文，可由 immutable package snapshot 重建。catalog 中的 `skill_name` 始终来自 `skill_versions.skill_name`；frontmatter `name` 不能在编译时重命名控制面身份，二者不一致由 Phase 4 lint 报告。DAG/组合路由仍属于后续阶段。

编译产物是可重建派生数据，不能替代 Git package 或 PostgreSQL registry identity。

---

## 8. Phase 4 离线质量层

Phase 4 是同步、离线、确定性的审计层。每次运行在单个只读 `REPEATABLE READ` 一致快照内读取 immutable version、严格 version files、已有 compiled artifact、同 skill baseline，以及 cutoff 前绑定到该 `skill_version_id` 的最近 200 条日志；唯一写副作用是一条 `skill_quality_runs` 审计记录。

检查集包括：

- package lint：根 `SKILL.md`/content、account/version 文件关系、canonical package hash、文本快照 hash/size、frontmatter name/description/routing metadata、重复项、自依赖、compiled artifact freshness 和 manifest 一致性；无 files 的 direct version 必须满足 `sha256(content) == content_hash == package_hash`；
- platform contract eval：重复编译一致、文件顺序不变、L0 不含正文、manifest 精确稳定、selected-resource 隔离与大小边界；
- release comparison：只允许同 account、同 `skill_name` baseline，显式指定或自动选择 previous release；输出 `package_hash_changed`、`resource_manifest_changed`、文件 hash diff、compiled routing diff 和 lint/eval regressions。resource manifest 使用内存编译结果（失败时可回退现有 artifact）比较 path/kind/MIME/indexable/text_available 等行为元数据，不修改 Phase 1 package hash 算法；
- telemetry snapshot：未绑定 `skill_version_id` 的历史日志完全排除；outcome 比率只以 triggered 为分母；triggered 少于 20 时为 `insufficient`；bypass/correction/failure/partial 仅在计数至少 3 且比率至少 10% 时产生固定建议。

comparison status 只使用：`not_available`、`blocked`、`static_regression_detected`、`review_required`、`no_static_regression_detected`。package hash 或 resource behavior manifest 不同时至少为 `review_required`。每个 check 包含确定性 severity：identity/root 为 `blocker`，frontmatter/manifest/self-dependency 与 platform eval 为 `error`，description/routing/duplicate/artifact 为 `warning`。报告不产生综合 quality score，不使用 legacy `eval_pass_rate`，不保存 instructions、resource 或 log 正文；建议只保存计数、分母、比率、fingerprint 和 log IDs。

质量运行列表只查询、返回不含 `report` 的 `QualityRunSummary`，保持 `created_at DESC, id DESC` 的稳定分页；只有 detail endpoint 解码完整 JSON report。`000019` 对 `skill_logs` 和 `skill_quality_runs` 的 target version 复合外键使用默认 `NO ACTION`，禁止单独删除被审计目标；quality run 的 account FK 保持 `ON DELETE CASCADE`，baseline FK 保持 column-list `ON DELETE SET NULL (baseline_version_id)`。

明确限制：该层不调用网络、LLM、provider、Qdrant，不发布、激活或索引 version，不创建 PR/MR，也不声称 semantic eval 或 reinforcement learning。Git 演化和人工审批仍是未来独立增量。

---

## 9. 错误与状态模型

source 状态：

- `active`：可同步/ingest；
- `disabled`：人工停止，拒绝写入；
- `error`：最近同步失败，可由下一次成功同步恢复。

revision 状态：

- `queued`、`ingesting`、`ingested`、`failed`。

当前 local ingest 在单事务内完成，因此主要产生 `ingested`。Git sync 初始也采用同步 HTTP 请求；若 archive 大小和 provider latency 证明不适合请求内执行，再引入持久化 sync job，而不是提前增加队列复杂度。

失败不能产生半完成 revision/version/file 关系。provider 获取失败应更新 source 错误状态，但 package ingest 的数据库事务必须原子提交或全部回滚。

---

## 10. 兼容性

第一阶段有意收紧 local snapshot 行为：

- 调用者提供的 `package_hash` 必须等于 AgentMate canonical hash；
- local `tree_hash` 必须等于 canonical package hash；
- 带文本内容的 `size_bytes` 必须等于实际字节数；
- 相同 snapshot identity 不能被重新解释为不同 package 或 skill；
- 不支持未持久化的 snapshot alias。

这是早期 open-source 阶段的 deliberate breaking change。旧客户端应省略可推导 hash/size，让服务端计算。

---

## 11. 已验证内容

package identity foundation 已通过：

- Go 全量测试、`go vet` 和 skills race test；
- 000017 全量 up、down、再次 up；
- 历史 one-version/multi-revision 数据拆分；
- 历史不同 Git commit、相同 package 的折叠；
- 同 `content_hash`、不同 `package_hash` 的写入与回滚；
- 并发相同 snapshot 幂等重放；
- snapshot 双身份冲突；
- 并发不同 package ingest；
- 并发 active 切换后仅一个 active；
- version/revision/file reciprocal linkage；
- account 删除时 source-bound 数据级联清理。

PostgreSQL 集成测试由 `AGENTMATE_TEST_DATABASE_URL` 显式启用，默认单元测试环境会 skip。

---

## 12. 路线图

### Phase 1：Package identity foundation（已实现）

- canonical package hash；
- immutable source revision/release；
- reciprocal one-to-one relationship；
- active uniqueness；
- local snapshot strict validation；
- concurrency/idempotency integration test。

### Phase 2：Public Git sync（核心已实现）

- GitHub/GitLab provider；
- ref -> immutable commit；
- bounded tar.gz extraction；
- Git source sync REST/MCP；
- source error/recovery 状态；
- provider/archive tests 与公开仓库 smoke test。

### Phase 3：Compiled catalog 与渐进式披露（核心已实现）

- deterministic Skill Card compiler 与 compiler versioning；
- triggers/capabilities/constraints/dependencies；
- 可重建 artifact 与无正文 resource manifest；
- active catalog 稳定分页/query 和 basic fallback；
- L1 instructions、L2 manifest、selected text resource fetch；
- compiled card index/search 与 ingest/publish/activate best-effort refresh；
- DAG/组合路由留待后续增量。

### Phase 4：离线 deterministic quality（核心已实现）

- package lint 与 platform contract checks；
- 同 account、同 skill release comparison，显式报告 package hash 和 resource behavior manifest 变化；
- check severity 与 blocker/error/warning findings；
- 严格 `skill_version_id` telemetry snapshot 与固定阈值 suggestions；
- append-only target FK、单表 account-scoped audit runs、summary list/full detail REST/MCP；
- GitHub Pull Request / GitLab Merge Request、语义评估、自动强化学习继续留待后续，且不在本阶段暗示已实现。

### Phase 5+：Knowledge Registry 与动态知识发现（未实现）

独立于 Skill Registry 的 Knowledge 域按 [Skill + Knowledge Architecture v0.1](skill-knowledge-architecture-v0.1.md) 的 K1–K5 路线推进（K1–K5 是实施里程碑编号，与知识披露层级 K0–K3 无关）：

- K1 Knowledge source 与 immutable identity；
- K2 Knowledge catalog 与 K0/K1/K2 检索；
- K3 Knowledge compiler / persistent wiki 与 promotion；
- K4 Skill Knowledge Discovery Contract、`knowledge_discover` 与 KnowledgeResolutionRun；
- K5 企业扩展（private credentials、object storage、external KB provider）。

Skill Registry 在 K4 前不修改现有 package identity 或 L0/L1/L2 API 契约。

---

## 13. 代码位置

| 路径 | 职责 |
|---|---|
| `internal/skills/model.go` | Registry API/domain models |
| `internal/skills/repo.go` | PostgreSQL 事务、不变量与查询 |
| `internal/skills/service.go` | source/snapshot normalization、hash、index orchestration |
| `internal/skills/compiler.go` | deterministic frontmatter compiler 与 stable resource manifest |
| `internal/skills/catalog_repo.go` | compiled artifact、catalog 分页和 scoped resource 查询 |
| `internal/skills/catalog_service.go` | compile/fallback 与 L0/L1/L2 disclosure |
| `internal/skills/quality_engine.go` | 纯 deterministic lint/eval/comparison/telemetry 引擎 |
| `internal/skills/quality_service.go` | 只读输入编排、same-skill baseline 与 audit run |
| `internal/skills/quality_repo.go` | version-bound telemetry 和 account-scoped quality runs |
| `internal/skills/git_provider.go` | GitHub/GitLab URL、ref、commit 与 archive endpoint |
| `internal/skills/git_archive.go` | 受限 tar.gz 下载和 package 提取 |
| `internal/skills/handler.go` | REST handlers |
| `internal/skills/mcp.go` | Skills MCP tools |
| `internal/skills/repo_integration_test.go` | PostgreSQL concurrency/identity tests |
| `migrations/000017_refactor_skill_package_identity.*.sql` | package identity schema migration |
| `migrations/000018_create_skill_compiled_catalogs.*.sql` | compiled catalog artifact schema migration |
| `migrations/000019_create_skill_quality_runs.*.sql` | version-bound logs 与单表 quality audit schema |

设计原则只有一条主线：

> **Git 保存可协作的 Skill package；AgentMate 把 package 编译成可同步、可寻址、可搜索、可渐进加载、可评估和可演化的 Agent 能力。**
