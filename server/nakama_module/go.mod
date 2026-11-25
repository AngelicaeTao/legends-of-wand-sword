// 权威 RPC 插件的独立 module。
// 版本必须与 Nakama 3.24.0 二进制一致：go1.23.2 + nakama-common v1.34.0 + protobuf v1.34.2。
// 用官方镜像构建：见 scripts/build-plugin.sh。
// module 名必须是主 module 的子路径，才能合法引用 zjcs/server/internal/*（Go internal 规则）。
module zjcs/server/nakama_module

go 1.23.2

require (
	github.com/heroiclabs/nakama-common v1.34.0
	zjcs/server v0.0.0
)

require google.golang.org/protobuf v1.34.2 // indirect

// 复用主 module 的 game/store 纯逻辑（本地路径，无需发布）
replace zjcs/server => ..
