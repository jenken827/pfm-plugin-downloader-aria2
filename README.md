# pfm-plugin-downloader-aria2

aria2 离线下载后端，[pfm](https://github.com/jenken827/pfm) 的官方插件（独立进程，kind=downloader）。

## 这是什么

[pfm](https://github.com/jenken827/pfm) 核心不再内置任何下载器客户端：离线下载的「提交任务 / 查进度 / 取消」由
`kind: downloader` 插件提供，这个插件把 [pfm](https://github.com/jenken827/pfm) 的下载器协议翻译成 aria2 的 JSON-RPC。

下载产物落在**宿主机**上（插件与宿主同机），算 blake3、建文件记录、分发到各
存储提供者这些入库动作仍然由 [pfm](https://github.com/jenken827/pfm) 做——插件不碰数据库与存储。

## 配置

[pfm](https://github.com/jenken827/pfm) 的 `config.yaml`：

```yaml
plugin:
  enabled: true          # 插件总开关
  dir: plugins

downloader:
  plugin: aria2          # 用哪个 downloader_id 的插件；留空则离线下载不可用
  options:               # 原样下发给插件
    endpoint: http://127.0.0.1:6800/jsonrpc
    token: ""            # aria2 的 --rpc-secret，没有就留空
    timeout: "30"        # 连接/响应超时（秒）
```

aria2 需要开 RPC：

```bash
aria2c --enable-rpc --rpc-listen-port=6800 --rpc-secret=<口令> --dir=<下载目录>
```

（`endpoint` 也支持 `ws://` / `wss://`，插件用的是同一个客户端。）

## 构建

依赖 `github.com/jenken827/pfm-plugin-sdk`，版本写在 `go.mod` 里，按 tag 从 Go module 代理取：

```bash
go build -trimpath -o pfm-plugin-downloader-aria2 .
```

要同时改 SDK 时用工作区把两个目录并起来，`go.work` 不入库：

```bash
go work init . ../pfm-plugin-sdk
```

插件与 SDK 的协议版本必须一致（`plugin.json` 的 `abi_version` 对应 SDK 的 `plugin/sdk.AbiVersion`）。

## 安装

目录布局（放到 [pfm](https://github.com/jenken827/pfm) 的 `plugin.dir` 下）：

```
plugins/pfm-plugin-downloader-aria2/
  plugin.json
  pfm-plugin-downloader-aria2
```

或从官方索引安装：`pfm plugin install pfm-plugin-downloader-aria2`，装完自动热加载
（`SIGHUP`），不用重启服务；`downloader.plugin` 这类配置项改动仍需重启。

## 插件做什么 / 不做什么

| 由插件负责 | 由 [pfm](https://github.com/jenken827/pfm) 负责 |
|---|---|
| 连 aria2（http/ws）、提交 URL/磁力、查状态、取消 | 校验提交的 URL（SSRF 防护）、建任务记录 |
| 把 aria2 的状态归一成协议里的 `DownloadStatus` | 找产物文件、算 blake3、建文件记录、分发 provider |
| 数值字段格式化（协议里长度/速度是十进制字符串） | 完成/失败判定与入库重试 |

宿主不订阅 aria2 的推送通知：[pfm](https://github.com/jenken827/pfm) 每 5 秒对一次任务状态（见 `internal/download/reconcile.go`）。

## 发布

推送 tag 即自动构建并发布（`.github/workflows/release.yml`）：

```bash
# 1) 先把 plugin.json 的 version 改成要发的版本
# 2) 打 tag 并推送（tag 必须等于 v<version>，否则工作流直接失败）
git tag v1.0.0
git push origin v1.0.0
```

工作流交叉编译 `linux-amd64` / `linux-arm64` / `darwin-amd64` / `darwin-arm64` /
`windows-amd64`，把 `pfm-plugin-downloader-aria2-<version>-<platform>.tar.gz`、`checksums.txt`
与 `index-entries.json` 作为资产发布到该 tag 的 GitHub Release。Release 说明由提交历史生成：
`上一个 tag..本 tag` 范围内的提交标题（首个版本列全部提交）。失败可重跑（同名资产覆盖、
说明重写，不会重复建 Release）。

发布后把 `index-entries.json` 里的条目并进索引仓库的 `index.json`（`seq` 递增）并在那里签名——
整份索引共用一个单调递增的 `seq`，所以签名不在本仓库的 CI 里做。

CI 只要 Go：SDK 按 `go.mod` 里的 tag 从 module 代理取，不需要检出宿主或 SDK 的源码，
Go 版本也直接读 `go.mod`。分支与 PR 的常规检查（编译 / vet / gofmt / 测试 / `plugin.json`
字段校验）在 `.github/workflows/ci.yml`。

### 没有 CI 时的手工发布

```bash
# 当前平台打包 + 生成签名索引（私钥只在发布机上使用，不入仓库）
scripts/make-release.sh --version 1.0.0 --seq 1 \
  --repo <owner>/pfm-plugin-downloader-aria2 \
  --base-url <包所在地址> --key <索引签名私钥>

# 或只为多平台包生成索引条目
scripts/package.sh --version 1.0.0 --goos linux --goarch arm64 --out dist
scripts/make-index-entries.sh --version 1.0.0 --tag v1.0.0 \
  --repo <owner>/pfm-plugin-downloader-aria2 --dir dist
```

包名、可执行文件名、`kind` / `downloader_id` 这些字段都从 `plugin.json` 取，脚本里没有第二处定义。
