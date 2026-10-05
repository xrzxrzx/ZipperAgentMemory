package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// 阅读页（web）默认值。
const (
	// defaultWebAddr 是只读阅读页的默认监听地址（仅本机，由 nginx 反代）。
	defaultWebAddr = "127.0.0.1:8932"
	// defaultTokenFileName 是口令文件的默认文件名：放在配置文件同目录
	// （无 -config 时用当前工作目录）。口令**只**从文件读，不走命令行参数
	// ——flag 会出现在 `ps` 输出里。
	defaultTokenFileName = "web-token"
)

// serveConfig 是 serve 子命令的生效配置，合并顺序（design 决策 3/6 补充）：
// flag 显式给出 > 配置文件 > 默认值；全部未给时用默认值。
type serveConfig struct {
	gitAuto  bool     // autocommit 是否开启
	hour     int      // 每日定时提交小时（0-23）
	allowIPs []string // MCP IP 白名单（nil=不限制）

	webEnabled   bool   // 是否启用只读阅读页
	webAddr      string // 阅读页监听地址（host:port）
	webTokenFile string // 口令文件路径
}

// configFile 是 -config JSON 配置文件的磁盘格式。
//
// 示例：
//
//	{
//	  "autocommit": {"enabled": true, "hour": 0},
//	  "allow_ips": ["127.0.0.1", "120.228.126.4"],
//	  "web": {"enabled": true, "addr": "127.0.0.1:8932", "token_file": "/opt/zipper-agent-memory/web-token"}
//	}
//
// 字段全部可选：缺失时用默认值（autocommit 默认开启、hour 默认 0、
// allow_ips 默认不限制、web 默认关闭）。指针字段用于区分「缺失」与「显式零值」。
type configFile struct {
	Autocommit *autocommitFile `json:"autocommit"`
	AllowIPs   []string        `json:"allow_ips"`
	Web        *webFile        `json:"web"`
}

type autocommitFile struct {
	Enabled *bool `json:"enabled"`
	Hour    *int  `json:"hour"`
}

type webFile struct {
	Enabled   *bool   `json:"enabled"`
	Addr      *string `json:"addr"`
	TokenFile *string `json:"token_file"`
}

// flagIsSet 报告 flag 是否被显式给出（flag 包未提供 IsSet；
// Visit 仅遍历被设置过的 flag，语义一致）。
func flagIsSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

// loadServeConfig 解析 -config 配置文件并与 flag 合并为生效配置。
//
// 优先级：flag 显式给出（flagIsSet）优先于配置文件，配置文件优先于默认值。
// 配置文件读取失败（不存在/JSON 非法）或合并后 hour 越界时返回错误
// （启动快速失败，不留带病运行）。
func loadServeConfig(sf *serveFlags, fs *flag.FlagSet) (*serveConfig, error) {
	var file *configFile
	if sf.config != "" {
		data, err := os.ReadFile(sf.config)
		if err != nil {
			return nil, fmt.Errorf("read config %s: %w", sf.config, err)
		}
		// 剥离 UTF-8 BOM（Windows 记事本等编辑器产物，json.Unmarshal 不识别）。
		data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
		file = &configFile{}
		if err := json.Unmarshal(data, file); err != nil {
			return nil, fmt.Errorf("parse config %s: %w", sf.config, err)
		}
	}

	cfg := &serveConfig{}

	// autocommit.enabled：flag 显式 > 配置 > 默认 true（决策 3 默认开启）。
	switch {
	case flagIsSet(fs, "git-autocommit"):
		cfg.gitAuto = sf.gitAuto
	case file != nil && file.Autocommit != nil && file.Autocommit.Enabled != nil:
		cfg.gitAuto = *file.Autocommit.Enabled
	default:
		cfg.gitAuto = true
	}

	// autocommit.hour：flag 显式 > 配置 > 默认 0（每日 0 点）。
	switch {
	case flagIsSet(fs, "git-autocommit-hour"):
		cfg.hour = sf.gitAutoHour
	case file != nil && file.Autocommit != nil && file.Autocommit.Hour != nil:
		cfg.hour = *file.Autocommit.Hour
	default:
		cfg.hour = 0
	}
	if cfg.hour < 0 || cfg.hour > 23 {
		return nil, fmt.Errorf("git autocommit hour %d out of range [0,23] (flag -git-autocommit-hour or config autocommit.hour)", cfg.hour)
	}

	// allow_ips：flag 显式 > 配置 > 默认不限制。IP 合法性由装配层
	// mcp.NewIPAllowList 校验（flag 与配置同路径，快速失败）。
	switch {
	case flagIsSet(fs, "allow-ips"):
		cfg.allowIPs = parseIPList(sf.allowIPs)
	case file != nil && len(file.AllowIPs) > 0:
		cfg.allowIPs = file.AllowIPs
	default:
		cfg.allowIPs = nil
	}

	if err := mergeWebConfig(cfg, sf, fs, file); err != nil {
		return nil, err
	}

	return cfg, nil
}

// mergeWebConfig 合并阅读页配置：
//
//   - 启用开关：`-web-addr` 显式给出即以其为准（空串 = 显式禁用）；否则取
//     配置 web.enabled；都未给则默认关闭（与旧版行为一致，零影响升级）；
//   - 监听地址：`-web-addr` 值 > 配置 web.addr > 默认 127.0.0.1:8932；
//   - 口令文件：配置 web.token_file > 默认（配置文件同目录的 web-token；
//     无 -config 时为工作目录下的 web-token）。**口令不允许来自 flag**——
//     命令行参数会出现在 `ps` 输出里。
//
// 启用时校验 addr 形如 host:port，非法即快速失败。
func mergeWebConfig(cfg *serveConfig, sf *serveFlags, fs *flag.FlagSet, file *configFile) error {
	switch {
	case flagIsSet(fs, "web-addr"):
		cfg.webEnabled = sf.webAddr != ""
		cfg.webAddr = sf.webAddr
	case file != nil && file.Web != nil && file.Web.Enabled != nil:
		cfg.webEnabled = *file.Web.Enabled
	default:
		cfg.webEnabled = false
	}

	switch {
	case cfg.webAddr != "":
		// flag 已给出地址
	case file != nil && file.Web != nil && file.Web.Addr != nil && *file.Web.Addr != "":
		cfg.webAddr = *file.Web.Addr
	default:
		cfg.webAddr = defaultWebAddr
	}

	switch {
	case file != nil && file.Web != nil && file.Web.TokenFile != nil && *file.Web.TokenFile != "":
		cfg.webTokenFile = *file.Web.TokenFile
	case sf.config != "":
		cfg.webTokenFile = filepath.Join(filepath.Dir(sf.config), defaultTokenFileName)
	default:
		cfg.webTokenFile = defaultTokenFileName
	}

	if cfg.webEnabled {
		if _, _, err := net.SplitHostPort(cfg.webAddr); err != nil {
			return fmt.Errorf("invalid web addr %q (want host:port): %w", cfg.webAddr, err)
		}
	}
	return nil
}

// loadWebTokenFile 解析 -config 里的 web.token_file（供 web-token 子命令复用）：
// 未配置时回落到配置文件同目录的 web-token。
func loadWebTokenFile(configPath string) (string, error) {
	if configPath == "" {
		return defaultTokenFileName, nil
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("read config %s: %w", configPath, err)
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	var file configFile
	if err := json.Unmarshal(data, &file); err != nil {
		return "", fmt.Errorf("parse config %s: %w", configPath, err)
	}
	if file.Web != nil && file.Web.TokenFile != nil && *file.Web.TokenFile != "" {
		return *file.Web.TokenFile, nil
	}
	return filepath.Join(filepath.Dir(configPath), defaultTokenFileName), nil
}
