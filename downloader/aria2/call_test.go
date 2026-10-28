package aria2

import (
	"context"
	"testing"
	"time"
)

func TestWebsocketCaller(t *testing.T) {
	time.Sleep(time.Second)
	endpoint := localAria2Websocket(t)
	c, err := newWebsocketCaller(context.Background(), endpoint, time.Second, &DummyNotifier{})
	if err != nil {
		t.Fatal(err.Error())
	}
	defer c.Close()

	var info VersionInfo
	if err := c.Call(aria2GetVersion, []any{}, &info); err != nil {
		t.Error(err.Error())
	} else {
		println(info.Version)
	}
}
