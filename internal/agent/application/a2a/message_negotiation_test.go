package a2a

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

func testAgent(id string) AgentIdentity {
	return AgentIdentity{ID: id, Name: "Agent " + id}
}

func TestNewMessageDefaults(t *testing.T) {
	// 极端情况：默认 Payload/Headers 非 nil，Retries 为 0。
	from := testAgent("a1")
	msg := NewMessage(MessageTypeHeartbeat, from)
	if msg.ID == "" || msg.Timestamp.IsZero() {
		t.Fatal("message id/timestamp must be set")
	}
	if msg.Payload == nil || msg.Headers == nil {
		t.Fatal("payload/headers must be non-nil")
	}
	if msg.Retries != 0 || msg.Priority != "" {
		t.Fatalf("defaults = %+v", msg)
	}
	if msg.From.ID != "a1" {
		t.Fatalf("from = %+v", msg.From)
	}
}

func TestMessageBuildersAndClone(t *testing.T) {
	msg := NewMessage(MessageTypeTaskStart, testAgent("a1")).
		WithRecipient(testAgent("a2")).
		WithPayload("key", 42).
		WithPriority(PriorityHigh).
		WithReplyTo("reply-id").
		WithTrace("trace-1", "span-1")

	if msg.To.ID != "a2" || msg.Payload["key"] != 42 || msg.Priority != PriorityHigh {
		t.Fatalf("built = %+v", msg)
	}
	if msg.InReplyTo != "reply-id" || msg.TraceID != "trace-1" || msg.SpanID != "span-1" {
		t.Fatalf("trace/reply = %+v", msg)
	}

	clone := msg.Clone()
	if clone.ID != msg.ID || clone.Priority != msg.Priority {
		t.Fatalf("clone basics = %+v", clone)
	}
	// 极端情况：clone 必须深拷贝 map，修改互不影响。
	clone.Payload["key"] = "mutated"
	clone.Headers["h"] = "v"
	if msg.Payload["key"] != 42 {
		t.Fatal("payload map must be deep-cloned")
	}
	if _, exists := msg.Headers["h"]; exists {
		t.Fatal("headers map must be deep-cloned")
	}
}

func TestMessageWithPayloadNilMap(t *testing.T) {
	// 极端情况：nil Payload 上调用 WithPayload 不 panic。
	msg := &Message{}
	msg.WithPayload("k", "v")
	if msg.Payload["k"] != "v" {
		t.Fatalf("payload = %+v", msg.Payload)
	}
}

func TestNegotiationProposeAndRespondAccepted(t *testing.T) {
	svc := NewNegotiationService(zap.NewNop())
	offer := &TaskOffer{
		From: testAgent("a1"), To: testAgent("a2"),
		Description: "do work", Timeout: time.Hour,
	}
	id, err := svc.ProposeTask(context.Background(), offer)
	if err != nil || id == "" {
		t.Fatalf("propose = %q, %v", id, err)
	}

	neg, err := svc.GetNegotiation(id)
	if err != nil || neg.State != NegotiationStatePending {
		t.Fatalf("pending = %v, %v", neg, err)
	}
	if len(svc.GetPendingNegotiations()) != 1 {
		t.Fatal("must be exactly one pending negotiation")
	}

	err = svc.RespondToOffer(context.Background(), id, &TaskResponse{
		OfferID: id, From: testAgent("a2"), Accepted: true,
	})
	if err != nil {
		t.Fatalf("respond = %v", err)
	}
	neg, _ = svc.GetNegotiation(id)
	if neg.State != NegotiationStateAccepted || neg.Response == nil || !neg.Response.Accepted {
		t.Fatalf("accepted = %+v", neg)
	}
	if len(svc.GetPendingNegotiations()) != 0 {
		t.Fatal("accepted must leave no pending")
	}
}

func TestNegotiationRejectUpdatesStats(t *testing.T) {
	svc := NewNegotiationService(zap.NewNop())
	id, _ := svc.ProposeTask(context.Background(), &TaskOffer{
		From: testAgent("a1"), To: testAgent("a2"), Timeout: time.Hour,
	})
	if err := svc.RespondToOffer(context.Background(), id, &TaskResponse{
		OfferID: id, From: testAgent("a2"), Accepted: false, Reason: "busy",
	}); err != nil {
		t.Fatalf("respond = %v", err)
	}
	neg, _ := svc.GetNegotiation(id)
	if neg.State != NegotiationStateRejected {
		t.Fatalf("state = %v", neg.State)
	}
	stats := svc.GetStats()
	if stats.TotalProposed != 1 || stats.TotalRejected != 1 || stats.TotalAccepted != 0 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestNegotiationRespondAcceptedUpdatesAverageDuration(t *testing.T) {
	// 极端情况：EMA 平均时长累计逻辑。
	svc := NewNegotiationService(zap.NewNop())
	id1, _ := svc.ProposeTask(context.Background(), &TaskOffer{
		From: testAgent("a1"), To: testAgent("a2"), Timeout: time.Hour,
	})
	_ = svc.RespondToOffer(context.Background(), id1, &TaskResponse{
		OfferID: id1, From: testAgent("a2"), Accepted: true,
	})
	first := svc.GetStats().AverageDuration
	if first <= 0 {
		t.Fatalf("first average = %v", first)
	}
	id2, _ := svc.ProposeTask(context.Background(), &TaskOffer{
		From: testAgent("a1"), To: testAgent("a2"), Timeout: time.Hour,
	})
	_ = svc.RespondToOffer(context.Background(), id2, &TaskResponse{
		OfferID: id2, From: testAgent("a2"), Accepted: true,
	})
	second := svc.GetStats().AverageDuration
	if second <= 0 {
		t.Fatalf("second average = %v", second)
	}
}

func TestNegotiationErrors(t *testing.T) {
	svc := NewNegotiationService(zap.NewNop())

	// 未知 negotiation。
	if _, err := svc.GetNegotiation("ghost"); !errors.Is(err, ErrNegotiationNotFound) {
		t.Fatalf("get err = %v", err)
	}
	if err := svc.RespondToOffer(context.Background(), "ghost", &TaskResponse{}); !errors.Is(err, ErrNegotiationNotFound) {
		t.Fatalf("respond err = %v", err)
	}
	if err := svc.CancelNegotiation(context.Background(), "ghost"); !errors.Is(err, ErrNegotiationNotFound) {
		t.Fatalf("cancel err = %v", err)
	}

	// 极端情况：已终结的 negotiation 不能再响应。
	id, _ := svc.ProposeTask(context.Background(), &TaskOffer{
		From: testAgent("a1"), To: testAgent("a2"), Timeout: time.Hour,
	})
	_ = svc.RespondToOffer(context.Background(), id, &TaskResponse{
		OfferID: id, From: testAgent("a2"), Accepted: true,
	})
	if err := svc.RespondToOffer(context.Background(), id, &TaskResponse{}); !errors.Is(err, ErrInvalidNegotiationState) {
		t.Fatalf("second respond err = %v", err)
	}
}

func TestNegotiationCancelAndCleanup(t *testing.T) {
	svc := NewNegotiationService(zap.NewNop())

	id, _ := svc.ProposeTask(context.Background(), &TaskOffer{
		From: testAgent("a1"), To: testAgent("a2"), Timeout: time.Hour,
	})
	if err := svc.CancelNegotiation(context.Background(), id); err != nil {
		t.Fatalf("cancel = %v", err)
	}
	neg, _ := svc.GetNegotiation(id)
	if neg.State != NegotiationStateCancelled {
		t.Fatalf("state = %v", neg.State)
	}

	// 极端情况：Cleanup 只清 final 且超龄的 negotiation；活跃的不清。
	svc.Cleanup(-time.Hour)
	if _, err := svc.GetNegotiation(id); err == nil {
		t.Fatal("final negotiation must be cleaned")
	}
	id2, _ := svc.ProposeTask(context.Background(), &TaskOffer{
		From: testAgent("a1"), To: testAgent("a2"), Timeout: time.Hour,
	})
	svc.Cleanup(-time.Hour)
	if _, err := svc.GetNegotiation(id2); err != nil {
		t.Fatal("pending negotiation must survive cleanup")
	}
}

func TestNegotiationProposeExpiryTimer(t *testing.T) {
	// 极端情况：offer 超时后 pending 自动转 cancelled 并移除。
	svc := NewNegotiationService(zap.NewNop())
	id, _ := svc.ProposeTask(context.Background(), &TaskOffer{
		From: testAgent("a1"), To: testAgent("a2"), Timeout: 10 * time.Millisecond,
	})
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := svc.GetNegotiation(id); err != nil {
			break // 已移除
		}
		if time.Now().After(deadline) {
			t.Fatal("expired negotiation must be removed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestNegotiationConcurrentAccess(t *testing.T) {
	// 极端情况：并发 propose/respond 不产生数据竞争。
	svc := NewNegotiationService(zap.NewNop())
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := svc.ProposeTask(context.Background(), &TaskOffer{
				From: testAgent("a1"), To: testAgent("a2"), Timeout: time.Hour,
			})
			if err == nil {
				_ = svc.RespondToOffer(context.Background(), id, &TaskResponse{
					OfferID: id, From: testAgent("a2"), Accepted: true,
				})
				_, _ = svc.GetNegotiation(id)
				_ = svc.GetPendingNegotiations()
				_ = svc.GetStats()
			}
		}()
	}
	wg.Wait()
}

func TestA2AErrorHierarchy(t *testing.T) {
	// 极端情况：A2AError 携带 Type 且实现 error。
	err := &A2AError{Type: ErrorTypeNegotiation, Message: "boom"}
	if got := err.Error(); got != "[negotiation] boom" {
		t.Fatalf("Error() = %q", got)
	}
}
