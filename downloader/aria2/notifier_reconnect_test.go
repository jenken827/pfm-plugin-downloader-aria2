package aria2

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// recordingNotifier 记录收到的通知，用于断言重连后仍能收到通知
type recordingNotifier struct {
	mu  sync.Mutex
	got []string
	ch  chan string
}

func newRecordingNotifier() *recordingNotifier {
	return &recordingNotifier{ch: make(chan string, 32)}
}

func (n *recordingNotifier) record(events []Event) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, e := range events {
		n.got = append(n.got, e.Gid)
		select {
		case n.ch <- e.Gid:
		default:
		}
	}
}

func (n *recordingNotifier) OnDownloadStart(events []Event)      { n.record(events) }
func (n *recordingNotifier) OnDownloadPause(events []Event)      { n.record(events) }
func (n *recordingNotifier) OnDownloadStop(events []Event)       { n.record(events) }
func (n *recordingNotifier) OnDownloadComplete(events []Event)   { n.record(events) }
func (n *recordingNotifier) OnDownloadError(events []Event)      { n.record(events) }
func (n *recordingNotifier) OnBtDownloadComplete(events []Event) { n.record(events) }

// wait 等待指定 gid 的通知到达
func (n *recordingNotifier) wait(gid string, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		select {
		case got := <-n.ch:
			if got == gid {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// 断线必须自动重连：aria2 重启（或连接抖动）后若不重连，通知通道永久失效，
// 所有下载都会停在 PENDING，只能靠重启 pfm 恢复。
func TestNotifierReconnectsAfterDisconnect(t *testing.T) {
	var connects int32
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		if atomic.AddInt32(&connects, 1) == 1 {
			// 第一轮：推一条通知后立刻断开，模拟 aria2 重启
			_ = conn.WriteJSON(map[string]any{
				"method": "aria2.onDownloadStart",
				"params": []Event{{Gid: "gid-1"}},
			})
			return
		}
		// 第二轮（重连后）：推第二条通知，并保持连接
		_ = conn.WriteJSON(map[string]any{
			"method": "aria2.onDownloadStart",
			"params": []Event{{Gid: "gid-2"}},
		})
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL + "/jsonrpc")
	if err != nil {
		t.Fatal(err)
	}
	notifier := newRecordingNotifier()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := newHTTPCaller(ctx, u, 2*time.Second, notifier)
	defer h.Close()

	if !notifier.wait("gid-1", 5*time.Second) {
		t.Fatal("断线前没收到通知，测试前置条件不成立")
	}
	if !notifier.wait("gid-2", 10*time.Second) {
		t.Fatalf("断线后没有收到通知（重连未生效），connects=%d", atomic.LoadInt32(&connects))
	}
	if got := atomic.LoadInt32(&connects); got < 2 {
		t.Errorf("重连次数 = %d，期望至少 2", got)
	}
}

// ctx 结束时读取循环必须退出（否则 Close 会挂住）
func TestNotifierCloseStopsLoop(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL + "/jsonrpc")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := newHTTPCaller(ctx, u, 2*time.Second, newRecordingNotifier())
	time.Sleep(200 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		cancel()
		_ = h.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close 未能退出通知循环")
	}
}
