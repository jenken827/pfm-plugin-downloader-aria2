package aria2

import (
	"context"
	"os"
	"testing"
	"time"
)

// localAria2 返回本机 aria2 的 RPC 地址与 token；不可用时跳过用例。
//
// 这些用例依赖本机 aria2 服务（未启动、端口不通、或服务端要求 token 而本地
// 没配 token 都属环境问题），不该算作代码失败。需要覆盖真实 aria2 时用
// ARIA2_RPC_ENDPOINT / ARIA2_RPC_TOKEN 两个环境变量指定。
func localAria2(t *testing.T) (endpoint, token string) {
	t.Helper()
	endpoint = os.Getenv("ARIA2_RPC_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://127.0.0.1:6800/jsonrpc"
	}
	token = os.Getenv("ARIA2_RPC_TOKEN")

	c, err := New(context.Background(), endpoint, token, 2*time.Second, nil)
	if err != nil {
		t.Skipf("本机 aria2 RPC 不可用，跳过：%v", err)
	}
	defer c.Close()
	if _, err := c.GetVersion(); err != nil {
		t.Skipf("本机 aria2 RPC 不可用（%s），跳过：%v", endpoint, err)
	}
	return endpoint, token
}

// localAria2Websocket 同 localAria2，但用于 websocket 传输的用例。
// websocketCaller 不支持 token 参数，服务端设了 --rpc-secret 时无法调用，跳过。
func localAria2Websocket(t *testing.T) string {
	t.Helper()
	endpoint, token := localAria2(t)
	if token != "" {
		t.Skip("本机 aria2 配了 rpc-secret，而 websocket 传输不支持 token 参数，跳过")
	}
	switch {
	case len(endpoint) > 5 && endpoint[:5] == "ws://":
		return endpoint
	case len(endpoint) > 6 && endpoint[:6] == "https:":
		return "wss://" + endpoint[len("https://"):]
	default:
		return "ws://" + endpoint[len("http://"):]
	}
}
