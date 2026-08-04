package a2a

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"
)

var errFake = errors.New("handler failed")

// readOutbox 非阻塞读 outbox 首条消息（无 Start 时无人消费，数据滞留）。
func readOutbox(h *ProtocolHandler) *Message {
	select {
	case msg := <-h.outbox:
		return msg
	default:
		return nil
	}
}

func TestProtocolHandlerStartStopIdempotent(t *testing.T) {
	h := NewProtocolHandler(zap.NewNop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := h.Start(ctx); err != nil {
		t.Fatalf("start = %v", err)
	}
	if err := h.Start(ctx); err != nil {
		t.Fatalf("second start = %v", err)
	}
	h.Stop()
}

func TestProtocolHandlerRoutingAndReply(t *testing.T) {
	// 同步 handleMessage：reply 写入 outbox 等待检查。
	h := NewProtocolHandler(zap.NewNop())
	h.RegisterHandler(MessageTypeDataRequest, func(ctx context.Context, msg *Message) (*Message, error) {
		return NewMessage(MessageTypeDataResponse, msg.To).
			WithRecipient(msg.From).
			WithPayload("echo", msg.Payload["echo"]), nil
	})

	msg := NewMessage(MessageTypeDataRequest, testAgent("a1")).
		WithRecipient(testAgent("a2")).
		WithPayload("echo", "hello")
	h.handleMessage(context.Background(), msg)

	reply := readOutbox(h)
	if reply == nil || reply.Type != MessageTypeDataResponse || reply.Payload["echo"] != "hello" {
		t.Fatalf("reply = %+v", reply)
	}
}

func TestProtocolHandlerHandlerErrorContinues(t *testing.T) {
	// 极端情况：handler 报错只记日志，继续执行后续 handler。
	h := NewProtocolHandler(zap.NewNop())
	var calls int
	h.RegisterHandler(MessageTypeHeartbeat, func(context.Context, *Message) (*Message, error) {
		return nil, errFake
	})
	h.RegisterHandler(MessageTypeHeartbeat, func(context.Context, *Message) (*Message, error) {
		calls++
		return nil, nil
	})

	h.handleMessage(context.Background(), NewMessage(MessageTypeHeartbeat, testAgent("a1")))
	if calls != 1 {
		t.Fatalf("second handler calls = %d", calls)
	}
}

func TestProtocolHandlerUnknownTypeWarns(t *testing.T) {
	// 极端情况：无 handler 的消息类型不 panic、不产出 reply。
	h := NewProtocolHandler(zap.NewNop())
	h.handleMessage(context.Background(), NewMessage(MessageTypeAgentDeparture, testAgent("a1")))
	if reply := readOutbox(h); reply != nil {
		t.Fatalf("unknown type must not reply, got %+v", reply)
	}
}

func TestProtocolHandlerSendMessageSetsReplyTo(t *testing.T) {
	h := NewProtocolHandler(zap.NewNop())
	ctx := context.Background()

	msg := NewMessage(MessageTypeHeartbeat, testAgent("a1"))
	if err := h.SendMessage(ctx, msg, "original-id"); err != nil {
		t.Fatalf("send = %v", err)
	}
	if msg.InReplyTo != "original-id" {
		t.Fatalf("replyTo = %q", msg.InReplyTo)
	}
	// 极端情况：replyTo 为空时保持原值。
	msg2 := NewMessage(MessageTypeHeartbeat, testAgent("a1"))
	if err := h.SendMessage(ctx, msg2, ""); err != nil {
		t.Fatalf("send = %v", err)
	}
	if msg2.InReplyTo != "" {
		t.Fatalf("empty replyTo must not overwrite, got %q", msg2.InReplyTo)
	}
}

func TestProtocolHandlerDefaultDiscoveryHandler(t *testing.T) {
	h := NewProtocolHandler(zap.NewNop())
	h.handleMessage(context.Background(),
		NewMessage(MessageTypeDiscoveryRequest, testAgent("a1")).WithRecipient(testAgent("a2")))

	reply := readOutbox(h)
	if reply == nil || reply.Type != MessageTypeDiscoveryResponse || reply.To.ID != "a1" || reply.InReplyTo == "" {
		t.Fatalf("discovery reply = %+v", reply)
	}
}

func TestProtocolHandlerDefaultCollaborationHandler(t *testing.T) {
	h := NewProtocolHandler(zap.NewNop())
	msg := NewMessage(MessageTypeCollaborationProposal, testAgent("a1")).
		WithRecipient(testAgent("a2")).
		WithPayload("collaboration_id", "c-1")
	h.handleMessage(context.Background(), msg)

	reply := readOutbox(h)
	if reply == nil || reply.Type != MessageTypeCollaborationAccept || reply.Payload["collaboration_id"] != "c-1" {
		t.Fatalf("collab reply = %+v", reply)
	}
}

func TestProtocolHandlerDefaultTaskProposalRejects(t *testing.T) {
	h := NewProtocolHandler(zap.NewNop())
	msg := NewMessage(MessageTypeTaskProposal, testAgent("a1")).
		WithRecipient(testAgent("a2")).
		WithPayload("description", "do it")
	h.handleMessage(context.Background(), msg)

	reply := readOutbox(h)
	if reply == nil || reply.Type != MessageTypeTaskResponse || reply.Payload["response"] != "rejected" {
		t.Fatalf("task reply = %+v", reply)
	}
}

func TestProtocolHandlerDefaultProgressHandlerNoReply(t *testing.T) {
	// 极端情况：progress 消息直接吞掉，无 reply。
	h := NewProtocolHandler(zap.NewNop())
	h.handleMessage(context.Background(),
		NewMessage(MessageTypeProgressUpdate, testAgent("a1")).
			WithPayload("task_id", "t1").WithPayload("progress", 50))
	if reply := readOutbox(h); reply != nil {
		t.Fatalf("progress must not reply, got %+v", reply)
	}
}

func TestProtocolHandlerHeartbeatNoReply(t *testing.T) {
	h := NewProtocolHandler(zap.NewNop())
	h.handleMessage(context.Background(), NewMessage(MessageTypeHeartbeat, testAgent("a1")))
	if reply := readOutbox(h); reply != nil {
		t.Fatalf("heartbeat must not reply, got %+v", reply)
	}
}

func TestProtocolHandlerReceiveBuffersWithoutStart(t *testing.T) {
	// 极端情况：未 Start 时 Receive 不阻塞（buffered channel）。
	h := NewProtocolHandler(zap.NewNop())
	h.Receive(NewMessage(MessageTypeHeartbeat, testAgent("a1")))
	select {
	case msg := <-h.inbox:
		if msg.Type != MessageTypeHeartbeat {
			t.Fatalf("msg = %+v", msg)
		}
	default:
		t.Fatal("inbox must buffer message")
	}
}
