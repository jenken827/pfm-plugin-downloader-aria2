// 命令 pfm-plugin-downloader-aria2 以 pfm 插件身份提供 aria2 离线下载后端。
package main

import (
	"github.com/jenken827/pfm-plugin-sdk/plugin/sdk"

	"github.com/jenken827/pfm-plugin-downloader-aria2/downloader"
)

func main() {
	sdk.MustServeDownloader("aria2", downloader.New())
}
