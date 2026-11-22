package ipc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIPCRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Serve(ctx, p, func(_ context.Context, r Request) Response {
		return Response{Text: r.Op + ":" + r.SessionID + ":" + string(r.Args)}
	})
	var resp Response
	var err error
	for i := 0; i < 100; i++ {
		resp, err = Call(ctx, p, Request{Op: "send", SessionID: "s1", Args: json.RawMessage(`{"text":"hi"}`)})
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || resp.Text != `send:s1:{"text":"hi"}` {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v", st.Mode().Perm())
	}
}

func TestCallDaemonDownFailsFast(t *testing.T) {
	start := time.Now()
	_, err := Call(context.Background(), filepath.Join(t.TempDir(), "none.sock"), Request{Op: "x"})
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("err=%v after %v", err, time.Since(start))
	}
}

func TestServerCancelsWhenClientGoes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelled := make(chan struct{})
	go Serve(ctx, p, func(hctx context.Context, r Request) Response {
		<-hctx.Done()
		close(cancelled)
		return Response{}
	})
	time.Sleep(50 * time.Millisecond)
	cctx, ccancel := context.WithCancel(context.Background())
	go Call(cctx, p, Request{Op: "approve"})
	time.Sleep(50 * time.Millisecond)
	ccancel()
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("handler context not cancelled after the caller went away")
	}
}
