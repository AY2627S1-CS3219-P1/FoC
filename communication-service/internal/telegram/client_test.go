package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const token = "123:secret-token"

func TestClientCallsBotAPI(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = io.WriteString(w, `{"ok":true,"result":{"id":1,"is_bot":true,"username":"foc_bot"}}`)
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			_, _ = io.WriteString(w, `{"ok":true,"result":[{"update_id":7,"message":{"message_id":1,"chat":{"id":42,"type":"private"},"text":"/start"}}]}`)
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":99,"chat":{"id":42,"type":"private"},"text":"hi"}}`)
		default:
			_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
		}
	}))
	defer srv.Close()
	client := NewClient(token, srv.URL, srv.Client())
	ctx := context.Background()

	me, err := client.GetMe(ctx)
	if err != nil || me.Username != "foc_bot" {
		t.Fatalf("GetMe = %+v, %v", me, err)
	}
	if gotPath != "/bot"+token+"/getMe" {
		t.Fatalf("path = %q", gotPath)
	}

	updates, err := client.GetUpdates(ctx, 5, 30*time.Second)
	if err != nil || len(updates) != 1 || updates[0].Message.Chat.ID != 42 {
		t.Fatalf("GetUpdates = %+v, %v", updates, err)
	}
	if gotBody["offset"] != float64(5) || gotBody["timeout"] != float64(30) {
		t.Fatalf("getUpdates body = %v", gotBody)
	}

	if err := client.SendMessage(ctx, 42, "hi"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if gotBody["chat_id"] != float64(42) || gotBody["text"] != "hi" {
		t.Fatalf("sendMessage body = %v", gotBody)
	}

	kb := &InlineKeyboard{Rows: [][]InlineButton{{{Text: "Fine Food order", CallbackData: "chat:1"}}}}
	sent, err := client.Send(ctx, 42, "pick one", kb)
	if err != nil || sent.MessageID != 99 {
		t.Fatalf("Send = %+v, %v", sent, err)
	}
	markup, _ := json.Marshal(gotBody["reply_markup"])
	if string(markup) != `{"inline_keyboard":[[{"callback_data":"chat:1","text":"Fine Food order"}]]}` {
		t.Fatalf("reply_markup = %s", markup)
	}

	if err := client.AnswerCallback(ctx, "cb1", "Chat closed"); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/bot"+token+"/answerCallbackQuery" || gotBody["callback_query_id"] != "cb1" || gotBody["text"] != "Chat closed" {
		t.Fatalf("answerCallbackQuery %q body = %v", gotPath, gotBody)
	}
}

func TestClientReportsRejectedToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
	}))
	defer srv.Close()

	_, err := NewClient(token, srv.URL, srv.Client()).GetMe(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestClientReportsAPIErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`)
	}))
	defer srv.Close()

	err := NewClient(token, srv.URL, srv.Client()).SendMessage(context.Background(), 1, "hi")
	if err == nil || !strings.Contains(err.Error(), "chat not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestClientKeepsTokenOutOfNetworkErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // Connection refused from now on.

	_, err := NewClient(token, url, nil).GetMe(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("error leaks the token: %v", err)
	}
}

func TestClientReturnsAPIErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 7","parameters":{"retry_after":7}}`)
	}))
	defer srv.Close()

	err := NewClient(token, srv.URL, srv.Client()).SendMessage(context.Background(), 42, "hi")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != 429 || apiErr.RetryAfter != 7*time.Second || apiErr.Method != "sendMessage" {
		t.Fatalf("err = %#v", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Fatal("error leaks the bot token")
	}
}

func TestAPIErrorClassification(t *testing.T) {
	cases := []struct {
		err                APIError
		chatGone, rejected bool
	}{
		{APIError{Code: 403, Description: "Forbidden: bot was blocked by the user"}, true, true},
		{APIError{Code: 400, Description: "Bad Request: chat not found"}, true, true},
		{APIError{Code: 400, Description: "Bad Request: message is too long"}, false, true},
		{APIError{Code: 429}, false, false},
		{APIError{Code: 502}, false, false},
	}
	for _, tc := range cases {
		if tc.err.ChatGone() != tc.chatGone || tc.err.Rejected() != tc.rejected {
			t.Errorf("%+v: ChatGone=%v Rejected=%v", tc.err, tc.err.ChatGone(), tc.err.Rejected())
		}
	}
}
