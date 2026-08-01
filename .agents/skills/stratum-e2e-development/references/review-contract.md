# Review Boundary

规格审查和代码质量审查用于发现需求偏差、缺陷和测试缺口，但不签发浏览器、合并或部署状态。

- review finding 必须绑定所审 diff 或 commit；代码变化后重新判断其有效性。
- lint、schema、单测和质量门禁是 review 输入，不冒充人工审查。
- review 不生成 fake receipt，不用字符串 `passed` 伪装 protected control-plane evidence。
- local browser report 是 developer audit assertion；CI job results 是 merge authority；release record 是 deployment authority。
- 三种状态不可由 review 文本互相转换。

需要独立审查时仍遵循“先规格、后质量”，但是否合并只由仓库真实 required checks 和分支保护决定。
