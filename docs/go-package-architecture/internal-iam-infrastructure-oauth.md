# internal/iam/infrastructure/oauth

该包实现 GitHub OAuth 端口，负责授权码换取访问令牌并读取 GitHub 用户资料。

完整导入路径：`github.com/byteBuilderX/stratum/internal/iam/infrastructure/oauth`

```mermaid
flowchart LR
  github["github.go<br/>GitHubClient / githubUserJSON<br/>ExchangeCode·GetUser·ClientID"]
  port["internal/iam/domain/port<br/>GitHubOAuthClient / GitHubProfile"]
  constants["pkg/constants<br/>HTTP 超时"]
  stdlib["标准库：net/http + net/url<br/>表单编码与手工 code exchange"]
  api["GitHub OAuth 与 User API"]
  tests["测试汇总<br/>github_test.go"]
  github -.实现.-> port
  github --> constants
  github --> stdlib --> api
  github -.-> tests
```

`NewGitHubClient` 配置端点和带超时的 HTTP 客户端；`ExchangeCode` 用 `url.Values` 组装表单并通过 `net/http` 手工交换授权码，`GetUser` 再读取用户 API，最终转换为端口定义的 `GitHubProfile`。
