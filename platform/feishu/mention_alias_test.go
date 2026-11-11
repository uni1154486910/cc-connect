package feishu

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func aliasMention(openID, name string) *larkim.MentionEvent {
	return &larkim.MentionEvent{Key: strPtr("@_user_1"), Id: &larkim.UserId{OpenId: strPtr(openID)}, Name: strPtr(name)}
}

func TestMentionAliasHit(t *testing.T) {
	owner := []*larkim.MentionEvent{aliasMention("ou_owner", "Owner")}
	other := []*larkim.MentionEvent{aliasMention("ou_other", "Other")}
	cases := []struct {
		name  string
		users string
		chats string
		chat  string
		ments []*larkim.MentionEvent
		want  bool
	}{
		{"alias in listed chat", "ou_owner", "oc_bug", "oc_bug", owner, true},
		{"alias in unlisted chat", "ou_owner", "oc_bug", "oc_other", owner, false},
		{"alias in any chat with *", "ou_owner", "*", "oc_other", owner, true},
		{"non-alias user", "ou_owner", "oc_bug", "oc_bug", other, false},
		{"no chats configured disables", "ou_owner", "", "oc_bug", owner, false},
		{"no users configured disables", "", "oc_bug", "oc_bug", owner, false},
		{"one of several aliases", "ou_a, ou_owner", "oc_x,oc_bug", "oc_bug", owner, true},
		{"no mentions", "ou_owner", "oc_bug", "oc_bug", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &Platform{mentionAliasUsers: parseIDSet(tc.users), mentionAliasChats: tc.chats}
			if got := p.mentionAliasHit(tc.chat, tc.ments); got != tc.want {
				t.Fatalf("mentionAliasHit = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStringOptAcceptsStringOrArray(t *testing.T) {
	if got := stringOpt(map[string]any{"k": "a,b"}, "k"); got != "a,b" {
		t.Fatalf("string form = %q", got)
	}
	if got := stringOpt(map[string]any{"k": []any{"a", "b"}}, "k"); got != "a,b" {
		t.Fatalf("array form = %q", got)
	}
	if got := stringOpt(map[string]any{}, "k"); got != "" {
		t.Fatalf("missing = %q", got)
	}
}

// End-to-end through onMessage: an @owner message in the alias chat reaches the
// handler; the same message in another chat is dropped by the mention gate.
func TestOnMessageMentionAliasRoutesToHandler(t *testing.T) {
	const appID, appSecret = "cli_alias", "secret-alias"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			writeJSON(t, w, map[string]any{"code": 0, "msg": "success", "expire": 7200, "tenant_access_token": "tenant-token"})
			return
		}
		writeJSON(t, w, map[string]any{"code": 0, "msg": "success"})
	}))
	defer srv.Close()

	got := make(chan *core.Message, 2)
	p := &Platform{
		platformName:      "feishu",
		domain:            srv.URL,
		appID:             appID,
		appSecret:         appSecret,
		botOpenID:         "ou_bot",
		dedup:             &core.MessageDedup{},
		mentionAliasUsers: parseIDSet("ou_owner"),
		mentionAliasChats: "oc_bug",
		client:            lark.NewClient(appID, appSecret, lark.WithOpenBaseUrl(srv.URL), lark.WithHttpClient(srv.Client())),
		handler:           func(_ core.Platform, msg *core.Message) { got <- msg },
	}

	send := func(msgID, chatID string) {
		chatType, senderType, msgType := "group", "user", "text"
		content := `{"text":"@_user_1 帮忙看下这个缺陷"}`
		createTime := strconv.FormatInt(time.Now().Add(time.Second).UnixMilli(), 10)
		err := p.onMessage(context.Background(), &larkim.P2MessageReceiveV1{
			Event: &larkim.P2MessageReceiveV1Data{
				Sender: &larkim.EventSender{SenderId: &larkim.UserId{OpenId: strPtr("ou_reporter")}, SenderType: &senderType},
				Message: &larkim.EventMessage{
					MessageId: strPtr(msgID), ChatId: strPtr(chatID), ChatType: &chatType, MessageType: &msgType,
					Content: &content, CreateTime: &createTime,
					Mentions: []*larkim.MentionEvent{aliasMention("ou_owner", "Owner")},
				},
			},
		})
		if err != nil {
			t.Fatalf("onMessage: %v", err)
		}
	}

	send("om_other_chat", "oc_other")
	send("om_bug_chat", "oc_bug")

	select {
	case msg := <-got:
		if msg.MessageID != "om_bug_chat" {
			t.Fatalf("handler got %q, want only the alias-chat message", msg.MessageID)
		}
		if !strings.Contains(msg.Content, "@Owner") {
			t.Fatalf("Content = %q, want the alias mention kept as @Owner", msg.Content)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out: alias mention in configured chat did not reach the handler")
	}
	select {
	case msg := <-got:
		t.Fatalf("unexpected second dispatch %q (other chat should be dropped)", msg.MessageID)
	case <-time.After(300 * time.Millisecond):
	}
}
