package application

// contractRetryDefaultAttempts 是输出契约违例（下游对上游输出字段的 JSON 引用
// 解析失败，见 nodeInput/referencedOutputKeys）的内置重试预算：初始 1 次 + 最多
// 2 次重试。commitRetryOrFail 的 canRetry 语义是 AttemptNo < maxAttempts 才重试，
// 故取 3。作者在节点上显式配置的 retry.max_attempts 若更高则被尊重。
const contractRetryDefaultAttempts = 3
