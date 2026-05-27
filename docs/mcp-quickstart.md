# MCP 快速开始

## 5 分钟快速开始

### 1. 配置 MCP 服务器

编辑 `config/mcp.yaml`：

```yaml
mcp:
  servers:
    - id: "my-server"
      name: "My MCP Server"
      transport: "http"
      url: "http://localhost:3000"
      timeout: 30s
```

### 2. 初始化系统

```go
manager := mcp.NewClientManager(logger, nil)
registry := mcp.NewMCPSkillRegistry(manager, logger)
handler := handler.NewMCPHandler(registry, manager, logger)
handler.RegisterRoutes(router)
```

### 3. 查询可用工具

```bash
curl http://localhost:8080/api/v1/mcp/servers
curl http://localhost:8080/api/v1/mcp/skills
```

### 4. 执行工具

```bash
curl -X POST http://localhost:8080/api/v1/mcp/tools/my-tool/execute \
  -H "Content-Type: application/json" \
  -d '{"param": "value"}'
```

## 完整示例

### 创建 MCP 服务器

```javascript
// server.js
const http = require('http');

const server = http.createServer((req, res) => {
  if (req.method === 'POST' && req.url === '/rpc') {
    let body = '';
    req.on('data', chunk => body += chunk);
    req.on('end', () => {
      const request = JSON.parse(body);
      
      if (request.method === 'tools/list') {
        res.writeHead(200, {'Content-Type': 'application/json'});
        res.end(JSON.stringify({
          result: [
            {
              name: 'greet',
              description: 'Greet someone',
              inputSchema: {
                type: 'object',
                properties: {
                  name: {type: 'string'}
                }
              }
            }
          ]
        }));
      } else if (request.method === 'tools/call') {
        const name = request.params.arguments.name;
        res.writeHead(200, {'Content-Type': 'application/json'});
        res.end(JSON.stringify({
          result: `Hello, ${name}!`
        }));
      }
    });
  } else if (req.url === '/health') {
    res.writeHead(200);
    res.end('OK');
  }
});

server.listen(3000, () => console.log('MCP server running on port 3000'));
```

### 使用 MCP 技能

```go
// 获取所有技能
skills := registry.GetAllSkills()
for _, skill := range skills {
    fmt.Printf("Skill: %s (%s)\n", skill.GetName(), skill.GetID())
}

// 执行技能
result, err := registry.ExecuteSkill("mcp:my-server:greet", map[string]interface{}{
    "name": "World",
})
if err != nil {
    log.Fatal(err)
}
fmt.Println(result) // Output: Hello, World!
```

## 常见问题

**Q: 如何添加新的 MCP 服务器？**
A: 在 `config/mcp.yaml` 中添加新的服务器配置，然后重启应用。

**Q: 支持哪些传输方式？**
A: stdio（本地命令）、SSE（事件流）、HTTP（REST API）。

**Q: 如何调试连接问题？**
A: 启用 DEBUG 日志级别，查看详细的连接和错误信息。

**Q: 缓存如何工作？**
A: 工具和资源列表被缓存以减少网络调用，TTL 后自动过期。

**Q: 如何处理长时间运行的工具？**
A: 在配置中增加 `timeout` 值，或在代码中使用 context 超时。
