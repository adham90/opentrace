package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSlackSender_PostsMrkdwnPayload(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("bad JSON body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sender := NewSlackSender(func() *SlackConfig {
		return &SlackConfig{WebhookURL: srv.URL, Enabled: true}
	})
	if err := sender.Send(context.Background(), "🚨 <b>Watch alert</b>\n<b>Value:</b> 12 &gt; 10"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	want := "🚨 *Watch alert*\n*Value:* 12 > 10"
	if got["text"] != want {
		t.Errorf("text = %q, want %q", got["text"], want)
	}
}

func TestSlackSender_SkipsWhenNotConfigured(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("no request should be sent when Slack is disabled")
	}))
	defer srv.Close()

	for name, cfg := range map[string]*SlackConfig{
		"nil":        nil,
		"disabled":   {WebhookURL: srv.URL, Enabled: false},
		"no webhook": {Enabled: true},
	} {
		sender := NewSlackSender(func() *SlackConfig { return cfg })
		if err := sender.Send(context.Background(), "hi"); err != nil {
			t.Errorf("%s: expected silent skip, got %v", name, err)
		}
	}
}

func TestSlackSender_WebhookURLRedactedInError(t *testing.T) {
	const secret = "https://hooks.slack.com/services/T000/B000/SuperSecretToken"

	sender := NewSlackSender(func() *SlackConfig {
		return &SlackConfig{WebhookURL: secret, Enabled: true}
	})
	sender.client = &http.Client{Transport: errRoundTripper{}}

	err := sender.Send(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected a transport error, got nil")
	}
	if strings.Contains(err.Error(), "SuperSecretToken") {
		t.Fatalf("webhook URL leaked in error: %q", err.Error())
	}
}

func TestHTMLToMrkdwn(t *testing.T) {
	cases := map[string]string{
		"<b>bold</b>":           "*bold*",
		"<i>it</i>":             "_it_",
		"<code>x</code>":        "`x`",
		`<a href="u">link</a>`:  "link", // unknown tags stripped
		"a &amp; b &lt;c&gt;":   "a & b <c>",
		"plain text, no markup": "plain text, no markup",
	}
	for in, want := range cases {
		if got := htmlToMrkdwn(in); got != want {
			t.Errorf("htmlToMrkdwn(%q) = %q, want %q", in, got, want)
		}
	}
}
