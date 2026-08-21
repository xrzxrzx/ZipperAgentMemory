package mcp

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
)

// IPAllowList 对入站请求实施基于远端 IP 的精确白名单访问控制
// （design.md §10 决策 6：8931 端口对公网开放时的访问控制）。
//
// 用法：serve 的 -allow-ips 解析后构造并包装 /mcp 路由；白名单为空时
// [Middleware] 原样透传——本地模式（监听 127.0.0.1，远端即 127.0.0.1）
// 默认无感知，向后兼容。匹配基于 netip 规范化地址：IPv4 与
// IPv4-mapped IPv6（::ffff:a.b.c.d）视为同一地址（Unmap 归一）。
type IPAllowList struct {
	allowed map[netip.Addr]struct{}
}

// NewIPAllowList 解析并校验 IP 列表（空列表 = 不限制）。任一条目非法
// 返回错误，serve 据此快速失败而非带病运行。
func NewIPAllowList(ips []string) (*IPAllowList, error) {
	l := &IPAllowList{allowed: make(map[netip.Addr]struct{}, len(ips))}
	for _, s := range ips {
		a, err := netip.ParseAddr(s)
		if err != nil {
			return nil, fmt.Errorf("mcp: invalid allow-ip %q: %w", s, err)
		}
		l.allowed[a.Unmap()] = struct{}{}
	}
	return l, nil
}

// Empty 报告白名单是否为空（空 = 不限制）。
func (l *IPAllowList) Empty() bool {
	return l == nil || len(l.allowed) == 0
}

// Allowed 报告远端地址（"host:port" 或裸 IP）是否命中白名单。
// 空白名单恒为 true；无法解析的远端地址一律拒绝（fail closed）。
func (l *IPAllowList) Allowed(remoteAddr string) bool {
	if l.Empty() {
		return true
	}
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	_, ok := l.allowed[a.Unmap()]
	return ok
}

// Middleware 返回包装 next 的访问控制中间件：未命中白名单返回 403；
// 空白名单直接返回 next（零开销，本地模式无感知）。
func (l *IPAllowList) Middleware(next http.Handler) http.Handler {
	if l.Empty() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.Allowed(r.RemoteAddr) {
			http.Error(w, "403 forbidden: source IP not allowed", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
