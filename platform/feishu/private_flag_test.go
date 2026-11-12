package feishu

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// p2p messages must reach the engine with IsPrivate=true (private-chat steering
// keys off it); group messages must not.
func TestOnMessageStampsIsPrivateForP2P(t *testing.T) {
	const appID, appSecret = "cli_private", "secret-private"
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
		platformName: "feishu", domain: srv.URL, appID: appID, appSecret: appSecret, botOpenID: "ou_bot",
		dedup:   &core.MessageDedup{},
		client:  lark.NewClient(appID, appSecret, lark.WithOpenBaseUrl(srv.URL), lark.WithHttpClient(srv.Client())),
		handler: func(_ core.Platform, msg *core.Message) { got <- msg },
	}
	send := func(msgID, chatType string) {
		senderType, msgType := "user", "text"
		content := `{"text":"@_user_1 hello"}`
		createTime := strconv.FormatInt(time.Now().Add(time.Second).UnixMilli(), 10)
		if err := p.onMessage(context.Background(), &larkim.P2MessageReceiveV1{Event: &larkim.P2MessageReceiveV1Data{
			Sender: &larkim.EventSender{SenderId: &larkim.UserId{OpenId: strPtr("ou_me")}, SenderType: &senderType},
			Message: &larkim.EventMessage{MessageId: strPtr(msgID), ChatId: strPtr("oc_" + chatType), ChatType: &chatType,
				MessageType: &msgType, Content: &content, CreateTime: &createTime,
				Mentions: []*larkim.MentionEvent{aliasMention("ou_bot", "Bot")}},
		}}); err != nil {
			t.Fatalf("onMessage: %v", err)
		}
	}
	want := map[string]bool{"om_p2p": true, "om_group": false}
	send("om_p2p", "p2p")
	send("om_group", "group")
	for i := 0; i < 2; i++ {
		select {
		case m := <-got:
			if m.IsPrivate != want[m.MessageID] {
				t.Fatalf("%s: IsPrivate=%v, want %v", m.MessageID, m.IsPrivate, want[m.MessageID])
			}
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for dispatched message")
		}
	}
}
