# Failure Taxonomy

失败必须先归类，再决定修复对象：

| Class | Meaning | Required action |
| --- | --- | --- |
| product | 产品行为违反需求或契约 | 写失败测试并修复产品 |
| fixture | 测试数据、schema、cookie、URL 或身份漂移 | 修复 fixture，不削弱产品契约 |
| environment | runner 或依赖不可用 | 标记 blocked，保留诊断证据 |
| assertion | 断言与已确认需求冲突 | 修复断言并记录依据 |
| flaky | 同一输入下结果不稳定 | 定位竞态；重跑不改变首次失败 |
| policy | manifest、审查或 attestation 证据缺失 | fail closed，补齐证据链 |

隔离 flaky 测试必须有 owner、根因、范围和到期时间。认证、租户、迁移、安全、cleanup 和 attestation 测试
不得隔离。
