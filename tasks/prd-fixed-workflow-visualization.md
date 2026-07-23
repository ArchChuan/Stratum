# PRD：固定工作流可视化前端

## 1. Introduction

为 Stratum 的固定 DAG 工作流提供可视化产品闭环。管理员通过引导式画布创建、验证和发布工作流；普通用户通过工作流中心或 Agent 快捷入口提供任务输入并执行；运行中心实时展示节点进度、输出、工具步骤、审批、人工介入和失败位置。

现有后端执行内核继续作为事实源。本功能同时补齐目录查询、输入 schema、运行归属和实时展示事件，不能退化为依赖手工 ID 或原始 JSON 的前端演示。

## 2. Goals

- 管理员无需编辑 JSON 即可管理固定工作流。
- 普通用户通过简单表单启动已发布版本。
- 提供可恢复的实时 DAG 运行监控。
- 实现普通用户自己的运行与管理员租户全局运行的授权边界。
- 保持发布版本和历史运行快照不可变。

## 3. User Stories

### US-001：补齐工作流输入与运行归属模型

**Description:** As a 平台开发者, I want 工作流版本冻结输入 schema 且运行记录发起人 so that 系统可以生成稳定表单并执行对象级授权。

**Acceptance Criteria:**

- [ ] Definition 草稿保存自然语言主任务和附加字段 schema
- [ ] Version 发布时冻结输入 schema
- [ ] Run 持久化当前租户用户作为 `created_by`
- [ ] Run 对外提供创建、开始和结束时间
- [ ] 历史 schema 升级测试覆盖已有租户
- [ ] 事务失败向上传播并回滚
- [ ] Go tests and race tests pass

### US-002：提供工作流目录与版本查询

**Description:** As a 用户, I want 浏览可用工作流 so that 我不需要知道工作流或版本 ID。

**Acceptance Criteria:**

- [ ] 普通用户分页查询只返回已发布工作流
- [ ] 管理员分页查询同时返回草稿状态和当前发布版本
- [ ] 支持名称搜索并使用稳定分页排序
- [ ] 提供指定定义的不可变版本列表
- [ ] 空结果返回空数组和分页信息，不返回 404
- [ ] tenant ID 显式进入 repository port 并通过 `execTenant`
- [ ] HTTP contract tests pass

### US-003：提供按归属授权的运行历史

**Description:** As a 用户, I want 查看自己的运行历史 so that 我可以继续查看进行中或已完成任务。

**Acceptance Criteria:**

- [ ] 普通用户的运行列表强制过滤 `created_by = 当前用户`
- [ ] 普通用户读取或控制他人运行返回禁止访问或不存在，不泄露对象信息
- [ ] 管理员可分页查看租户全部运行
- [ ] 支持按状态、工作流和时间筛选
- [ ] 单次运行详情与取消操作执行相同对象级授权
- [ ] 管理员保留暂停、恢复、取消、审批和人工介入能力
- [ ] 真实 PostgreSQL 测试覆盖跨租户与跨用户访问

### US-004：实现结构化输入校验

**Description:** As a 用户, I want 在启动前得到字段级校验 so that 无效输入不会创建运行。

**Acceptance Criteria:**

- [ ] 支持短文本、长文本、数字、单选、多选、布尔和日期字段
- [ ] 主任务始终必填
- [ ] 后端按目标 Version 的 schema 校验必填、类型和枚举值
- [ ] 无效输入返回字段名、错误码和中文消息
- [ ] 校验失败不创建 Run 或事件
- [ ] 同一输入与版本保持现有幂等启动语义
- [ ] Unit and integration tests pass

### US-005：实现工作流中心

**Description:** As a 普通用户, I want 发现并启动已发布工作流 so that 我能选择固定流程完成任务。

**Acceptance Criteria:**

- [ ] 页面展示名称、说明、当前版本、更新时间和我的最近运行状态
- [ ] 支持搜索和分页，列表不超过五列
- [ ] 无数据提示“工作流还是空的”并提供管理员引导
- [ ] 搜索无结果提示“没有找到匹配的工作流”
- [ ] 普通用户看不到草稿和编辑入口
- [ ] Agent 对话页提供跳转到专用执行页的快捷入口
- [ ] Lint and typecheck pass
- [ ] Verify in browser using Stratum E2E workflow

### US-006：实现引导式工作流设计器

**Description:** As a 管理员, I want 使用引导式画布配置 DAG so that 我无需编辑原始 JSON。

**Acceptance Criteria:**

- [ ] 左侧节点库包含 Agent、Skill、MCP、条件和人工审批
- [ ] 画布支持插入、连线、选择、删除、缩放和适应画布
- [ ] 右侧优先展示基础配置，高级设置折叠输入输出映射、超时和重试
- [ ] 保存草稿携带预期修订号
- [ ] 修订冲突保留本地编辑内容并阻止覆盖
- [ ] 未保存修改离开页面时显示确认对话框
- [ ] 移动端不提供画布编辑入口
- [ ] Lint, typecheck and component tests pass
- [ ] Verify in browser using Stratum E2E workflow

### US-007：实现验证、发布与版本查看

**Description:** As a 管理员, I want 在发布前定位配置错误并查看历史版本 so that 已发布工作流可执行且可追溯。

**Acceptance Criteria:**

- [ ] 验证结果包含错误码、中文消息及节点或连线 ID
- [ ] 点击验证错误会聚焦对应画布元素
- [ ] 发布前必须验证当前最新修订
- [ ] 发布使用 `Modal.confirm` 说明版本不可变
- [ ] 发布成功通知不超过 2 秒
- [ ] 历史版本只读展示 DAG 和输入 schema
- [ ] V1 不展示删除、归档或回滚操作
- [ ] Verify in browser using Stratum E2E workflow

### US-008：实现工作流执行页

**Description:** As a 普通用户, I want 通过主任务和附加字段启动工作流 so that 我可以专注于业务目标。

**Acceptance Criteria:**

- [ ] 首屏展示工作流说明、主任务、附加字段和开始按钮
- [ ] 表单由当前发布版本的输入 schema 生成
- [ ] 字段说明使用 `extra`，补充信息使用 `tooltip`
- [ ] 后端字段错误映射到对应表单项
- [ ] 重复提交使用稳定 idempotency key，避免创建重复运行
- [ ] 创建成功后立即导航到运行详情
- [ ] 文件上传不出现在 V1
- [ ] Verify in browser using Stratum E2E workflow

### US-009：实现实时运行详情

**Description:** As a 用户, I want 实时看到 DAG 进度和步骤证据 so that 我知道任务正在做什么以及失败在哪里。

**Acceptance Criteria:**

- [ ] DAG 按运行快照渲染，不读取可变草稿
- [ ] 页面展示总体状态、完成数、当前节点和节点状态
- [ ] 展示经过裁剪的节点输出增量
- [ ] 工具步骤展示工具名、耗时和摘要
- [ ] SSE 使用最后事件序号断线续传且不重复应用事件
- [ ] 连接中断显示重连状态，不将运行标记为失败
- [ ] 节点失败时标红并自动打开错误详情
- [ ] 完成后展示最终结果和折叠步骤摘要
- [ ] 普通用户只看到取消自己运行的操作
- [ ] Verify in browser using Stratum E2E workflow

### US-010：实现管理员运行治理

**Description:** As a 管理员, I want 查看租户全部运行并处理控制点 so that 异常和高风险操作得到人工治理。

**Acceptance Criteria:**

- [ ] 运行中心支持我的运行与租户全部运行视图
- [ ] 页面只展示后端返回的 `available_actions`
- [ ] 暂停、恢复和取消携带 expected generation
- [ ] 审批定位到对应运行、节点和 attempt
- [ ] 人工介入只展示后端允许的解决动作
- [ ] generation 冲突后刷新状态并显示持久错误通知
- [ ] 危险操作使用 `Modal.confirm` 描述后果
- [ ] Verify in browser using Stratum E2E workflow

### US-011：完成真实链路与风险验收

**Description:** As a 发布负责人, I want 验证浏览器、API、数据库和 worker 全链路 so that V1 不以 mock 通过代替真实可用。

**Acceptance Criteria:**

- [ ] 管理员创建、保存、验证并发布工作流
- [ ] 普通用户启动工作流并实时查看节点进度
- [ ] 管理员审批后运行继续并完成
- [ ] 覆盖节点失败、SSE 断线恢复、并发编辑和 generation 冲突
- [ ] 覆盖跨用户读取与控制失败，成功率为 0
- [ ] 覆盖非幂等外部副作用进入人工介入
- [ ] 桌面端完成全流程，移动端完成发现、启动和运行查看
- [ ] `make risk-guardrails` passes
- [ ] Backend race tests and frontend lint/build pass

## 4. Functional Requirements

- FR-1: 系统必须允许管理员创建和显式保存工作流草稿。
- FR-2: 系统必须用预期修订号拒绝过期草稿写入。
- FR-3: 系统必须允许管理员通过引导式画布编辑固定 DAG。
- FR-4: 系统必须验证节点、连线、映射、资源引用和输入 schema。
- FR-5: 系统必须将验证错误定位到节点、连线或输入字段。
- FR-6: 系统必须将已发布版本保存为不可变快照。
- FR-7: 系统必须允许管理员只读查看历史版本。
- FR-8: 系统必须只向普通用户列出已发布工作流。
- FR-9: 系统必须按当前发布版本生成执行表单。
- FR-10: 系统必须在输入校验失败时拒绝创建运行。
- FR-11: 系统必须记录运行发起用户。
- FR-12: 系统必须限制普通用户只访问自己的运行。
- FR-13: 系统必须允许管理员访问租户内全部运行。
- FR-14: 系统必须分页查询工作流、版本和运行。
- FR-15: 系统必须允许普通用户取消自己的非终态运行。
- FR-16: 系统必须允许管理员执行后端声明的运行控制动作。
- FR-17: 系统必须按运行快照渲染 DAG。
- FR-18: 系统必须通过可续传事件更新运行状态。
- FR-19: 系统必须展示经过安全裁剪的输出增量和工具步骤。
- FR-20: 系统必须把审批和人工介入定位到具体节点。
- FR-21: 系统必须在 SSE 断线时区分连接状态与业务状态。
- FR-22: 系统必须在权限、修订或 generation 冲突时失败关闭。

## 5. Non-Goals

- 动态生成或运行时修改 DAG
- 多人实时协同与离线编辑
- 文件输入
- 工作流删除、归档、复制和版本回滚
- 定时、Webhook 和事件触发
- 任意失败节点的人工重试
- 跨租户模板市场
- 成本、SLA 报表和运行重放
- 移动端画布编辑

## 6. Design Considerations

- 普通用户界面聚焦任务目标，不暴露映射、重试、超时和资源 ID。
- 管理员设计器采用节点库、画布和节点配置三栏结构。
- 页面使用 Ant Design 现有组件和项目统一通知规范。
- 画布节点必须有稳定尺寸，状态变化不得导致布局跳动。
- 节点状态不能只依赖颜色，还需图标或文字语义。
- 列表、表单和运行页均提供 loading、empty、success 和 persistent error 状态。

## 7. Technical Considerations

- 保持 handler → application → domain/port → infrastructure 分层。
- 新增 tenant repository 方法必须显式接收 tenant ID 并经过 `execTenant`。
- 对象级授权在 application 层执行并 fail closed。
- Version 冻结 DAG 与输入 schema；Run 冻结版本快照并记录发起人。
- SSE 复用现有事件序号，新增展示事件不得记录密钥、原始上游响应或敏感参数。
- 前端普通请求复用唯一 Axios client，流式请求复用 `streamApiEvents`。
- 具体画布库在技术 SPEC 中选定，PRD 不锁定实现库。

## 8. Success Metrics

- 管理员无需 JSON 完成五类节点的创建、验证和发布。
- 三节点工作流首次创建并发布的中位时间不超过 10 分钟。
- 普通用户在一个页面内完成输入并启动。
- 正常测试环境中事件展示延迟 P95 不超过 2 秒。
- 正常网络下详情重载后 3 秒内恢复持久化状态。
- 跨用户读取和控制成功率为 0。
- 新增业务逻辑和授权逻辑覆盖率不低于 80%。

## 9. Open Questions

产品范围不存在阻塞性开放问题。测试环境规格、事件裁剪上限和具体画布库由后续技术 SPEC 明确。
