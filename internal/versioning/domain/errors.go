package domain

import "errors"

var (
	// ErrVersionNotFound 目标版本不存在，或存在但不是该资源的可回滚历史版本
	// （非 deprecated）。与 skill 的 ErrSkillNotFound 同语义：回滚目标非法时
	// 对外呈现 404，不泄露版本状态细节。
	ErrVersionNotFound = errors.New("version not found")
	// ErrVersionKindUnsupported 资源类型尚未接入通用版本机制（无产品表映射）。
	// 本期仅 agent 接入；fail-closed，禁止静默跳过版本写入。
	ErrVersionKindUnsupported = errors.New("version kind not supported")
)
