// Package watch 实现记忆库目录树的文件系统监听（阶段 2）。
//
// 基于 fsnotify 递归监听 root 目录（含运行时新建/删除的子目录），把原始
// 文件系统事件按去抖窗口（默认 500ms）合并成一批 Event 交给 Handler——
// 禁止逐事件做 IO（编码规范 §4.4，低资源红线：空闲 CPU≈0，纯事件驱动无轮询）。
//
// watch 与索引解耦：本包只做「事件 → 去抖批量回调」，不做任何索引/过滤
// 决策；pkg/index 的 ShouldIndex 过滤与 Upsert/Remove 由编排方
// （cmd/zipper-agent-memoryd serve）在 Handler 内完成。
//
// 去抖语义（滑动窗口）：窗口内任意新事件都会重置计时，窗口静默 debounce
// 时长后一次性 flush 去重后的批次；ctx 取消时先 flush 剩余事件再退出，
// 保证优雅停机不丢最后一次变更。
package watch
