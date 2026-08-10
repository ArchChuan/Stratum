# Proto 契约生成设计(前后端参数约束)

> 状态:已获批(brainstorming 三节设计逐节通过)
> 日期:2026-08-10

## 1. 背景与目标

现状:HTTP 请求/响应 DTO 手写于 `api/http/dto/`(~90 struct),前端契约类型手写分散于 `web/src/modules/*/model`、`api` 文件。两侧字段定义相互独立,参数契约无单一事实源,前后端偏移只能靠人工发现。

引入 Google Protocol Buffers 契约文件(`.proto`),作为前后端 HTTP 参数契约的**唯一事实源**,生成后端 Go struct 与前端 TS 类型。

**核心目标(成功标准)**:

1. proto = HTTP 参数契约唯一事实源,前后端都从它生成。
2. 后端约束:proto 字段变更 → 生成 struct 变更 → 150 个 golden 字节 diff → `contract_test.go` 失败。任何一端想改参数,必须改 proto 并让 golden 字节级通过。
3. 前端约束:proto 变更 → 生成 TS interface 变更 → 引用旧字段处 TS 编译失败(CI 生成后跑 `fe-lint`/`fe-build`,不一致即红)。前端想加参数,必须改 proto。
4. 序列化语义保持 encoding/json + 现有 json 字段名,150 golden 字节零变化,零破坏迁移。

## 2. 非目标

- 不做 gRPC、不做 HTTP 路由/Gin 框架改造,proto 只生成类型。
- 不引入 protobuf runtime(`.pb.go` 不用),生成物是纯 Go struct。
- 不做前端运行时校验(zod schema 保留现状),proto 只生成编译期类型。
- 不迁移前端 UI 类型(TableColumns、PageProps 等非契约类型)。

## 3. 架构总览

```
proto/<domain>/<entity>.proto        # 契约定义(唯一事实源)
        │
        ├─► buf generate ──► protoc-gen-ginstruct(自研) ──► api/http/dto/gen/*.go
        └─► buf generate ──► protoc-gen-typescript(现成) ──► web/src/services/gen/*.ts
```

- proto 字段名 = 现有 json tag 逐字节(含存量 `created_at`、`idempotency_key` 等 snake_case;json tag 原样输出,Go 字段名 PascalCase 化)。
- 生成物是普通 Go struct + camelCase(或存量)json tag,天然 encoding/json 语义。
- 生成物与 protobuf runtime 完全解耦,类型映射表自定义(见 §5)。

## 4. 工具链与生成物管理

- **buf**:`buf.yaml` + `buf.gen.yaml`,插件版本由 `buf.lock` 锁定。
- **protoc-gen-ginstruct**(自研,~300 行):基于 `github.com/bufbuild/protocompile` 解析 proto,输入 FileDescriptor → 输出 Go struct 文本;确定性纯函数,单测用文本快照。
- **protoc-gen-typescript**(improbable-eng/ts-protoc-gen):纯 interface 输出,配合 axios 直接消费 camelCase JSON;不用 protoc-gen-es(其 fromJson 为 protojson 语义,与 encoding/json 输出不兼容)。

**生成物管理(不入 git)**:

- `api/http/dto/gen/`、`web/src/services/gen/` 进 `.gitignore`,git 只审计 proto 契约。
- Makefile:所有 `build/test/lint/fe-build` 入口目标前置依赖 `proto-gen`(幂等、秒级),开发者 checkout 后无需手动步骤。
- CI 流水线 job 第一步 `make proto-gen`(buf 用 bufbuild/setup-buf 安装;protoc-gen-ginstruct 本地 `go build`,与 go.mod 同一依赖解析,版本天然一致),然后 build/test/lint 照常。
- 已知约束:开发者直敲 `go test ./...`(绕过 make)时,若生成物不存在会 import 编译失败;文档写明,与"生成物不入 git"配套接受。

## 5. 类型映射规则

| 现有 Go 类型 | proto 写法 | 生成类型 | JSON 字节 |
|---|---|---|---|
| string / int64 / bool | string / int64 / bool | 同左 | 同 |
| int(Page/Total/TopK 等) | int32 | int32 | 同(数字) |
| float64 | double | float64 | 同 |
| time.Time | google.protobuf.Timestamp | time.Time(不用 timestamppb,仅借类型声明) | 同(RFC3339) |
| *time.Time | optional Timestamp | *time.Time | 同 |
| map[string]any / map[string]interface{} | google.protobuf.Struct | map[string]any | 同 |
| any / []any | google.protobuf.Value / repeated Value | any / []any | 同 |
| json.RawMessage | google.protobuf.Value | json.RawMessage(encoding/json 透传) | 同 |
| []byte | bytes | []byte | 同 |
| *bool 等指针 | optional bool | *bool | 同 |
| 嵌套 message([]TenantResponse 等) | message / repeated message | T / []T(非指针,匹配现状) | 同 |

**规则收紧**:

- DTO 的 string 字段一律 proto string,禁止 proto enum(enum 生成 int32 破坏字节);required 消息字段禁止(proto3 无),required 语义只经 binding tag 表达。
- 同一 message 内字段 PascalCase 化后不得冲突(现状已保证,生成器校验报错)。

## 6. 参数校验与逃生舱

- **binding 校验**:proto 字段注释 `// @binding: required,max=255` → 生成 `binding:"required,max=255"`,gin 校验语义零变化。`@binding` 格式错误 → 生成器非零退出。
- **逃生舱** `// @gotype: []domain.ProposalEvent`:直接指定生成 Go 类型,解决 proto 无法表达的跨包类型(operation_proposal 3 处引用 domain 实体),序列化字节不变。

## 7. 前端迁移范围

- 只替换 **API 请求/响应契约类型**:`client.ts` 调用泛型、`model/*.ts`、`api/*.ts` 中的契约类型(替换后引用改 import `services/gen`)。
- 不迁移:UI 类型、zod schema(`ExperimentResponse = z.infer<...>` 保留,与 proto 契约双源并存,演进项不做)。

## 8. 迁移阶段与验证

**阶段 0 — 工具链落地**(不迁移任何 DTO):

- `cmd/protoc-gen-ginstruct` + `buf.yaml`/`buf.gen.yaml` + Makefile `proto-gen` 目标(挂为 build/test/lint/fe-build 前置依赖)。
- 生成器单测:文本快照 + 类型映射表全覆盖断言。
- 验收:选最小 domain 试点生成,生成物与手写 DTO 逐字段 diff 为零。

**阶段 1 — 试点 domain**(collaboration,3 struct 最小):

- 写 `proto/collaboration/collaboration.proto` → 生成 → 删手写 struct → handler/service 引用改 `gen.` 包(编译器全量引导)。
- 验收:150 golden 字节零 diff、`go build`/`go vet` 干净、前端 build 通过。

**阶段 2 — 批量迁移**(每批 = 1 个 proto 文件 + 对应手写删除):

- 每批独立 commit,CI 全绿再进下一批;每批验收同阶段 1。
- 前端同步:生成 TS interface 替换手写契约类型,`fe-lint`/`fe-build` 验证。

**阶段 3 — 收尾**:

- 删净手写 DTO;CLAUDE.md 记录"proto 是契约事实源 + `make proto-gen` 约束 + 直敲 `go test` 前需先生成"。
- 全量 `go test -race` + `make test-verify-before-pr` 终验。

**失败暴露**:生成器遇 proto 语法错、`@binding` 格式错、PascalCase 冲突、映射表未覆盖类型 → 非零退出 + 明确报错,禁止静默降级;CI 失败即阻断,无伪成功路径。

## 9. 风险

- **类型传导**:int→int32、map 泛型调整等类型变化传导到 handler/service 引用点,机械改动量大但由编译器全量引导,无歧义。
- **生成器维护**:protoc-gen-ginstruct 是唯一自研组件,查证无现成"纯 Go struct + camelCase + binding"生成器,自研是被迫路径;范围收敛到字段声明与 tag 输出,确定性可测。
- **zod 双源**:前端 zod schema 与 proto 契约并存,未来可演进为 proto → zod 生成,本阶段不做。
