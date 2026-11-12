package core

import (
	"context"
	"sync"
	"testing"
)

type claudeStubAgent struct{ stubAgent }

func (a *claudeStubAgent) Name() string { return "claudecode" }

type steerRecordingSession struct {
	stubAgentSession
	mu    sync.Mutex
	sent  []string
	fails bool
}

func (s *steerRecordingSession) Send(prompt string, _ string, _ []ImageAttachment, _ []FileAttachment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fails {
		return context.Canceled
	}
	s.sent = append(s.sent, prompt)
	return nil
}

func newSteerFixture(t *testing.T, steer bool, agent Agent) (*Engine, *stubPlatformEngine, *steerRecordingSession, *Session) {
	t.Helper()
	p := &stubPlatformEngine{n: "feishu"}
	e := NewEngine("test", agent, []Platform{p}, "", LangChinese)
	e.SetSteerPrivate(steer)
	as := &steerRecordingSession{}
	sess := &Session{}
	if _, ok := sess.TryLock(); !ok {
		t.Fatal("could not mark session busy")
	}
	e.interactiveStates["k"] = &interactiveState{platform: p, replyCtx: "ctx", agentSession: as, busySession: sess}
	return e, p, as, sess
}

func TestSteerBusySession_PrivateMessageGoesIntoRunningTurn(t *testing.T) {
	e, p, as, sess := newSteerFixture(t, true, &claudeStubAgent{})
	msg := &Message{SessionKey: "feishu:oc_p2p:ou_me", Content: "顺便再跑一下 echo B", IsPrivate: true, ReplyCtx: "ctx"}

	if !e.steerBusySession(p, msg, "k", sess) {
		t.Fatal("private message on a busy Claude Code session should be steered into the turn")
	}
	if len(as.sent) != 1 || as.sent[0] != "顺便再跑一下 echo B" {
		t.Fatalf("agent stdin got %v, want the message text once", as.sent)
	}
	if len(p.sent) != 1 || p.sent[0] != e.i18n.T(MsgSteerDelivered) {
		t.Fatalf("user should get the steer acknowledgement, got %v", p.sent)
	}
}

func TestSteerBusySession_FallsBackToQueue(t *testing.T) {
	cases := []struct {
		name    string
		steer   bool
		agent   Agent
		private bool
		idle    bool
		fail    bool
	}{
		{name: "group chat always queues", steer: true, agent: &claudeStubAgent{}, private: false},
		{name: "feature switched off", steer: false, agent: &claudeStubAgent{}, private: true},
		{name: "non Claude Code agent", steer: true, agent: &stubAgent{}, private: true},
		{name: "session not busy", steer: true, agent: &claudeStubAgent{}, private: true, idle: true},
		{name: "send failure", steer: true, agent: &claudeStubAgent{}, private: true, fail: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, p, as, sess := newSteerFixture(t, tc.steer, tc.agent)
			if tc.idle {
				sess.Unlock(0)
			}
			as.fails = tc.fail
			msg := &Message{SessionKey: "feishu:oc_x:ou_me", Content: "补充", IsPrivate: tc.private, ReplyCtx: "ctx"}
			if e.steerBusySession(p, msg, "k", sess) {
				t.Fatal("expected fallback to the normal queue path")
			}
			if len(as.sent) != 0 || len(p.sent) != 0 {
				t.Fatalf("nothing should be sent on fallback: stdin=%v replies=%v", as.sent, p.sent)
			}
		})
	}
}
