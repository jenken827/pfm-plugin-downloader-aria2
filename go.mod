module github.com/jenken827/pfm-plugin-downloader-aria2

go 1.26.0

require (
	github.com/gorilla/websocket v1.5.3
	github.com/jenken827/pfm-plugin-sdk v0.3.0
	github.com/sirupsen/logrus v1.9.4
)

require (
	github.com/stretchr/testify v1.11.1 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

// 本地要连改 SDK 时用工作区，不要往这里加 replace：
//   go work init . ../pfm-plugin-sdk   （go.work 不入库）
