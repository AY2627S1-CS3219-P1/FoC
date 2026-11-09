// Package telegram is a minimal Telegram Bot API client: identify the bot,
// long-poll for updates and send messages.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL is the public Bot API endpoint.
const DefaultBaseURL = "https://api.telegram.org"

// ErrUnauthorized means Telegram rejected the bot token.
var ErrUnauthorized = errors.New("telegram rejected the bot token")

// APIError is a request Telegram answered with ok=false, other than a
// rejected token.
type APIError struct {
	Method      string
	Code        int
	Description string
	// Set when Telegram rate-limits the bot (code 429).
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s failed: %d %s", e.Method, e.Code, e.Description)
}

// ChatGone reports whether the chat can no longer receive messages: the user
// blocked the bot or deleted their account, or the chat doesn't exist.
func (e *APIError) ChatGone() bool {
	return e.Code == http.StatusForbidden ||
		(e.Code == http.StatusBadRequest && strings.Contains(strings.ToLower(e.Description), "chat not found"))
}

// Rejected reports whether Telegram will refuse the same request every time,
// unlike rate limits and server errors.
func (e *APIError) Rejected() bool {
	return e.Code >= 400 && e.Code < 500 && e.Code != http.StatusTooManyRequests
}

// Client calls the Bot API for one bot. The zero HTTP client is not usable;
// build one with NewClient.
type Client struct {
	token   string
	baseURL string
	http    *http.Client
}

// NewClient returns a client for token. An empty baseURL means DefaultBaseURL.
func NewClient(token, baseURL string, httpClient *http.Client) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &Client{token: token, baseURL: baseURL, http: httpClient}
}

// User is a Telegram account, including bots.
type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

// Chat is the conversation a message belongs to. For private chats the ID is
// what the bot uses to message that user later.
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// Message is a message in a chat. Text is empty for photos, stickers and
// other non-text messages.
type Message struct {
	MessageID int64  `json:"message_id"`
	From      *User  `json:"from"`
	Chat      Chat   `json:"chat"`
	Date      int64  `json:"date"`
	Text      string `json:"text"`
	// The message this one replies to, if any.
	ReplyToMessage *Message `json:"reply_to_message"`
}

// CallbackQuery is a press of an inline keyboard button.
type CallbackQuery struct {
	ID   string `json:"id"`
	From User   `json:"from"`
	// The message the button was attached to. Absent if it's too old.
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

// InlineButton is one inline keyboard button. Pressing it sends Data back to
// the bot as a CallbackQuery.
type InlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

// InlineKeyboard is a set of button rows shown under a message.
type InlineKeyboard struct {
	Rows [][]InlineButton `json:"inline_keyboard"`
}

// ChatMember is a user's membership in a chat. For a private chat with the
// bot, Status "kicked" means the user blocked the bot.
type ChatMember struct {
	Status string `json:"status"`
}

// ChatMemberUpdated reports a change to the bot's membership in a chat.
type ChatMemberUpdated struct {
	Chat          Chat       `json:"chat"`
	NewChatMember ChatMember `json:"new_chat_member"`
}

// Update is one item from getUpdates.
type Update struct {
	UpdateID      int64              `json:"update_id"`
	Message       *Message           `json:"message"`
	MyChatMember  *ChatMemberUpdated `json:"my_chat_member"`
	CallbackQuery *CallbackQuery     `json:"callback_query"`
}

type response[T any] struct {
	OK          bool   `json:"ok"`
	Result      T      `json:"result"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// GetMe returns the bot's own account, which also checks the token.
func (c *Client) GetMe(ctx context.Context) (User, error) {
	var me User
	err := c.call(ctx, "getMe", struct{}{}, &me)
	return me, err
}

// GetUpdates waits up to timeout for updates after offset. Pass the last
// update ID plus one as offset to acknowledge earlier updates.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error) {
	var updates []Update
	err := c.call(ctx, "getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         int(timeout.Seconds()),
		"allowed_updates": []string{"message", "my_chat_member", "callback_query"},
	}, &updates)
	return updates, err
}

// SendMessage sends plain text to a chat.
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string) error {
	_, err := c.Send(ctx, chatID, text, nil)
	return err
}

// Send sends plain text to a chat, with an optional inline keyboard, and
// returns the sent message.
func (c *Client) Send(ctx context.Context, chatID int64, text string, keyboard *InlineKeyboard) (Message, error) {
	params := map[string]any{"chat_id": chatID, "text": text}
	if keyboard != nil {
		params["reply_markup"] = keyboard
	}
	var sent Message
	err := c.call(ctx, "sendMessage", params, &sent)
	return sent, err
}

// AnswerCallback acknowledges a button press, showing text briefly to the
// user when it isn't empty.
func (c *Client) AnswerCallback(ctx context.Context, callbackID, text string) error {
	params := map[string]any{"callback_query_id": callbackID}
	if text != "" {
		params["text"] = text
	}
	return c.call(ctx, "answerCallbackQuery", params, nil)
}

func (c *Client) call(ctx context.Context, method string, params any, result any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode %s: %w", method, err)
	}
	endpoint := fmt.Sprintf("%s/bot%s/%s", c.baseURL, c.token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build %s: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		// The request URL contains the token; report the method only.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return fmt.Errorf("call %s: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var decoded response[json.RawMessage]
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return fmt.Errorf("decode %s: status %d: %w", method, resp.StatusCode, err)
	}
	if !decoded.OK {
		if decoded.ErrorCode == http.StatusUnauthorized {
			return ErrUnauthorized
		}
		apiErr := &APIError{Method: method, Code: decoded.ErrorCode, Description: decoded.Description}
		if decoded.Parameters != nil {
			apiErr.RetryAfter = time.Duration(decoded.Parameters.RetryAfter) * time.Second
		}
		return apiErr
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(decoded.Result, result); err != nil {
		return fmt.Errorf("decode %s result: %w", method, err)
	}
	return nil
}
