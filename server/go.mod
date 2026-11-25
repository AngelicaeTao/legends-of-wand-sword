module zjcs/server

go 1.21

// 主 module 只服务 standalone（go run main.go），不依赖 nakama-common。
// 权威 RPC 插件是独立 module：见 nakama_module/go.mod（go 1.23.2，与 Nakama 3.24.0 对齐）。
