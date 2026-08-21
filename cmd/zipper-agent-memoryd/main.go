// 命令 zipper-agent-memoryd 是 ZipperAgentMemory 的记忆守护进程入口。
//
// 阶段 0 仅输出版本号后退出；serve（常驻 + streamable HTTP）与 stdio
// 两种运行模式将在后续阶段实现（见 docs/design.md §4.1）。
package main

import "fmt"

// version 是守护进程版本号，对应 docs/design.md 的阶段交付规划。
const version = "v0.1.0"

func main() {
	fmt.Printf("zipper-agent-memoryd %s\n", version)
}
