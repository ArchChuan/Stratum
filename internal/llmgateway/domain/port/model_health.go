package port

// ModelHealthProvider 提供模型运行时健康状态，供目录投影附加到响应。
// 状态字符串取值与 llmgateway infrastructure 的 ModelHealth 一致：
// healthy / degraded / unhealthy / half_open；未记录状态返回空字符串
// （前端展示为"未探活"）。健康状态仅用于展示与降级提示，不参与解析决策
// （解析层的健康感知在 infrastructure 内直接访问 HealthRegistry）。
type ModelHealthProvider interface {
	ModelHealth(modelName string) string
}
