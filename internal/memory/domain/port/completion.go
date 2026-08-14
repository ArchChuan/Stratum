package port

import (
	llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
)

// 传输镜像退役桥接（过渡态，plan Task 8 删除本文件）。
//
// 此前 memport 是 llmgateway domain 的「有损镜像」：Temperature float64、无 json
// tag、CompletionResponse 只留 Content+CompletionTokens，wiring 需手工逐字段转换。
// 改为无损别名后类型逐字段一致，转换层删除；消费者按包逐个迁移到 llmdomain，
// 迁移完成（Task 8）后本文件整体删除。llmdomain 是 spec §3 指定的唯一可跨
// context import 的 domain。
type CompletionMessage = llmdomain.Message
type ResponseFormat = llmdomain.ResponseFormat
type CompletionRequest = llmdomain.CompletionRequest
type CompletionResponse = llmdomain.CompletionResponse
type Completer = llmdomain.Completer
