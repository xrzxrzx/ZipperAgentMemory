// Package memory 实现记忆库核心：路径沙箱校验与文件的读、写、追加（原子写）。
//
// Store 持有 memory/ 根路径，所有相对路径操作都经路径沙箱校验
// （filepath.Clean + 根内判定 + 符号链接逃逸防护，见 go-编码规范 §5.1），
// 防止 ../、绝对路径与符号链接逃出记忆库根目录；写操作走
// 「同目录临时文件 + fsync + rename」原子写（规范 §6.1），
// 由互斥锁串行化（设计文档 §6.2）。
//
// 供 cmd/zam CLI 及后续阶段的 watcher / 索引 / MCP server 复用。
package memory
