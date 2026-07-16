# Go 包代码架构图设计

## 目标

为 stratum 项目 `internal/...` 与 `pkg/...` 下的全部 Go 包生成独立的 Mermaid 代码架构图，帮助开发者按 bounded context、DDD 分层和公共基础包快速理解代码结构。

## 范围

- 以 `go list ./internal/... ./pkg/...` 的结果作为包清单的唯一来源。
- 包含 `internal/` 和 `pkg/` 下所有可构建的包及其子包。
- 不为 `api/`、`cmd/`、`config/`、`web/`、`.worktrees/`、`vendor/` 或 `tmp/` 生成包图。
- 不修改任何 Go 源码、配置、迁移或测试。

## 输出结构

所有文件写入 `docs/go-package-architecture/`：

- `README.md`：总索引，按 `internal` bounded context 和 `pkg` 基础设施分类。
- 每个 Go 包对应一个 Markdown 文件，文件名由相对导入路径转换而来，例如：
  - `internal-agent-application.md`
  - `internal-memory-domain-port.md`
  - `pkg-storage-postgres.md`

索引中的每个包都链接到对应 Markdown 文件，并显示包的相对导入路径与一句话职责说明。

## 单包图内容

每个包文档包含包名、完整导入路径、职责摘要和一张 Mermaid `flowchart`。图中按实际代码展示：

1. 包内非测试 Go 文件。
2. 核心导出结构体、接口、函数及重要的未导出实现类型。
3. 类型之间的接口实现、组合、构造和主要调用关系。
4. 当前包直接导入的 stratum 项目内包。
5. 对理解架构有价值的关键外部依赖；标准库和低价值工具依赖不逐项展开。
6. 测试文件仅汇总为测试节点，不展开每个测试函数。

当包内容过多时，节点按文件或职责分组，优先保证图可读；不会为了塞入全部函数而生成不可读的超大图。空壳包或仅含少量声明的包仍生成文档，并准确展示其实际内容。

## 分析方式

- 使用 `go list -json ./internal/... ./pkg/...` 获取包、源文件、测试文件和直接导入关系。
- 完整读取每个目标包的非测试源码，提取类型声明、接口、构造函数、主要公开函数和架构关系。
- 结合 `docs/agent/architecture.md` 与各模块规则校验 bounded context 和 DDD 层级描述。
- Mermaid 节点标识使用稳定、安全的内部 ID，显示文本对特殊字符进行转义，确保 Markdown 渲染器可解析。

## 验证标准

1. 索引包数与 `go list ./internal/... ./pkg/...` 完全一致。
2. 每个目标包恰好有一个对应 Markdown 文件，索引不存在断链。
3. 每个文档恰好包含一张 Mermaid 图，且代码块闭合。
4. Mermaid 图中的源码文件和项目内依赖均来自当前仓库，不虚构类型或关系。
5. 使用 Mermaid CLI（若已安装）批量渲染校验；若未安装，则执行结构检查并明确记录未完成渲染验证及缺失工具。
6. 最终仅新增或修改 `docs/go-package-architecture/` 及本设计文档。

## 非目标

- 不生成系统级部署图、时序图或前端架构图。
- 不评价或重构现有包边界。
- 不生成 API、命令入口和配置包的架构图。
- 不自动提交最终生成的架构文档，除非用户另行要求。
