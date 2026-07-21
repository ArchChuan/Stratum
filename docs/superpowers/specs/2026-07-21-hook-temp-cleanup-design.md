# Hook 临时目录清理放行设计

## 目标

调整 Stratum 用户级 worktree policy，使 agent 即使从主工作区发起命令，也能执行以下操作：

- 使用 `rm -f` 或 `rm -rf` 清理一个或多个精确的 `/tmp/<name>` 路径，不限制 Stratum 前缀。
- 通过 SSH 执行同样受限的 `/tmp` 临时路径清理。
- 使用 `curl`、`pgrep`、`ps`、`ls`、`test` 等命令完成只读诊断和恢复检查。

主工作区仍保持只读，不能借助这些例外修改 Stratum 仓库内容。

## 安全边界

本地临时清理只接受完整命令匹配。允许的目标必须是 `/tmp/` 下的直接或嵌套具体路径，且至少包含
一个非斜杠字符。支持多个以空格分隔的目标。

以下输入必须拒绝：

- 删除 `/tmp` 或 `/tmp/` 本身。
- 路径含 `..`、`*`、`?`、字符类、shell 变量、命令替换或反引号。
- 路径不位于 `/tmp/`。
- `rm` 命令还包含未识别选项、重定向、管道或其他 shell 操作。

SSH 例外只接受单一 SSH 调用。远端命令必须是由分号连接的受限诊断命令和精确 `/tmp` 清理命令；
不允许重定向、管道、命令替换、变量展开或对非 `/tmp` 路径执行写操作。SSH 目标主机不在 policy 中
硬编码，以便相同规则用于不同环境。

普通 `curl` 请求作为网络诊断放行，但包含上传、请求体、输出文件或显式写操作参数的调用不属于只读
例外。仓库内已有 worktree、分支和文件写入保护保持不变。

## 实现位置

用户级单一事实源是 `/home/yang/.local/lib/stratum-worktree-policy.sh`。Codex 和 Claude adapter 继续只
负责协议转换，不复制判断逻辑。

在 policy 的主工作区 Bash 分支中，先识别严格的临时清理和只读网络诊断，再进入通用 mutation
正则。匹配函数按完整命令判断，不能仅通过查找某个安全片段放行整个复合命令。

用户级测试 `/home/yang/.local/bin/test-stratum-worktree-guard` 增加允许与拒绝矩阵。仓库设计文档只记录
契约；实际用户级 hook 文件不提交到 Stratum 仓库。

## 验证

自动测试至少覆盖：

- 本地 `rm -f /tmp/stratum-loadtest`。
- 本地 `rm -rf /tmp/other-tool /tmp/nested/cache`。
- SSH 内的进程检查、精确临时清理和存在性检查。
- 只读公网 `curl` 健康检查。
- 拒绝 `/tmp`、通配符、路径穿越、变量、命令替换和非 `/tmp` 删除。
- 拒绝 SSH 内修改仓库、写文件或执行任意脚本。
- 继续拒绝主工作区 `git add`、脚本写入和分支切换。

修改后运行用户级 policy 测试、Codex hook adapter 测试和现有 sudo guard 测试。最后使用曾被误拦截的
本机临时清理、远程临时清理和公网健康检查命令做真实回归验证。
