package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

// serveConfig 是 serve 子命令的生效配置，合并顺序（design 决策 3/6 补充）：
// flag 显式给出 > 配置文件 > 默认值；全部未给时用默认值。
type serveConfig struct {
	gitAuto  bool     // autocommit 是否开启
	hour     int      // 每日定时提交小时（0-23）
	allowIPs []string // MCP IP 白名单（nil=不限制）
}

// configFile 是 -config JSON 配置文件的磁盘格式。
//
// 示例：
//
//	{
//	  "autocommit": {"enabled": true, "hour": 0},
//	  "allow_ips": ["127.0.0.1", "120.228.126.4"]
//	}
//
// 字段全部可选：缺失时用默认值（autocommit 默认开启、hour 默认 0、
// allow_ips 默认不限制）。指针字段用于区分「缺失」与「显式零值」。
type configFile struct {
	Autocommit *autocommitFile `json:"autocommit"`
	AllowIPs   []string        `json:"allow_ips"`
}

type autocommitFile struct {
	Enabled *bool `json:"enabled"`
	Hour    *int  `json:"hour"`
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

	return cfg, nil
}
