module github.com/bhandras/delight/server

go 1.24.1

toolchain go1.24.5

require (
	github.com/gin-contrib/cors v1.7.2
	github.com/gin-gonic/gin v1.10.0
	github.com/golang-jwt/jwt/v5 v5.2.1
	github.com/mattn/go-sqlite3 v1.14.22
	golang.org/x/crypto v0.42.0
)

require (
	github.com/andybalholm/brotli v1.2.0 // indirect
	github.com/bhandras/delight/protocol v0.0.0
	github.com/bytedance/sonic v1.11.6 // indirect
	github.com/bytedance/sonic/loader v0.1.1 // indirect
	github.com/cloudwego/base64x v0.1.4 // indirect
	github.com/cloudwego/iasm v0.2.0 // indirect
	github.com/gabriel-vasile/mimetype v1.4.3 // indirect
	github.com/gin-contrib/sse v0.1.0 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/go-playground/validator/v10 v10.20.0 // indirect
	github.com/goccy/go-json v0.10.2 // indirect
	github.com/gofrs/uuid v4.0.0+incompatible // indirect
	github.com/gomodule/redigo v1.8.4 // indirect
	github.com/google/uuid v1.6.0
	github.com/googollee/go-socket.io v1.7.0
	github.com/gookit/color v1.6.0 // indirect
	github.com/gorilla/websocket v1.5.3
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/compress v1.18.0 // indirect
	github.com/klauspost/cpuid/v2 v2.2.7 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/pelletier/go-toml/v2 v2.2.2 // indirect
	github.com/quic-go/qpack v0.5.1 // indirect
	github.com/quic-go/quic-go v0.54.0 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/ugorji/go/codec v1.2.12 // indirect
	github.com/vmihailenco/msgpack/v5 v5.4.1 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	github.com/zishang520/socket.io/parsers/engine/v3 v3.0.0-rc.8 // indirect
	github.com/zishang520/socket.io/parsers/socket/v3 v3.0.0-rc.8 // indirect
	github.com/zishang520/socket.io/servers/engine/v3 v3.0.0-rc.8 // indirect
	github.com/zishang520/socket.io/servers/socket/v3 v3.0.0-rc.8
	github.com/zishang520/socket.io/v3 v3.0.0-rc.8
	github.com/zishang520/webtransport-go v0.9.1 // indirect
	go.uber.org/mock v0.6.0 // indirect
	golang.org/x/arch v0.8.0 // indirect
	golang.org/x/mod v0.28.0 // indirect
	golang.org/x/net v0.44.0 // indirect
	golang.org/x/sync v0.17.0 // indirect
	golang.org/x/sys v0.36.0 // indirect
	golang.org/x/text v0.29.0 // indirect
	golang.org/x/tools v0.37.0 // indirect
	google.golang.org/protobuf v1.34.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/bhandras/delight/protocol => ../protocol
