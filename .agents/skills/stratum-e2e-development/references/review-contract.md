# Independent Review Contract

| Risk | Required review |
| --- | --- |
| R0 | automated checks |
| R1 | code quality |
| R2 | code quality; specification when behavior changes |
| R3 | specification and code quality |
| R4 | specification, code quality, and release evidence |

审查者不得是实现 Agent。先进行规格审查，规格通过后才能进行代码质量审查。任何 finding 修复后必须由原审查
类型复审。审查结果记录 reviewer identity、review type、commit、policy version、findings 和 verification evidence。
相关代码变化后审查自动失效。

自动 lint、schema、单测和质量门禁是 review 输入，不能自称独立审查。CI 只接受受保护 GitHub environment
签发的结构化 review receipt；环境必须配置非实现者 required reviewer。字符串环境变量 `passed` 不是审查证据。
