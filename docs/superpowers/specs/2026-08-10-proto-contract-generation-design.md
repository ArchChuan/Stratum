# Proto 契约生成设计(前后端参数约束)

> 状态:已获批(设计逐节通过)+ 三路并行评审(架构/安全/测试)后修订
> 日期:2026-08-10(修订 v2)

## 1. 背景与目标

现状:HTTP 请求/响应 DTO 手写于 `api/http/dto/`(71 个 struct,352 个字段),前端契约类型手写分散于 `web/src/modules/*/model`、`api` 文件。两侧字段定义相互独立,参数契约无单一事实源,前后端偏移只能靠人工发现。

引入 Google Protocol Buffers 契约文件(`.proto`),作为前后端 HTTP JSON 参数契约的**唯一事实源**,生成后端 Go struct 与前端 TS 类型。

**核心目标(成功标准)**:

1. proto = HTTP JSON 参数契约唯一事实源,前后端都从它生成。
2. 后端约束:proto 字段变更 → 生成 struct 变更 → 由 **DTO parity 测试**与 **golden 语义等价测试**双重守卫失败(见 §8)。任何一端想改参数,必须改 proto。
3. 前端约束:proto 变更 → 生成 TS interface 变更 → 引用旧字段处 TS **编译失败**(CI 生成后跑 `fe-typecheck`(tsc --noEmit),不一致即红)。前端想加参数,必须改 proto。
4. 序列化语义保持 encoding/json + 现有 json 字段名,150 golden 文件**语义等价零变化**,零破坏迁移。

## 2. 非目标

- 不做 gRPC、不做 HTTP 路由/Gin 框架改造,proto 只生成类型。
- 不引入 protobuf runtime(`.pb.go` 不用,生成物是纯 Go struct / 纯 TS interface)。
- 不做前端运行时校验(zod schema 保留现状),proto 只生成编译期类型。
- **不迁移前端 UI 类型**(TableColumns、PageProps 等非契约类型)。
- **不迁移非 JSON 契约**:multipart 上传(`*multipart.FileHeader`,rag.go UploadDocumentRequest)、query 参数(`form:"..."`,evaluation.go EvaluationCenterQuery)——proto 无法表达,继续手写,proto 契约只覆盖 JSON body。

## 3. 架构总览

```
proto/<domain>/<entity>.proto        # 契约定义(唯一事实源)
        │
        └─► buf generate ──► protoc-gen-ginstruct(自研,双输出) ──► api/http/dto/gen/*.go
                                                              └─► web/src/services/gen/*.ts
```

- **protoc-gen-ginstruct 一个插件双输出**:CodeGeneratorResponse 同时携带 .go 与 .ts 文件,Go/TS 共享同一解析、命名、类型映射逻辑——字段名 = proto 字段名 = json tag 逐字节(含存量 `created_at` 等 snake_case),94 个 snake_case 存量字段两端零分叉风险。
- 生成物是普通 Go struct + 现有 json tag,天然 encoding/json 语义;TS 是 `export interface`(无运行时、无序列化函数,axios 直接消费 JSON)。
- 生成物与 protobuf runtime 完全解耦,类型映射表自定义(见 §5)。

## 4. 工具链与生成物管理

- **buf**:`buf.yaml` + `buf.gen.yaml`;`protoc-gen-ginstruct` 为本地插件(`plugins.local` 指向 PATH 中二进制),插件版本由 go.mod 锁定;buf 版本由 CI/本地固定(build-action 或 brew 锁定,见 CI 清单)。
- **插件协议**:标准 CodeGeneratorRequest(编译后的 FileDescriptorProto)经 stdin 输入、CodeGeneratorResponse 经 stdout 输出;编译是 buf 做的,插件不解析源文件。**protocompile 仅用于生成器单测**(从 .proto 文本构建 descriptor)。跨 proto 引用配 `strategy: all`。
- **前端零 npm 生成依赖**:TS 由同一 Go 插件产出,无 ts-protoc-gen/ts-proto 第三方包。

**生成物管理(不入 git)**:

- `api/http/dto/gen/`、`web/src/services/gen/` 进 `.gitignore`,git 只审计 proto 契约。
- Makefile:所有 `build/test/lint/fe-build/fe-typecheck/check/code-quality` 入口目标前置依赖 `proto-gen`(幂等、秒级)。
- **CI 改造清单**(生成物不入 git 后,以下 job 全部前置 `make proto-gen`,并安装锁定版本 buf):
  - `test` job(直敲 `go test -v -race ./...`)、`lint` job(golangci-lint)、`contract` job(make contract-enforce)、`build` job、`frontend` job(fe-lint/fe-typecheck/fe-build)——现有 7 个 job 均无 setup-buf,spec 必须列明每个受影响 job。
  - 脚本链:`scripts/quality/risk-regression-guard.sh`、`incremental-go-test.sh` 等 10+ 个直调 `go test` 的脚本,经 make 入口调用,前置 proto-gen。
- **worktree 工作流**:项目强制 `scripts/new-worktree.sh` 创建隔离 worktree,每个新 worktree 是干净 checkout——脚本在创建后自动执行一次 `make proto-gen`,避免首次构建必失败。
- 已知约束:开发者绕过 make 直敲 `go test` 且未生成时 import 编译失败;文档写明,与"生成物不入 git"配套接受。

## 5. 类型映射规则

| 现有 Go 类型 | proto 写法 | 生成 Go 类型 | 生成 TS 类型 | JSON 字节 |
|---|---|---|---|---|
| string / int64 / bool | string / int64 / bool | 同左 | string / number / boolean | 同 |
| int(Page/Total/TopK 等) | int32 | int32 | number | 同(数字) |
| float64 | double | float64 | number | 同 |
| float32(rag ScoreThreshold) | float | float32 | number | 同 |
| time.Time(22 处) | google.protobuf.Timestamp | time.Time(不用 timestamppb,仅借类型声明) | string | 同(RFC3339) |
| *time.Time | optional Timestamp | *time.Time | string \| null | 同 |
| time.Duration(mcp_config Timeout) | int64(JSON 是纳秒整数,不用 proto Duration——其 JSON 语义是 "3.5s" 字符串,不兼容) | time.Duration | number | 同 |
| map[string]any / map[string]interface{} | google.protobuf.Struct | map[string]any | Record<string, unknown> | 同 |
| map[string]string(Env/Headers) | map<string, string> | map[string]string | Record<string, string> | 同 |
| any / []any | google.protobuf.Value / repeated Value | any / []any | unknown / unknown[] | 同 |
| json.RawMessage | google.protobuf.Value | json.RawMessage(encoding/json 透传) | unknown | 同 |
| []byte | bytes | []byte | string | 同 |
| *bool 等指针 | optional bool | *bool | boolean \| null | 同 |
| 嵌套 message([]TenantResponse 等) | message / repeated message | T / []T(非指针,匹配现状) | T / T[] | 同 |

**规则收紧**:

- DTO 的 string 字段一律 proto string,禁止 proto enum(enum 生成 int32 破坏字节);required 消息字段禁止(proto3 无),required 语义只经 binding tag 表达。
- 同一 message 内字段 PascalCase 化后不得冲突(生成器校验报错)。
- camelCase 字段名违反 proto 惯例,buf lint `FIELD_LOWER_SNAKE_CASE` 需配置豁免;阶段 0 验证工具链容忍度。
- **int32 溢出风险**:int→int32 后,运行时值若超 2^31(长数字 ID)在序列化/转换点溢出。存量 int 字段均为 page/total/count 类(值 < 2^31),parity 测试断言映射,handler 显式转换,记录为已知风险不阻断。

## 6. 参数校验、逃生舱与敏感字段

- **binding 校验**:proto 字段注释 `// @binding: required,max=255` → 生成 `binding:"required,max=255"`;`@binding` 格式错误 → 生成器非零退出(fail-closed)。**内容级漂移由 parity 测试守护**(手写 DTO 92 个 binding tag / 81 个 required / dive 嵌套逐字段比对,见 §8)。
- **逃生舱** `// @gotype: <GoType>`:直接指定生成 Go 类型,解决 proto 无法表达的跨包类型。
  - **白名单校验**:值必须通过 Go 类型表达式解析,且包前缀限于内部白名单(`internal/` 下 domain 包);不在白名单 → 非零退出。快照单测覆盖。
  - **实际规模(~30 处/6 文件,非最初估计 3 处)**:collaboration、mcp_config、resource_change_proposal、scheduled_task、operation_proposal、workflow 整实体嵌入(`domain.ScheduledTask`、`collabdomain.Collaboration`、`domain.OperationProposal`、`domain.ResourceChangeProposal`、`workflowdomain.Spec` 等)。这些字段的"真相源"仍是 domain 包,proto 对其是黑盒引用——约 8.5% 字段不在契约事实源内,声明为已知边界。
- **DTO 转换器/方法归属**(迁移后无家可归问题):`mcp_config.go:32 ServerConfig()`(业务校验,拒绝 system_key)、`mcp_config.go:72 NewMCPServerConfigResponse()`(含 `filterMCPConfigValues`/`authCredentialConfigured` 敏感过滤)、`workflow.go:31 RunInput()`(带业务逻辑的成员方法)、6 个 `To*Response` 转换器。
  - 方案:gen 包只放生成 struct;转换器与业务方法迁移到同包**手写文件**(`api/http/dto/gen/convert.go` 或原包保留 `*_manual.go`),含敏感过滤逻辑的转换器逐行保留并受既有 golden 守护;迁移时逐函数确认归属,禁止删除。
- **敏感字段策略**:
  - 请求侧凭证字段(如 `Auth *domain.AuthConfig`,含 Token/APIKeyValue/OAuth2ClientSecret)走 `@gotype` 黑盒,不进 proto 契约。
  - 响应侧凭证值字段禁止进入 proto 契约(现状 `MCPAuthConfigResponse` 只暴露 `CredentialConfigured bool`);**golden 机械断言**:扫描 150 个 golden 响应体,禁止出现 token/secret/api_key_value/client_secret 等凭证值字段名(新增 CI 断言,把"响应剔除凭证"从人工 review 变强制)。

## 7. 前端迁移范围

- 只替换 **API 请求/响应 JSON 契约类型**:`client.ts` 调用泛型、`model/*.ts`、`api/*.ts` 中的契约类型(替换后引用改 import `services/gen`)。
- **命名一致性**:TS interface 字段名 = proto 字段名(json tag)逐字节,自研双输出保证——`created_at` 等 94 个 snake_case 存量字段在 TS 侧原样保留,与后端 JSON key 一致,无运行时 undefined 风险。
- 不迁移:UI 类型、zod schema(`ExperimentResponse = z.infer<...>` 等 16 个文件的手写契约类型**声明暂缓**,与 proto 双源并存,演进项不做)。
- 前端捕获手段统一表述为 **fe-typecheck(tsc --noEmit)**:CI frontend job 现跑 fe-lint/fe-typecheck/fe-build,typecheck 才是编译期约束的真实守卫。

## 8. 迁移阶段与验证

**阶段 0 — 工具链落地**(不迁移任何 DTO):

- `cmd/protoc-gen-ginstruct`(Go+TS 双输出)+ `buf.yaml`/`buf.gen.yaml`(lint 豁免配置)+ Makefile `proto-gen` 目标(挂为全部入口前置依赖)+ worktree 脚本自动生成。
- 生成器单测:protocompile 从 .proto 文本构建 descriptor;文本快照 + 类型映射表全覆盖断言 + `@binding` 解析表驱动用例(含内容变化 → 输出变化)+ `@gotype` 白名单用例。
- **DTO parity 测试(建立,最高优先级)** `api/http/dto/gen/parity_test.go`:对每个 proto message 用反射比对生成 struct 与手写 struct——json tag 集合(逐字段)、Go 类型映射、binding tag 集合(含嵌套 dive 字段)。迁移期断言"生成 == 手写";删手写后退化为"生成 == 批准时快照"(仅经显式流程更新)。
- 验收:试点 domain 生成物与手写 DTO parity 全绿。

**阶段 1 — 试点 domain**(collaboration,3 struct 最小):

- 写 `proto/collaboration/collaboration.proto` → 生成 → 删手写 struct → handler/service 引用改 `gen.` 包(编译器全量引导)。
- 验收:parity 全绿、150 golden 语义等价零 diff、`go build`/`go vet` 干净、前端 fe-typecheck/fe-build 通过、**真实链路冒烟一次**(`make test-verify-local` 或 stateful short,不依赖 golden stub)。

**阶段 2 — 批量迁移**(每批 = 1 个 proto 文件 + 对应手写删除):

- 每批独立 commit,CI 全绿再进下一批;每批验收同阶段 1(parity + golden 语义等价 + build/vet + 前端 typecheck + 真实链路冒烟)。
- 前端同步:生成 TS interface 替换手写契约类型,`fe-typecheck` 验证。

**阶段 3 — 收尾**:

- **残留守卫**:guard 脚本断言 `api/http/dto/` 无残留手写 struct、无旧包 import、`web/src/modules/*/model` 无残留契约类型,防半迁移态混入 main。
- 删净手写 DTO;CLAUDE.md 记录"proto 是契约事实源 + `make proto-gen` 约束 + 直敲 `go test` 前需先生成"。
- 全量 `go test -race` + `make test-verify-before-pr` 终验。

**失败暴露**:生成器遇 proto 语法错、`@binding` 格式错、`@gotype` 白名单外、PascalCase 冲突、映射表未覆盖类型 → 非零退出 + 明确报错,禁止静默降级;CI 失败即阻断,无伪成功路径。

## 9. 验收口径说明(评审修订)

- **golden 是行为快照,不是结构快照**:`contract_test.go:563` 的 `jsonEquivalent` 是反序列化后 `reflect.DeepEqual` 语义等价比较(字段顺序、数字 1.0 vs 1 不敏感);stub 返回空骨架 + omitempty 零值不落 JSON,零值字段改名/删除 golden 无感知。
- 因此"零破坏"证明由**两层守卫**承担:
  1. **DTO parity 测试**(§8 阶段 0)——字段名/类型/binding 逐字段结构等价,直接守护目标 2;
  2. **golden 语义等价**——端到端行为回归,守护目标 2 的运行时路径。
- 成功标准 2/4 的表述以本口径为准:golden 零变化是"语义等价零变化",结构等价由 parity 承担。

## 10. 风险

- **类型传导**:int→int32、map 泛型调整等类型变化传导到 handler/service 引用点,机械改动量大但由编译器全量引导;int32 溢出风险记录于 §5。
- **生成器维护**:protoc-gen-ginstruct 是唯一自研组件(Go+TS 双输出 ~400 行),查证无现成"纯 Go struct + camelCase + binding"与"字段名逐字节一致 TS"生成器,自研是被迫路径;范围收敛到字段声明与 tag 输出,确定性可测。
- **双 stub 维护**(改进项,不阻断):contract_test.go 与 record-contracts.go 各自复制 stub repo,迁移改 DTO 后需同步;后续抽公共 stub 包。
- **zod 双源**:16 个前端模块的手写 zod schema 契约类型与 proto 并存,未来可演进为 proto → zod 生成,本阶段不做。
- **凭证字段黑盒**:`@gotype` 引用的 domain 凭证类型不在契约事实源内,是已知边界;响应侧凭证值字段由 golden 机械断言(§6)守护。
