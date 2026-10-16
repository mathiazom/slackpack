// Package slackapi is a minimal Slack Web API client for archiving. It keeps Slack's
// objects as raw JSON, so the archive stores every field Slack sends rather than only
// those a typed client knows about.
package slackapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

const apiUrl = "https://slack.com/api/"

// methodRates are Slack's documented per-method rate limit tiers (tier 2: 20/min,
// tier 3: 50/min, tier 4: 100/min) for the methods used.
var methodRates = map[string]int{
	"auth.test":             100,
	"conversations.list":    20,
	"conversations.history": 50,
	"conversations.replies": 50,
	"users.list":            20,
	"emoji.list":            20,
}

// maxRetries bounds retries of a rate-limited (429) call.
const maxRetries = 5

// ErrGone signals a download that will never succeed (403/404/410), as opposed to a
// transient failure that should be retried on a later pack run.
var ErrGone = errors.New("gone from Slack")

// APIError is a Web API response with ok=false, e.g. "channel_not_found".
type APIError struct {
	Method string
	Code   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s failed: %s", e.Method, e.Code)
}

// Client authenticates as a user session: a token, plus the browser session's "d"
// cookie for xoxc tokens.
type Client struct {
	http     *http.Client
	token    string
	cookie   string
	limiters map[string]*rate.Limiter
}

// Connect creates a client and checks its credentials with auth.test.
func Connect(ctx context.Context, token, cookie string) (*Client, error) {
	if token == "" {
		return nil, errors.New("no Slack token")
	}
	c := &Client{
		http:     &http.Client{Timeout: 5 * time.Minute},
		token:    token,
		cookie:   cookie,
		limiters: map[string]*rate.Limiter{},
	}
	for method, perMinute := range methodRates {
		c.limiters[method] = rate.NewLimiter(rate.Every(time.Minute/time.Duration(perMinute)), 1)
	}
	if err := c.call(ctx, "auth.test", url.Values{}, nil); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) authorize(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	if c.cookie != "" {
		cookie := c.cookie
		if strings.ContainsAny(cookie, "/+=") {
			cookie = url.QueryEscape(cookie)
		}
		req.Header.Set("Cookie", "d="+cookie)
	}
}

// call POSTs a Web API method, waiting out rate limits, and decodes the response into
// out (when not nil). It fails with an *APIError when Slack answers ok=false.
func (c *Client) call(ctx context.Context, method string, params url.Values, out any) error {
	limiter, ok := c.limiters[method]
	if !ok {
		return fmt.Errorf("no rate limit known for %s", method)
	}
	for attempt := 0; ; attempt++ {
		if err := limiter.Wait(ctx); err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiUrl+method, strings.NewReader(params.Encode()))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		c.authorize(req)

		resp, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("%s failed: %w", method, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("%s failed: %w", method, err)
		}

		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxRetries {
			wait, err := strconv.Atoi(resp.Header.Get("Retry-After"))
			if err != nil || wait < 1 {
				wait = 1
			}
			select {
			case <-time.After(time.Duration(wait) * time.Second):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%s failed with status: %d", method, resp.StatusCode)
		}

		var status struct {
			Ok    bool   `json:"ok"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(body, &status); err != nil {
			return fmt.Errorf("failed to parse %s response: %w", method, err)
		}
		if !status.Ok {
			return &APIError{method, status.Error}
		}
		if out == nil {
			return nil
		}
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("failed to parse %s response: %w", method, err)
		}
		return nil
	}
}

// paginate calls a cursor-paginated method until the last page, collecting the raw
// objects under itemsKey.
func (c *Client) paginate(ctx context.Context, method string, params url.Values, itemsKey string) ([]json.RawMessage, error) {
	var items []json.RawMessage
	for {
		var page map[string]json.RawMessage
		if err := c.call(ctx, method, params, &page); err != nil {
			return items, err
		}
		var pageItems []json.RawMessage
		if raw, ok := page[itemsKey]; ok {
			if err := json.Unmarshal(raw, &pageItems); err != nil {
				return items, fmt.Errorf("failed to parse %s %s: %w", method, itemsKey, err)
			}
		}
		items = append(items, pageItems...)

		var meta struct {
			NextCursor string `json:"next_cursor"`
		}
		if raw, ok := page["response_metadata"]; ok {
			_ = json.Unmarshal(raw, &meta)
		}
		if meta.NextCursor == "" {
			return items, nil
		}
		params.Set("cursor", meta.NextCursor)
	}
}

// PublicChannels lists every public channel, archived ones included.
func (c *Client) PublicChannels(ctx context.Context) ([]json.RawMessage, error) {
	return c.paginate(ctx, "conversations.list", url.Values{"types": {"public_channel"}, "exclude_archived": {"false"}, "limit": {"100"}}, "channels")
}

// Users lists every user, deactivated ones included.
func (c *Client) Users(ctx context.Context) ([]json.RawMessage, error) {
	return c.paginate(ctx, "users.list", url.Values{"include_locale": {"true"}, "limit": {"200"}}, "members")
}

// History lists a channel's messages, newest first, without thread replies.
func (c *Client) History(ctx context.Context, channelId string) ([]json.RawMessage, error) {
	return c.paginate(ctx, "conversations.history", url.Values{"channel": {channelId}, "limit": {"100"}}, "messages")
}

// Replies lists a thread's replies, without its parent (which Slack repeats on every
// page).
func (c *Client) Replies(ctx context.Context, channelId, threadTs string) ([]json.RawMessage, error) {
	messages, err := c.paginate(ctx, "conversations.replies", url.Values{"channel": {channelId}, "ts": {threadTs}, "limit": {"200"}}, "messages")
	if err != nil {
		return nil, err
	}
	replies := messages[:0]
	for _, message := range messages {
		var m struct {
			Ts string `json:"ts"`
		}
		if err := json.Unmarshal(message, &m); err != nil {
			return nil, fmt.Errorf("failed to parse reply: %w", err)
		}
		if m.Ts != threadTs {
			replies = append(replies, message)
		}
	}
	return replies, nil
}

// Emoji maps each custom emoji name to its image URL, or "alias:<name>".
func (c *Client) Emoji(ctx context.Context) (map[string]string, error) {
	var response struct {
		Emoji map[string]string `json:"emoji"`
	}
	if err := c.call(ctx, "emoji.list", url.Values{}, &response); err != nil {
		return nil, err
	}
	return response.Emoji, nil
}

// Download fetches a file (or thumbnail) URL from files.slack.com. It wraps ErrGone
// when the file is permanently unavailable.
func (c *Client) Download(ctx context.Context, fileUrl string) ([]byte, error) {
	if fileUrl == "" {
		return nil, errors.New("empty download URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileUrl, nil)
	if err != nil {
		return nil, err
	}
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var data bytes.Buffer
		_, err := io.Copy(&data, resp.Body)
		return data.Bytes(), err
	case http.StatusForbidden, http.StatusNotFound, http.StatusGone:
		return nil, fmt.Errorf("%w: status %d", ErrGone, resp.StatusCode)
	default:
		return nil, fmt.Errorf("download failed with status: %d", resp.StatusCode)
	}
}
