package a2a

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

// newTestClient 构造 protocol + client，返回两者。
func newTestClient(t *testing.T, agentID string) (*A2AProtocol, *A2AClient) {
	t.Helper()
	p := NewA2AProtocol(DefaultProtocolConfig(), zap.NewNop())
	c := p.CreateClient(agentID, "Agent "+agentID)
	if c == nil {
		t.Fatal("client must not be nil")
	}
	return p, c
}

// clientHandler 取 client 上某消息类型的第一个默认 handler。
func clientHandler(c *A2AClient, mtype MessageType) func(context.Context, *Message) (*Message, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	hs := c.listeners[mtype]
	if len(hs) == 0 {
		return nil
	}
	return hs[0]
}

func TestNewA2AClientDefaults(t *testing.T) {
	_, c := newTestClient(t, "a1")
	if c.GetAgentID() != "a1" {
		t.Fatalf("agent id = %q", c.GetAgentID())
	}
	if caps := c.GetCapabilities(); caps == nil || len(caps) != 0 {
		t.Fatalf("capabilities = %+v", caps)
	}
	if len(c.GetActiveCollaborations()) != 0 {
		t.Fatal("no active collaborations expected")
	}
}

func TestClientAnnounceCapabilities(t *testing.T) {
	_, c := newTestClient(t, "a1")
	caps := []Capability{{Name: "code"}, {Name: "docs"}}
	if err := c.AnnounceCapabilities(context.Background(), caps); err != nil {
		t.Fatalf("announce = %v", err)
	}
	got := c.GetCapabilities()
	if len(got) != 2 || got[0].Name != "code" {
		t.Fatalf("capabilities = %+v", got)
	}
	// 极端情况：空能力列表覆盖旧值。
	if err := c.AnnounceCapabilities(context.Background(), nil); err != nil {
		t.Fatalf("announce empty = %v", err)
	}
	if len(c.GetCapabilities()) != 0 {
		t.Fatal("capabilities must be replaced, not appended")
	}
}

func TestClientDiscoverAgents(t *testing.T) {
	p, c := newTestClient(t, "a1")
	_ = p.discovery.RegisterPeer(context.Background(), testAgent("a2"), []Capability{{Name: "code"}})

	identities, err := c.DiscoverAgents(context.Background(), []string{"code"})
	if err != nil || len(identities) != 1 || identities[0].ID != "a2" {
		t.Fatalf("discover = %+v, %v", identities, err)
	}
	// 极端情况：无匹配能力 → 空结果非 nil。
	identities, err = c.DiscoverAgents(context.Background(), []string{"nope"})
	if err != nil || len(identities) != 0 || identities == nil {
		t.Fatalf("empty discover = %+v, %v", identities, err)
	}
}

func TestClientSendAndBroadcastMethods(t *testing.T) {
	_, c := newTestClient(t, "a1")
	ctx := context.Background()

	if err := c.AcceptTask(ctx, "p1"); err != nil {
		t.Fatalf("accept = %v", err)
	}
	if err := c.RejectTask(ctx, "p1", "busy"); err != nil {
		t.Fatalf("reject = %v", err)
	}
	collabID, err := c.RequestCollaboration(ctx, []AgentIdentity{testAgent("a2")}, "work", StrategyParallel)
	if err != nil || collabID == "" {
		t.Fatalf("request collaboration = %q, %v", collabID, err)
	}
	// 极端情况：空 participants 不 panic。
	if _, err := c.RequestCollaboration(ctx, nil, "work", StrategySequential); err != nil {
		t.Fatalf("empty collab = %v", err)
	}
	if err := c.JoinCollaboration(ctx, "c-1"); err != nil {
		t.Fatalf("join = %v", err)
	}
	if err := c.LeaveCollaboration(ctx, "c-1"); err != nil {
		t.Fatalf("leave = %v", err)
	}
	if err := c.ReportProgress(ctx, "t1", 50, "half"); err != nil {
		t.Fatalf("progress = %v", err)
	}
	dataID, err := c.SendData(ctx, testAgent("a2"), "docs", map[string]interface{}{"k": 1})
	if err != nil || dataID == "" {
		t.Fatalf("send data = %q, %v", dataID, err)
	}
	// 极端情况：nil data。
	if _, err := c.SendData(ctx, testAgent("a2"), "docs", nil); err != nil {
		t.Fatalf("send nil data = %v", err)
	}
	if err := c.Broadcast(ctx, MessageTypeHeartbeat, map[string]interface{}{"k": "v"}); err != nil {
		t.Fatalf("broadcast = %v", err)
	}
}

func TestClientProposeTaskCancelledContext(t *testing.T) {
	// 极端情况：ctx 已取消 → 立即返回 ctx.Err()，replyChan 被清理。
	_, c := newTestClient(t, "a1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.ProposeTask(ctx, testAgent("a2"), "work", nil)
	if err == nil {
		t.Fatal("cancelled ctx must error")
	}
	c.mu.RLock()
	n := len(c.replyChans)
	c.mu.RUnlock()
	if n != 0 {
		t.Fatalf("replyChans must be cleaned, got %d", n)
	}
}

func TestClientRequestDataCancelledContext(t *testing.T) {
	_, c := newTestClient(t, "a1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := c.RequestData(ctx, testAgent("a2"), "docs", nil); err == nil {
		t.Fatal("cancelled ctx must error")
	}
	c.mu.RLock()
	n := len(c.replyChans)
	c.mu.RUnlock()
	if n != 0 {
		t.Fatalf("replyChans must be cleaned, got %d", n)
	}
}

func TestClientCollaborationSessionLifecycle(t *testing.T) {
	// 通过默认 handler 驱动 collaboration session 状态机。
	_, c := newTestClient(t, "a1")
	ctx := context.Background()

	proposal := NewMessage(MessageTypeCollaborationProposal, testAgent("a2")).
		WithPayload("collaboration_id", "c-1").
		WithPayload("participants", []AgentIdentity{testAgent("a3")})
	if _, err := clientHandler(c, MessageTypeCollaborationProposal)(ctx, proposal); err != nil {
		t.Fatalf("proposal handler = %v", err)
	}
	session := c.GetCollaboration("c-1")
	if session == nil || session.State != "invited" || session.Role != "participant" {
		t.Fatalf("session = %+v", session)
	}
	if len(c.GetActiveCollaborations()) != 1 {
		t.Fatal("must have one active collaboration")
	}

	// Join 追加参与者。
	join := NewMessage(MessageTypeJoinCollaboration, testAgent("a4")).
		WithPayload("collaboration_id", "c-1").
		WithPayload("agent_id", "a4")
	if _, err := clientHandler(c, MessageTypeJoinCollaboration)(ctx, join); err != nil {
		t.Fatalf("join handler = %v", err)
	}
	session = c.GetCollaboration("c-1")
	if len(session.Participants) != 2 || session.Participants[1].ID != "a4" {
		t.Fatalf("participants = %+v", session.Participants)
	}

	// Started → active。
	started := NewMessage(MessageTypeCollaborationStarted, testAgent("a2")).
		WithPayload("collaboration_id", "c-1")
	if _, err := clientHandler(c, MessageTypeCollaborationStarted)(ctx, started); err != nil {
		t.Fatalf("started handler = %v", err)
	}
	if session.State != sessionStateActive {
		t.Fatalf("state = %q", session.State)
	}

	// Completed → completed + shared data。
	completed := NewMessage(MessageTypeCollaborationCompleted, testAgent("a2")).
		WithPayload("collaboration_id", "c-1").
		WithPayload("results", map[string]interface{}{"ok": true})
	if _, err := clientHandler(c, MessageTypeCollaborationCompleted)(ctx, completed); err != nil {
		t.Fatalf("completed handler = %v", err)
	}
	if session.State != taskStatusCompleted || session.SharedData["ok"] != true {
		t.Fatalf("completed = %+v", session)
	}

	// Leave 移除参与者。
	leave := NewMessage(MessageTypeLeaveCollaboration, testAgent("a4")).
		WithPayload("collaboration_id", "c-1").
		WithPayload("agent_id", "a4")
	if _, err := clientHandler(c, MessageTypeLeaveCollaboration)(ctx, leave); err != nil {
		t.Fatalf("leave handler = %v", err)
	}
	session = c.GetCollaboration("c-1")
	if len(session.Participants) != 1 || session.Participants[0].ID != "a3" {
		t.Fatalf("participants after leave = %+v", session.Participants)
	}
}

func TestClientSharedDataAndMissingSession(t *testing.T) {
	_, c := newTestClient(t, "a1")

	// 极端情况：session 不存在时 AddSharedData/GetSharedData no-op。
	c.AddSharedData("ghost", "k", "v")
	if _, ok := c.GetSharedData("ghost", "k"); ok {
		t.Fatal("ghost session must not return data")
	}

	// 正常路径。
	proposal := NewMessage(MessageTypeCollaborationProposal, testAgent("a2")).
		WithPayload("collaboration_id", "c-1").
		WithPayload("participants", []AgentIdentity{})
	if _, err := clientHandler(c, MessageTypeCollaborationProposal)(context.Background(), proposal); err != nil {
		t.Fatalf("proposal handler = %v", err)
	}
	c.AddSharedData("c-1", "key", 42)
	value, ok := c.GetSharedData("c-1", "key")
	if !ok || value != 42 {
		t.Fatalf("shared data = %v, %v", value, ok)
	}
}

func TestClientDataResponseRoutesReply(t *testing.T) {
	// 极端情况：DataResponse 路由到对应 replyChan。
	_, c := newTestClient(t, "a1")
	replyChan := make(chan *Message, 1)
	c.mu.Lock()
	c.replyChans["expected-id"] = replyChan
	c.mu.Unlock()

	reply := NewMessage(MessageTypeDataResponse, testAgent("a2")).
		WithPayload("data", "payload").
		WithReplyTo("expected-id")
	if _, err := clientHandler(c, MessageTypeDataResponse)(context.Background(), reply); err != nil {
		t.Fatalf("data response handler = %v", err)
	}
	select {
	case got := <-replyChan:
		if got.Payload["data"] != "payload" {
			t.Fatalf("routed reply = %+v", got)
		}
	default:
		t.Fatal("reply must be routed to waiting channel")
	}

	// 极端情况：无等待者的 response 不阻塞。
	stray := NewMessage(MessageTypeDataResponse, testAgent("a2")).
		WithReplyTo("no-waiter")
	if _, err := clientHandler(c, MessageTypeDataResponse)(context.Background(), stray); err != nil {
		t.Fatalf("stray response = %v", err)
	}
}

func TestClientDefaultHandlersNoPanic(t *testing.T) {
	// 极端情况：默认 handler 对各种消息类型不 panic。
	_, c := newTestClient(t, "a1")
	ctx := context.Background()

	task := NewMessage(MessageTypeTaskProposal, testAgent("a2")).
		WithPayload("description", "do it")
	if _, err := clientHandler(c, MessageTypeTaskProposal)(ctx, task); err != nil {
		t.Fatalf("task handler = %v", err)
	}

	dataReq := NewMessage(MessageTypeDataRequest, testAgent("a2")).
		WithPayload("data_type", "docs")
	got, err := clientHandler(c, MessageTypeDataRequest)(ctx, dataReq)
	if err != nil || got == nil || got.Type != MessageTypeDataResponse || got.Payload["error"] != "data not available" {
		t.Fatalf("data request default = %+v, %v", got, err)
	}

	progress := NewMessage(MessageTypeProgressUpdate, testAgent("a2")).
		WithPayload("task_id", "t1").
		WithPayload("progress", 10)
	if _, err := clientHandler(c, MessageTypeProgressUpdate)(ctx, progress); err != nil {
		t.Fatalf("progress handler = %v", err)
	}

	announcement := NewMessage(MessageTypeCapabilityAnnouncement, testAgent("a2"))
	if _, err := clientHandler(c, MessageTypeCapabilityAnnouncement)(ctx, announcement); err != nil {
		t.Fatalf("announcement handler = %v", err)
	}

	// 极端情况：未知类型无 handler → handler 不存在（不 panic）。
	c.mu.RLock()
	_, known := c.listeners[MessageTypeAgentDeparture]
	c.mu.RUnlock()
	if known {
		t.Fatal("unknown type must have no handler")
	}
}

func TestClientStopClosesResources(t *testing.T) {
	_, c := newTestClient(t, "a1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	replyChan := make(chan *Message, 1)
	c.mu.Lock()
	c.replyChans["pending"] = replyChan
	c.collaborations["c-1"] = &CollaborationSession{ID: "s1", CollaborationID: "c-1"}
	c.mu.Unlock()

	if err := c.Stop(ctx); err != nil {
		t.Fatalf("stop = %v", err)
	}
	// 极端情况：Stop 后 replyChans/collaborations 清空，且 channel 被 close。
	select {
	case _, ok := <-replyChan:
		if ok {
			t.Fatal("reply channel must be closed")
		}
	default:
		t.Fatal("reply channel must be closed")
	}
	c.mu.RLock()
	nReply := len(c.replyChans)
	nCollab := len(c.collaborations)
	c.mu.RUnlock()
	if nReply != 0 || nCollab != 0 {
		t.Fatalf("after stop: replies=%d collabs=%d", nReply, nCollab)
	}
}
