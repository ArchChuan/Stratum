# Golden 评测集：开发期 RAG 检索质量抽查

本目录是 `cmd/e2e-rag-check` 的首期 golden 数据集，用于开发期对知识库检索质量做抽查与回归。数据集内容按被评测工具逐字段校验，标注质量直接决定检索指标（MRR / NDCG / recall@k / no-answer 判定）的可信度。

## 数据集用途

- 开发期 RAG 检索质量抽查：在功能开发、检索参数调整、embedding 模型切换或分块策略变更后，用固定基线对比检索效果，防止回归。
- 按 `mode`（vector / keyword / hybrid）分路校验检索召回与排序；`expect_no_answer` 校验 no-answer 分支；`citation_documents` 校验引用完整性。
- 数据文件目录（相对本文件）：
  - `cases.yaml` —— 检索用例与标注
  - `documents/onboarding.md`、`documents/billing.md`、`documents/security-policy.md`、`documents/faq.md` —— 被测知识库源文档

## 来源

- 四篇文档为人工编写的代表性产品文档（入门、计费、安全合规、常见问题），每篇 600–1200 字，词汇域刻意区分，并为每条检索 query 埋设 2–3 个事实性、关键词密集的可检索锚点。
- 用例由人工按「自然用户提问」编写，基于上述文档内容标注相关文档、引用文档与无答案判定。

## 标注说明

- **相关文档（relevant_documents）**：答案事实内容所在的源文档。判定标准是「该 query 的完整答案只由这些文档回答」，非相关文档不得包含答案内容。相关文档按重要性排序，首位为主文档。
- **引用文档（citation_documents）**：可选字段。**双主题 query 一律列全答案所需的全部文档**（每条 query 的 `note` 会写明各主题对应哪篇），而非只列主文档——字段非空时，工具要求其中**所有**文档都被检索到才算 citation_correct。仅当答案确实需要多篇文档的完整上下文才列出，默认省略。
- **无答案（expect_no_answer）**：query 使用四篇文档中完全不出现的词汇/主题，真实检索应返回空。用于校验 no-answer 分支正确生效。
- **排序敏感**：`relevant_documents` 含 ≥2 篇不同文档、且存在一个明显更核心的主文档的 case，用于度量 MRR / NDCG 对排序的敏感性。

## 分路语义（随 mode 而不同）

- **keyword 模式查询必须词素密集**：keyword 腿走 `tsv @@ plainto_tsquery('public.chinese_zh', $2)`，`plainto_tsquery` 对所有分词 token 做 **AND**。因此 keyword case 的 query 必须是术语密集的检索串——每个分词 token 都要命中且只命中目标文档，杜绝自然语言虚词（具体/步骤/什么/长/多少/机会/限制）混入。违反该约束的 case 会因任一 OOV token 而整条查询空召回（见 cases.yaml 中 case 5/6/10 的 `note`）。
- **no-answer 由 keyword 模式 + OOV 词触发**：当前 `score_threshold=0`（不启用阈值过滤），vector 腿（Milvus L2 无距离截断）对非空 collection 恒返回 top-k，因此 vector/hybrid 模式下 no-answer 分支不可达。唯一确定性触发路径是 keyword 模式对文档外词素（如「线下/实体店/会员积分」）空召回 → `Sources=[]` → no-answer 生效。新增 no-answer case 时必须用 keyword 模式并保持 query 词素全部落在文档词汇域之外。
- **hybrid 的 keyword 腿在自然语言 query 下恒空**：hybrid 把 vector + keyword 两路结果合并，自然语言 query 的 keyword 腿因 AND 语义几乎必空，因此 hybrid 模式在效果上退化为 vector 语义；keyword 模式 case 专门覆盖 TSV 检索路径，两者互补而非重叠。

## 更新流程

- 新增用例：必须提交到本目录供人工 review，确认 query 是自然提问（不照抄文档标题/原句）、keyword 模式 query 满足词素密集约束、no-answer case 走 keyword + OOV 触发、相关/引用标注正确、词汇域不造成交叉命中后再合入。
- 基线重录：检索参数或 embedding 模型变更后需重录基线时，使用 `--record-baseline --confirm-record` 显式录制，禁止隐式更新基线。
- 修改文档内容：若改动文档锚点，需同步 review 受影响 case 的标注是否仍成立。

## 防过拟合红线

- 固定基线对比：评测以录制基线为准，禁止针对本数据集动态调参或特判。
- 标注可 review：所有相关/引用/无答案判定均写明 `note`，供复核，禁止静默改动。
- 禁止为过测扭曲实现：检索与排序逻辑不得因 golden 数据集的 case 而做特判；golden 只用于度量，不驱动实现。

## HTTP 保真子集说明

- 本集只评测 `reranking=""` + `query_rewrite=none` 配置下的 `/knowledge/query` 路径（见 `cases.yaml` 的 `snapshot` 段）。
- 打开重排或查询改写后的行为不在本集覆盖范围内，接入这类能力时需另建对应快照的评测集。

## 二期扩展点

- 白名单隔离评测预留：二期计划在文档中划分「白名单隔离」段落，用于评测租户/来源隔离场景下检索不得命中隔离外的文档；当前数据集未包含该能力，字段与用例留待后续扩展。
