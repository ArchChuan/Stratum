# JWT Self-Mint 模板

需要 Bearer token 时，从 `.env` 读取 `JWT_PRIVATE_KEY_PEM` 自签即可，无需走登录流程。

两种方式任选：Go（推荐，项目已有工具链）或 Python（零文件，PyJWT 需已安装）。

## Go 临时脚本模板

文件名 `tmp-e2e-xxx.go`，build tag `ignore`：

```go
//go:build ignore

package main

import (
 "crypto/x509"
 "encoding/pem"
 "fmt"
 "os"
 "strings"
 "time"

 "github.com/golang-jwt/jwt/v5"
)

func main() {
 // 从 .env 读取私钥（set -a; . ./.env; set +a 已注入到环境）
 raw := os.Getenv("JWT_PRIVATE_KEY_PEM")
 raw = strings.ReplaceAll(raw, `\n`, "\n")
 block, _ := pem.Decode([]byte(raw))
 key, _ := x509.ParsePKCS1PrivateKey(block.Bytes)

 now := time.Now()
 token, _ := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
  "sub":         "<user_id>",
  "tid":         "<tenant_id>",
  "role":        "member",
  "global_role": "user",
  "system_role": "user",
  "jti":         fmt.Sprintf("e2e-%d", now.UnixNano()),
  "iat":         now.Unix(),
  "exp":         now.Add(30 * time.Minute).Unix(),
 }).SignedString(key)

 // 使用 token 调用 API（不打印 token 本身）
 // req, _ := http.NewRequest("POST", "http://localhost:8080/...", body)
 // req.Header.Set("Authorization", "Bearer "+token)
 _ = token
}
```

## Python + curl 快速模板

不落盘，适合一次性验证。若 PyJWT 未安装，改用上方 Go 模板：

```bash
python3 - <<'PY'
import json
import re
import subprocess
import sys
import time
import uuid
from pathlib import Path

import jwt

env_text = Path(".env").read_text()
# 下面的正则仅用于从本地 .env 提取私钥；本身不含任何密钥。
key = re.search(
    r'JWT_PRIVATE_KEY_PEM="(-----BEGIN RSA PRIVATE KEY-----.*?-----END RSA PRIVATE KEY-----)"',  # gitleaks:allow
    env_text,
    re.S,
).group(1)

tenant_id = "<tenant_id>"
user_id = "<user_id>"
agent_id = "<agent_id>"
now = int(time.time())
token = jwt.encode({
    "tid": tenant_id,
    "sub": user_id,
    "role": "owner",
    "global_role": "global_admin",
    "system_role": "admin",
    "iat": now,
    "exp": now + 3600,
    "jti": str(uuid.uuid4())[:8],
}, key, algorithm="RS256")

payload = {
    "query": "请完成一次端到端验证，并在需要时调用可用工具。",
    "options": {"maxSteps": 4, "timeout": 120},
}

res = subprocess.run([
    "curl", "-sS", "-w", "\nHTTP_STATUS:%{http_code}\n",
    "-X", "POST", f"http://localhost:8080/agents/{agent_id}/execute",
    "-H", f"Authorization: Bearer {token}",
    "-H", "Content-Type: application/json",
    "--data", json.dumps(payload, ensure_ascii=False),
], text=True, capture_output=True, timeout=180)

print(res.stdout)
if res.stderr:
    print(res.stderr, file=sys.stderr)
PY
```

## 关键约定

- claims 字段名必须与 `internal/iam/application/jwt_service.go` 中的 `jwtAccessClaims` 一致（`tid`/`role`/`global_role`/`system_role`）
- 私钥格式：`x509.ParsePKCS1PrivateKey`（PKCS#1 RSA）
- `.env` 中换行符可能是字面 `\n`，需 `strings.ReplaceAll` 转义
- 不得打印 token、私钥或任何原始凭据
- claims 的角色值按场景调整：管理员 API 用 owner/admin，普通用户 API 用 member/user
