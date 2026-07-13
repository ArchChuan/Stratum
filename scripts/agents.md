# scripts 目录规则

## Windows 定时任务注册

本目录里的无人值守任务脚本运行在 WSL2 内，但定时触发由 WSL2 外的 Windows Task Scheduler 负责。

规则：

- 不为每个任务新增 `register-*.ps1` 注册脚本。
- Windows 侧定时任务直接调用 `wsl.exe`，再在 WSL2 内执行对应 Bash 脚本。
- Bash 脚本自己负责日志目录、锁文件、dry-run、超时和退出码。
- 新增每日任务时，参照 `update-docs-daily.sh` 的调用模型，只提交 WSL 内执行脚本。

推荐注册方式：

```powershell
schtasks /Create `
  /TN "Stratum Daily Agent Interview Research" `
  /SC DAILY `
  /ST 08:30 `
  /TR "wsl.exe -d Ubuntu --cd /home/yang/go-projects/stratum -- bash -lc 'chmod +x scripts/daily-agent-interview.sh && scripts/daily-agent-interview.sh'" `
  /F
```

验证命令：

```powershell
schtasks /Run /TN "Stratum Daily Agent Interview Research"
```

错过计划后的补执行设置：

```powershell
$names = @(
  "Stratum Daily Agent Interview Research",
  "Stratum Daily Codex Risk Scan",
  "StratumDocsUpdate"
)

foreach ($name in $names) {
  $task = Get-ScheduledTask -TaskName $name
  $settings = $task.Settings
  $settings.StartWhenAvailable = $true
  Set-ScheduledTask -TaskName $task.TaskName -TaskPath $task.TaskPath -Settings $settings
}
```

确认设置：

```powershell
$names = @(
  "Stratum Daily Agent Interview Research",
  "Stratum Daily Codex Risk Scan",
  "StratumDocsUpdate"
)

foreach ($name in $names) {
  $task = Get-ScheduledTask -TaskName $name
  [PSCustomObject]@{
    TaskName = $task.TaskName
    TaskPath = $task.TaskPath
    StartWhenAvailable = $task.Settings.StartWhenAvailable
  }
}
```

输出位置：

- 日志：`tmp/agent-interview/logs/YYYY-MM-DD.log`
- 报告：`tmp/agent-interview/reports/latest.md`

如需改发行版、时间或仓库路径，只修改 Windows 侧计划任务配置，不在仓库内新增注册脚本。
