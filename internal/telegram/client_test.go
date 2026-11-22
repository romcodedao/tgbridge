package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type rec struct {
	mu    sync.Mutex
	calls []call
}
type call struct {
	Method string
	Body   map[string]any
}

func fake(t *testing.T, h func(method string, body map[string]any, n int) (int, string)) (*Client, *rec) {
	r := &rec{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		method := req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]
		b, _ := io.ReadAll(req.Body)
		var body map[string]any
		json.Unmarshal(b, &body)
		r.mu.Lock()
		r.calls = append(r.calls, call{method, body})
		n := len(r.calls)
		r.mu.Unlock()
		code, resp := h(method, body, n)
		w.WriteHeader(code)
		io.WriteString(w, resp)
	}))
	t.Cleanup(srv.Close)
	return New("TOKEN", srv.URL), r
}

func TestCallDecodesError(t *testing.T) {
	c, _ := fake(t, func(string, map[string]any, int) (int, string) {
		return 400, `{"ok":false,"error_code":400,"description":"Bad Request: nope"}`
	})
	err := c.Call(context.Background(), "sendMessage", map[string]any{}, nil)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != 400 || !strings.Contains(ae.Description, "nope") {
		t.Fatalf("got %v", err)
	}
}

func TestClientRetriesAfter429(t *testing.T) {
	c, r := fake(t, func(_ string, _ map[string]any, n int) (int, string) {
		if n == 1 {
			return 429, `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":1}}`
		}
		return 200, `{"ok":true,"result":true}`
	})
	c.sleep = func(time.Duration) {}
	if err := c.Call(context.Background(), "sendRichMessageDraft", map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 2 {
		t.Fatalf("calls=%d", len(r.calls))
	}
}

func TestPollerAdvancesOffset(t *testing.T) {
	c, r := fake(t, func(_ string, _ map[string]any, n int) (int, string) {
		switch n {
		case 1:
			return 200, `{"ok":true,"result":[{"update_id":10,"message":{"message_id":1,"chat":{"id":5,"type":"private"},"text":"a"}}]}`
		case 2:
			return 200, `{"ok":true,"result":[{"update_id":11,"stopped_message_generation":{"chat":{"id":5,"type":"private"},"draft_id":7}}]}`
		}
		time.Sleep(50 * time.Millisecond)
		return 200, `{"ok":true,"result":[]}`
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := NewPoller(c, 0).Updates(ctx)
	u1, u2 := <-ch, <-ch
	if u1.Message == nil || u1.Message.Text != "a" || u2.StoppedMessageGeneration == nil || u2.StoppedMessageGeneration.DraftID != 7 {
		t.Fatalf("got %+v %+v", u1, u2)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if off := r.calls[1].Body["offset"]; off != float64(11) {
		t.Fatalf("second offset=%v", off)
	}
	allowed, _ := json.Marshal(r.calls[0].Body["allowed_updates"])
	if !strings.Contains(string(allowed), "stopped_message_generation") {
		t.Fatalf("allowed_updates=%s", allowed)
	}
}

func TestSendRichDraftBody(t *testing.T) {
	c, r := fake(t, func(string, map[string]any, int) (int, string) { return 200, `{"ok":true,"result":true}` })
	err := c.SendRichDraft(context.Background(), 5, 9, 42, RichMessage{Blocks: []Block{Thinking("hmm"), Paragraph("hi")}}, true)
	if err != nil {
		t.Fatal(err)
	}
	b := r.calls[0].Body
	rm := b["rich_message"].(map[string]any)["blocks"].([]any)
	if r.calls[0].Method != "sendRichMessageDraft" || b["draft_id"] != float64(42) || b["can_stop"] != true || b["message_thread_id"] != float64(9) || rm[0].(map[string]any)["type"] != "thinking" {
		t.Fatalf("body=%v", b)
	}
}

func TestThreadZeroOmitted(t *testing.T) {
	c, r := fake(t, func(string, map[string]any, int) (int, string) {
		return 200, `{"ok":true,"result":{"message_id":3,"chat":{"id":5,"type":"private"}}}`
	})
	m, err := c.SendRichMessage(context.Background(), 5, 0, RichMessage{Blocks: []Block{Paragraph("x")}}, nil)
	if err != nil || m.MessageID != 3 {
		t.Fatal(m, err)
	}
	if _, ok := r.calls[0].Body["message_thread_id"]; ok {
		t.Fatal("thread 0 must be omitted")
	}
}

func TestClientRetries429Repeatedly(t *testing.T) {
	c, r := fake(t, func(_ string, _ map[string]any, n int) (int, string) {
		if n <= 3 {
			return 429, `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":1}}`
		}
		return 200, `{"ok":true,"result":true}`
	})
	c.sleep = func(time.Duration) {}
	if err := c.Call(context.Background(), "sendRichMessage", map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 4 {
		t.Fatalf("calls=%d", len(r.calls))
	}
}

func TestClient429HonoursContext(t *testing.T) {
	c, _ := fake(t, func(string, map[string]any, int) (int, string) {
		return 429, `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":30}}`
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	c.Call(ctx, "sendMessage", map[string]any{}, nil)
	if time.Since(start) > 2*time.Second {
		t.Fatal("429 sleep ignored the context")
	}
}

func downloadClient(t *testing.T, fileSize string, file http.HandlerFunc) (*Client, *int) {
	fileHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/file/") {
			fileHits++
			file(w, r)
			return
		}
		io.WriteString(w, `{"ok":true,"result":{"file_id":"F",`+fileSize+`"file_path":"photos/a.jpg"}}`)
	}))
	t.Cleanup(srv.Close)
	return New("T", srv.URL), &fileHits
}

func TestDownload(t *testing.T) {
	c, _ := downloadClient(t, `"file_size":5,`, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/file/botT/photos/a.jpg" {
			t.Errorf("path %s", r.URL.Path)
		}
		io.WriteString(w, "hello")
	})
	var buf strings.Builder
	if err := c.Download(context.Background(), "F", &buf); err != nil || buf.String() != "hello" {
		t.Fatalf("%q %v", buf.String(), err)
	}
}

func TestDownloadTooBigBySize(t *testing.T) {
	c, hits := downloadClient(t, `"file_size":`+strconv.Itoa(MaxDownload+1)+`,`, func(http.ResponseWriter, *http.Request) {})
	if err := c.Download(context.Background(), "F", io.Discard); !errors.Is(err, ErrTooBig) || *hits != 0 {
		t.Fatalf("%v hits=%d", err, *hits)
	}
}

func TestDownloadTooBigByStream(t *testing.T) {
	c, _ := downloadClient(t, ``, func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, MaxDownload+1))
	})
	if err := c.Download(context.Background(), "F", io.Discard); !errors.Is(err, ErrTooBig) {
		t.Fatal(err)
	}
}

func TestDownloadErrorHidesToken(t *testing.T) {
	c, _ := downloadClient(t, ``, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	err := c.Download(context.Background(), "F", io.Discard)
	if err == nil || strings.Contains(err.Error(), "botT") {
		t.Fatal(err)
	}
}

func TestDownloadTransportErrorHidesToken(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL + "/botT/secret"
	dead.Close()
	c, _ := downloadClient(t, ``, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, deadURL, http.StatusFound) })
	err := c.Download(context.Background(), "F", io.Discard)
	if err == nil || strings.Contains(err.Error(), "botT") {
		t.Fatal(err)
	}
}
