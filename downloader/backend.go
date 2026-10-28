// Package downloader 把 aria2 的 RPC 客户端适配成 pfm 的 kind=downloader 插件接口。
//
// pfm 只认「提交任务 / 查进度 / 取消」，这里负责：
//   - 用 downloader.options 里的 endpoint / token / timeout 连 aria2（支持 http(s) 与 ws(s)）；
//   - 把 aria2 的状态归一成 DownloadStatus；
//   - 把 aria2 的字符串数值原样透出（协议就是字符串）。
//
// 注意：插件不导入 aria2 的通知（WebSocket 推送）——宿主用周期对齐拿状态，
// 少一条推送通道就少一套重连 / 补发逻辑。
package downloader

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jenken827/pfm-plugin-sdk/proto/gen/plugin"

	"github.com/jenken827/pfm-plugin-downloader-aria2/downloader/aria2"
)

// defaultTimeout 连 aria2 的超时（options.timeout 单位秒）
const defaultTimeout = 30 * time.Second

// aria2TaskKeys 宿主需要的字段一次取全（避免 N+1）
var aria2TaskKeys = []string{
	"gid", "status", "dir", "files", "totalLength", "completedLength",
	"downloadSpeed", "infoHash", "followedBy", "errorCode", "errorMessage", "bittorrent",
}

// Backend aria2 后端实现
type Backend struct {
	mu  sync.RWMutex
	cli aria2.Client
}

var _ interface {
	Configure(ctx context.Context, options map[string]string) (string, error)
	AddURI(ctx context.Context, uris []string, options map[string]string) (string, error)
} = (*Backend)(nil)

// New 创建后端（尚未连接）
func New() *Backend { return &Backend{} }

// Configure 连接 aria2 并校验可用性
func (b *Backend) Configure(_ context.Context, options map[string]string) (string, error) {
	endpoint := strings.TrimSpace(options["endpoint"])
	if endpoint == "" {
		return "", errors.New("缺少 options.endpoint（aria2 的 RPC 地址，如 http://127.0.0.1:6800/jsonrpc）")
	}
	timeout := defaultTimeout
	if raw := strings.TrimSpace(options["timeout"]); raw != "" {
		seconds, err := strconv.Atoi(raw)
		if err != nil || seconds <= 0 {
			return "", fmt.Errorf("options.timeout 必须是正整数秒，实际：%q", raw)
		}
		timeout = time.Duration(seconds) * time.Second
	}
	cli, err := aria2.New(context.Background(), endpoint, strings.TrimSpace(options["token"]), timeout, nil)
	if err != nil {
		return "", fmt.Errorf("连接 aria2 失败（%s）: %w", endpoint, err)
	}
	version, err := cli.GetVersion()
	if err != nil {
		return "", fmt.Errorf("读取 aria2 版本失败（%s）: %w", endpoint, err)
	}
	b.mu.Lock()
	b.cli = cli
	b.mu.Unlock()
	return version.Version, nil
}

// client 取当前连接（未 Configure 时报可读错误）
func (b *Backend) client() (aria2.Client, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.cli == nil {
		return nil, errors.New("后端尚未初始化（宿主没有下发 Configure）")
	}
	return b.cli, nil
}

// AddURI 提交 URL 任务（磁力也是 URL）
func (b *Backend) AddURI(_ context.Context, uris []string, options map[string]string) (string, error) {
	if len(uris) == 0 {
		return "", errors.New("uris 为空")
	}
	cli, err := b.client()
	if err != nil {
		return "", err
	}
	opts := make(map[string]any, len(options))
	for key, value := range options {
		opts[key] = value
	}
	var gid string
	if len(opts) > 0 {
		gid, err = cli.AddURI(uris, opts)
	} else {
		gid, err = cli.AddURI(uris)
	}
	if err != nil {
		return "", fmt.Errorf("aria2.addUri 失败: %w", err)
	}
	return gid, nil
}

// TellStatus 查单个任务
func (b *Backend) TellStatus(_ context.Context, gid string) (*plugin.DownloadTask, error) {
	if strings.TrimSpace(gid) == "" {
		return nil, errors.New("gid 为空")
	}
	cli, err := b.client()
	if err != nil {
		return nil, err
	}
	info, err := cli.TellStatus(gid, aria2TaskKeys...)
	if err != nil {
		return nil, fmt.Errorf("aria2.tellStatus(%s) 失败: %w", gid, err)
	}
	return toProtoTask(info), nil
}

// ListTasks 按状态列任务
func (b *Backend) ListTasks(_ context.Context, states []plugin.DownloadStatus) ([]*plugin.DownloadTask, error) {
	cli, err := b.client()
	if err != nil {
		return nil, err
	}
	infos := make([]aria2.StatusInfo, 0, 64)
	for _, fetch := range b.fetches(cli, states) {
		items, fetchErr := fetch()
		if fetchErr != nil {
			return nil, fetchErr
		}
		infos = append(infos, items...)
	}
	tasks := make([]*plugin.DownloadTask, 0, len(infos))
	for _, info := range infos {
		tasks = append(tasks, toProtoTask(info))
	}
	return tasks, nil
}

// fetches 按请求的状态决定拉哪几类任务；空表示全部
func (b *Backend) fetches(cli aria2.Client, states []plugin.DownloadStatus) []func() ([]aria2.StatusInfo, error) {
	want := func(s plugin.DownloadStatus) bool {
		if len(states) == 0 {
			return true
		}
		for _, state := range states {
			if state == s {
				return true
			}
		}
		return false
	}
	var fetches []func() ([]aria2.StatusInfo, error)
	if want(plugin.DownloadStatus_DOWNLOAD_STATUS_ACTIVE) {
		fetches = append(fetches, func() ([]aria2.StatusInfo, error) {
			return cli.TellActive(aria2TaskKeys...)
		})
	}
	if want(plugin.DownloadStatus_DOWNLOAD_STATUS_WAITING) || want(plugin.DownloadStatus_DOWNLOAD_STATUS_PAUSED) {
		fetches = append(fetches, func() ([]aria2.StatusInfo, error) {
			return cli.TellWaiting(0, 1000, aria2TaskKeys...)
		})
	}
	// 已停止的三态（complete / error / removed）都是 tellStopped 里的
	wantStopped := want(plugin.DownloadStatus_DOWNLOAD_STATUS_COMPLETE) ||
		want(plugin.DownloadStatus_DOWNLOAD_STATUS_ERROR) ||
		want(plugin.DownloadStatus_DOWNLOAD_STATUS_REMOVED)
	if wantStopped {
		fetches = append(fetches, func() ([]aria2.StatusInfo, error) {
			return cli.TellStopped(0, 1000, aria2TaskKeys...)
		})
	}
	return fetches
}

// Remove 停止任务（保留结果）
func (b *Backend) Remove(_ context.Context, gid string) error {
	cli, err := b.client()
	if err != nil {
		return err
	}
	if _, err := cli.ForceRemove(gid); err != nil {
		return fmt.Errorf("aria2.forceRemove(%s) 失败: %w", gid, err)
	}
	return nil
}

// RemoveResult 删除任务结果
func (b *Backend) RemoveResult(_ context.Context, gid string) error {
	cli, err := b.client()
	if err != nil {
		return err
	}
	if _, err := cli.RemoveDownloadResult(gid); err != nil {
		return fmt.Errorf("aria2.removeDownloadResult(%s) 失败: %w", gid, err)
	}
	return nil
}

// Version 后端版本号
func (b *Backend) Version(_ context.Context) (string, error) {
	cli, err := b.client()
	if err != nil {
		return "", err
	}
	info, err := cli.GetVersion()
	if err != nil {
		return "", err
	}
	return info.Version, nil
}

// toProtoTask aria2 状态 → 协议消息
func toProtoTask(info aria2.StatusInfo) *plugin.DownloadTask {
	task := &plugin.DownloadTask{
		Gid:             stringPtr(info.Gid),
		Status:          statusEnum(info.Status),
		Dir:             stringPtr(info.Dir),
		TotalLength:     stringPtr(info.TotalLength),
		CompletedLength: stringPtr(info.CompletedLength),
		DownloadSpeed:   stringPtr(info.DownloadSpeed),
		InfoHash:        stringPtr(strings.ToLower(info.InfoHash)),
		ErrorCode:       stringPtr(info.ErrorCode),
		ErrorMessage:    stringPtr(info.ErrorMessage),
		TorrentName:     stringPtr(info.BitTorrent.Info.Name),
	}
	if len(info.FollowedBy) > 0 {
		task.FollowedBy = info.FollowedBy
	}
	if len(info.Files) > 0 {
		task.Files = make([]*plugin.DownloadFile, 0, len(info.Files))
		for _, f := range info.Files {
			file := &plugin.DownloadFile{
				Path:            stringPtr(f.Path),
				Length:          stringPtr(f.Length),
				CompletedLength: stringPtr(f.CompletedLength),
				Selected:        stringPtr(f.Selected),
			}
			for _, u := range f.URIs {
				file.Uris = append(file.Uris, &plugin.DownloadURI{
					Uri:    stringPtr(u.URI),
					Status: stringPtr(u.Status),
				})
			}
			task.Files = append(task.Files, file)
		}
	}
	return task
}

// statusEnum aria2 状态字符串 → 协议枚举
func statusEnum(status string) *plugin.DownloadStatus {
	var value plugin.DownloadStatus
	switch status {
	case "waiting":
		value = plugin.DownloadStatus_DOWNLOAD_STATUS_WAITING
	case "active":
		value = plugin.DownloadStatus_DOWNLOAD_STATUS_ACTIVE
	case "paused":
		value = plugin.DownloadStatus_DOWNLOAD_STATUS_PAUSED
	case "complete":
		value = plugin.DownloadStatus_DOWNLOAD_STATUS_COMPLETE
	case "error":
		value = plugin.DownloadStatus_DOWNLOAD_STATUS_ERROR
	case "removed":
		value = plugin.DownloadStatus_DOWNLOAD_STATUS_REMOVED
	default:
		value = plugin.DownloadStatus_DOWNLOAD_STATUS_UNSPECIFIED
	}
	return &value
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
