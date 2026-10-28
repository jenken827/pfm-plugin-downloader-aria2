package downloader

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jenken827/pfm-plugin-sdk/proto/gen/plugin"
)

// localBackend 连本机 aria2；未启动 / 没有 token 时跳过用例。
//
// 用 ARIA2_RPC_ENDPOINT / ARIA2_RPC_TOKEN 指定，默认 http://127.0.0.1:6800/jsonrpc。
func localBackend(t *testing.T) *Backend {
	t.Helper()
	endpoint := os.Getenv("ARIA2_RPC_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://127.0.0.1:6800/jsonrpc"
	}
	b := New()
	version, err := b.Configure(context.Background(), map[string]string{
		"endpoint": endpoint,
		"token":    os.Getenv("ARIA2_RPC_TOKEN"),
		"timeout":  "2",
	})
	if err != nil {
		t.Skipf("本机 aria2 RPC 不可用（%s），跳过：%v", endpoint, err)
	}
	if version == "" {
		t.Skipf("本机 aria2 没有返回版本号（%s），跳过", endpoint)
	}
	return b
}

// TestMagnetMetadataContact 磁力任务在 aria2 上的真实形状：元数据任务自带
// info_hash、files 是 [METADATA] 伪文件。宿主「按 infohash 找产物」依赖这个契约。
func TestMagnetMetadataContact(t *testing.T) {
	b := localBackend(t)
	infohash := randomInfoHash(t)
	uri := "magnet:?xt=urn:btih:" + infohash + "&dn=contract-probe"
	gid, err := b.AddURI(context.Background(), []string{uri}, map[string]string{
		"dir":              t.TempDir(),
		"bt-save-metadata": "false",
	})
	if err != nil {
		t.Fatalf("提交磁力任务失败: %v", err)
	}
	t.Cleanup(func() {
		_ = b.Remove(context.Background(), gid)
		_ = b.RemoveResult(context.Background(), gid)
	})

	task := waitFor(t, b, gid, func(task *plugin.DownloadTask) bool {
		return strings.EqualFold(task.GetInfoHash(), infohash) && hasMetadataFile(task)
	})
	if !strings.EqualFold(task.GetInfoHash(), infohash) {
		t.Fatalf("info_hash = %q, want %q", task.GetInfoHash(), infohash)
	}
	if len(task.GetFiles()) == 0 || !strings.HasPrefix(task.GetFiles()[0].GetPath(), "[METADATA]") {
		t.Fatalf("磁力元数据任务的 files 应以 [METADATA] 开头：%+v", task.GetFiles())
	}
}

// TestURLTaskContact 普通直链任务：无 info_hash、files 是真实文件
func TestURLTaskContact(t *testing.T) {
	b := localBackend(t)
	gid, err := b.AddURI(context.Background(),
		[]string{"http://127.0.0.1:1/never-reachable.bin"}, map[string]string{
			"dir":       t.TempDir(),
			"max-tries": "1",
		})
	if err != nil {
		t.Fatalf("提交 URL 任务失败: %v", err)
	}
	t.Cleanup(func() {
		_ = b.Remove(context.Background(), gid)
		_ = b.RemoveResult(context.Background(), gid)
	})

	task := waitFor(t, b, gid, func(task *plugin.DownloadTask) bool {
		return task.GetInfoHash() == "" && len(task.GetFiles()) > 0 &&
			!strings.HasPrefix(task.GetFiles()[0].GetPath(), "[METADATA]")
	})
	if task.GetInfoHash() != "" {
		t.Fatalf("非 BT 任务不应有 info_hash：%q", task.GetInfoHash())
	}
}

// waitFor 轮询任务直到满足条件（或超时失败）
func waitFor(t *testing.T, b *Backend, gid string,
	ok func(task *plugin.DownloadTask) bool) *plugin.DownloadTask {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var last *plugin.DownloadTask
	for time.Now().Before(deadline) {
		task, err := b.TellStatus(context.Background(), gid)
		if err != nil {
			t.Fatalf("查询任务失败: %v", err)
		}
		last = task
		if ok(task) {
			return task
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("等待任务形状超时，最后状态：status=%v info_hash=%q files=%d",
		last.GetStatus(), last.GetInfoHash(), len(last.GetFiles()))
	return last
}

// hasMetadataFile 是否含 [METADATA] 伪文件
func hasMetadataFile(task *plugin.DownloadTask) bool {
	for _, f := range task.GetFiles() {
		if strings.HasPrefix(f.GetPath(), "[METADATA]") {
			return true
		}
	}
	return false
}

func randomInfoHash(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("生成随机 infohash 失败: %v", err)
	}
	return hex.EncodeToString(buf)
}
