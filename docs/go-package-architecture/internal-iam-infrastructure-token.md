# internal/iam/infrastructure/token

实现 IAM token 端口，使用 RS256 签发和校验 access token，并在 JWT claims 与领域 claims 间转换。

完整导入路径：`github.com/byteBuilderX/stratum/internal/iam/infrastructure/token`

实现强制校验 RSA 签名方法；测试覆盖签发、解析、过期和非法签名。
