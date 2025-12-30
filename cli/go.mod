module github.com/bhandras/delight/cli

go 1.24.1

toolchain go1.24.5

require (
	github.com/creack/pty v1.1.21
	github.com/golang-jwt/jwt/v5 v5.3.0
	github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e
	github.com/stretchr/testify v1.11.1
	github.com/zishang520/socket.io/clients/socket/v3 v3.0.0-rc.9
	github.com/zishang520/socket.io/v3 v3.0.0-rc.9
	golang.org/x/crypto v0.45.0
)

require (
	github.com/andybalholm/brotli v1.2.0 // indirect
	github.com/bhandras/delight/protocol v0.0.0
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/gookit/color v1.6.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/klauspost/compress v1.18.1 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/quic-go/quic-go v0.57.1 // indirect
	github.com/vmihailenco/msgpack/v5 v5.4.1 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	github.com/zishang520/socket.io/clients/engine/v3 v3.0.0-rc.9 // indirect
	github.com/zishang520/socket.io/parsers/engine/v3 v3.0.0-rc.9 // indirect
	github.com/zishang520/socket.io/parsers/socket/v3 v3.0.0-rc.9 // indirect
	github.com/zishang520/socket.io/servers/engine/v3 v3.0.0-rc.9 // indirect
	github.com/zishang520/socket.io/servers/socket/v3 v3.0.0-rc.9 // indirect
	github.com/zishang520/webtransport-go v0.9.1 // indirect
	golang.org/x/net v0.47.0 // indirect
	golang.org/x/sys v0.39.0 // indirect
	golang.org/x/text v0.31.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	resty.dev/v3 v3.0.0-beta.4 // indirect
)

replace github.com/bhandras/delight/protocol => ../protocol
