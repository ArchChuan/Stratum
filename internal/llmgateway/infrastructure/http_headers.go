package infrastructure

import "net/http"

// applyExtraHeaders 把 provider extra_headers 应用到请求头（key 已由写时
// 校验归一化，见 domain.ValidateExtraHeaders）。调用方必须在之后设置自身
// 硬编码鉴权头（Authorization / x-api-key / Content-Type 等），使鉴权头
// 永远覆盖用户配置，禁止用户配置改写鉴权。
func applyExtraHeaders(h http.Header, extra map[string]string) {
	for k, v := range extra {
		h.Set(k, v)
	}
}
