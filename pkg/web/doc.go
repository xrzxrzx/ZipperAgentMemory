// Package web 提供记忆库的只读 Web 阅读界面（需求-001-记忆阅读页）。
//
// 职责：把 memory.Store 的文件树与 index.Index 的全文检索能力，以**只读**
// HTTP 接口暴露给浏览器；写操作一概不提供——需要写仍走 MCP / CLI。
//
// 认证模型：单一长期有效的口令（token），来源是本地文件（默认
// <工作目录>/web-token，权限 0600，内容一行）。口令以两种方式提交：
//
//   - 页面登录：POST /memory/api/login 校验通过后下发持久 Cookie
//     （HttpOnly + SameSite=Lax；HTTPS 下再置 Secure）。**必须用 Cookie 的原因**：
//     浏览器在顶层导航请求里不会发送 Authorization 头，纯 Bearer 方案一刷新即掉登录；
//   - 脚本/命令行：Authorization: Bearer <token>。
//
// 两种凭据都经 crypto/subtle 常量时间比较，失败按远端 IP 限流。
//
// 路由（全部挂在 /memory/ 前缀下，线上由 nginx 反代到 127.0.0.1:8932）：
//
//	GET  /memory/login                       公开登录页（不含任何库内容）
//	POST /memory/api/login                   口令换 Cookie
//	POST /memory/api/logout                  清 Cookie
//	GET  /memory/api/status                  库统计
//	GET  /memory/api/tree?path=              目录项（不递归）
//	GET  /memory/api/file?path=              正文（Markdown 渲染 / 文本 / 二进制提示）
//	GET  /memory/api/raw?path=               原始字节下载
//	GET  /memory/api/search?q=&mode=&limit=  全文检索（and/or 两种语义）
//	GET  /memory/static/*                    内嵌静态资源（需登录）
//
// 安全：所有 path 经 memory.Store 的路径沙箱校验；Markdown 渲染未启用
// goldmark 的 html.WithUnsafe()，库内从外部抓取的资料里若含原始 HTML/脚本，
// 会被省略为注释标记（`<!-- raw HTML omitted -->`）而不会在浏览器执行。
package web
