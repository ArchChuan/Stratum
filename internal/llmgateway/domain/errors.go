package domain

// policyBlockedError 是网关策略拦截的语义化错误：permanent（不重试不降级），
// 与 infrastructure 的 permanentError 同构但位于 domain 包，供 api 中间件
// 以 errors.Is 匹配 sentinel 映射 HTTP 状态码。
type policyBlockedError struct{ msg string }

func (e *policyBlockedError) Error() string   { return e.msg }
func (e *policyBlockedError) Permanent() bool { return true }

// ErrSamplingOutOfRange 表示采样参数越界（L3：注入或显式值超模型上限）。
// 拦截错误 = permanent：重试依旧报错，立即中止 fallback 链。
var ErrSamplingOutOfRange = &policyBlockedError{msg: "sampling parameter out of range"}

// ErrCapabilityUnsupported 表示请求能力与模型声明不匹配（L4：known-non）。
// 拦截错误 = permanent：重试依旧报错，立即中止 fallback 链。
var ErrCapabilityUnsupported = &policyBlockedError{msg: "requested capability unsupported by model"}
