package main

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// newServeFlagSetForTest 构造 serve flag 集并解析 args（ContinueOnError）。
func newServeFlagSetForTest(t *testing.T, args ...string) (*serveFlags, *flag.FlagSet) {
	t.Helper()
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	sf := registerServeFlags(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatalf("flag parse %v: %v", args, err)
	}
	return sf, fs
}

// writeConfigFile 写临时 JSON 配置文件，返回路径。
func writeConfigFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestLoadServeConfigDefaults 验收：无 -config、无 flag → 全默认
// （autocommit 默认开启、hour 默认 0、allow_ips 默认不限制）。
func TestLoadServeConfigDefaults(t *testing.T) {
	sf, fs := newServeFlagSetForTest(t)
	cfg, err := loadServeConfig(sf, fs)
	if err != nil {
		t.Fatalf("loadServeConfig: %v", err)
	}
	if !cfg.gitAuto {
		t.Error("autocommit 默认应开启（决策 3）")
	}
	if cfg.hour != 0 {
		t.Errorf("hour 默认应为 0，实际 %d", cfg.hour)
	}
	if len(cfg.allowIPs) != 0 {
		t.Errorf("allow_ips 默认应为空（不限制），实际 %v", cfg.allowIPs)
	}
}

// TestLoadServeConfigFromFile 验收：配置文件读取正确
// （autocommit.enabled/hour 与 allow_ips 全部生效）。
func TestLoadServeConfigFromFile(t *testing.T) {
	p := writeConfigFile(t, `{"autocommit":{"enabled":false,"hour":23},"allow_ips":["127.0.0.1","120.228.126.4"]}`)
	sf, fs := newServeFlagSetForTest(t, "-config", p)
	cfg, err := loadServeConfig(sf, fs)
	if err != nil {
		t.Fatalf("loadServeConfig: %v", err)
	}
	if cfg.gitAuto {
		t.Error("配置 enabled=false 应生效")
	}
	if cfg.hour != 23 {
		t.Errorf("配置 hour=23 应生效，实际 %d", cfg.hour)
	}
	if want := []string{"127.0.0.1", "120.228.126.4"}; !reflect.DeepEqual(cfg.allowIPs, want) {
		t.Errorf("配置 allow_ips = %v, want %v", cfg.allowIPs, want)
	}
}

// TestLoadServeConfigFlagPriority 验收：flag 显式给出优先于配置文件。
func TestLoadServeConfigFlagPriority(t *testing.T) {
	p := writeConfigFile(t, `{"autocommit":{"enabled":true,"hour":2},"allow_ips":["9.9.9.9"]}`)

	// -allow-ips flag 显式 > 配置 allow_ips。
	sf, fs := newServeFlagSetForTest(t, "-config", p, "-allow-ips", "1.2.3.4")
	cfg, err := loadServeConfig(sf, fs)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"1.2.3.4"}; !reflect.DeepEqual(cfg.allowIPs, want) {
		t.Errorf("flag -allow-ips 应优先，实际 %v", cfg.allowIPs)
	}

	// -git-autocommit=false 显式 > 配置 enabled=true。
	sf, fs = newServeFlagSetForTest(t, "-config", p, "-git-autocommit=false")
	cfg, err = loadServeConfig(sf, fs)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.gitAuto {
		t.Error("flag -git-autocommit=false 应优先于配置 enabled=true")
	}

	// -git-autocommit-hour=5 显式 > 配置 hour=2。
	sf, fs = newServeFlagSetForTest(t, "-config", p, "-git-autocommit-hour", "5")
	cfg, err = loadServeConfig(sf, fs)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.hour != 5 {
		t.Errorf("flag -git-autocommit-hour=5 应优先，实际 %d", cfg.hour)
	}

	// 未显式给 flag → 配置生效（enabled=true、hour=2、allow_ips=[9.9.9.9]）。
	sf, fs = newServeFlagSetForTest(t, "-config", p)
	cfg, err = loadServeConfig(sf, fs)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.gitAuto || cfg.hour != 2 || !reflect.DeepEqual(cfg.allowIPs, []string{"9.9.9.9"}) {
		t.Errorf("未给 flag 时应全部取配置：gitAuto=%v hour=%d allowIPs=%v", cfg.gitAuto, cfg.hour, cfg.allowIPs)
	}

	// -allow-ips 显式空串 = 不限制（覆盖配置白名单，保持向后兼容语义）。
	sf, fs = newServeFlagSetForTest(t, "-config", p, "-allow-ips", "")
	cfg, err = loadServeConfig(sf, fs)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.allowIPs) != 0 {
		t.Errorf("flag -allow-ips \"\" 应覆盖配置为不限制，实际 %v", cfg.allowIPs)
	}
}

// TestLoadServeConfigMissingFieldsDefaults 验收：配置缺失字段用默认值。
func TestLoadServeConfigMissingFieldsDefaults(t *testing.T) {
	// 仅 allow_ips：autocommit 用默认（开启、0 点）。
	p := writeConfigFile(t, `{"allow_ips":["127.0.0.1"]}`)
	sf, fs := newServeFlagSetForTest(t, "-config", p)
	cfg, err := loadServeConfig(sf, fs)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.gitAuto || cfg.hour != 0 {
		t.Errorf("缺失 autocommit 应用默认（enabled=true hour=0），实际 gitAuto=%v hour=%d", cfg.gitAuto, cfg.hour)
	}
	if want := []string{"127.0.0.1"}; !reflect.DeepEqual(cfg.allowIPs, want) {
		t.Errorf("allow_ips = %v, want %v", cfg.allowIPs, want)
	}

	// autocommit 仅 enabled：hour 用默认 0。
	p = writeConfigFile(t, `{"autocommit":{"enabled":false}}`)
	sf, fs = newServeFlagSetForTest(t, "-config", p)
	cfg, err = loadServeConfig(sf, fs)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.gitAuto || cfg.hour != 0 {
		t.Errorf("enabled=false 应生效且 hour 默认 0，实际 gitAuto=%v hour=%d", cfg.gitAuto, cfg.hour)
	}

	// autocommit 仅 hour：enabled 用默认 true。
	p = writeConfigFile(t, `{"autocommit":{"hour":7}}`)
	sf, fs = newServeFlagSetForTest(t, "-config", p)
	cfg, err = loadServeConfig(sf, fs)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.gitAuto || cfg.hour != 7 {
		t.Errorf("hour=7 应生效且 enabled 默认 true，实际 gitAuto=%v hour=%d", cfg.gitAuto, cfg.hour)
	}

	// 空配置 {}：全默认。
	p = writeConfigFile(t, `{}`)
	sf, fs = newServeFlagSetForTest(t, "-config", p)
	cfg, err = loadServeConfig(sf, fs)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.gitAuto || cfg.hour != 0 || len(cfg.allowIPs) != 0 {
		t.Errorf("空配置应用全默认，实际 gitAuto=%v hour=%d allowIPs=%v", cfg.gitAuto, cfg.hour, cfg.allowIPs)
	}
}

// TestLoadServeConfigUTF8BOM 验收：UTF-8 BOM 前缀的配置文件可正常解析
// （Windows 记事本等编辑器产物，json.Unmarshal 本身不识别 BOM）。
func TestLoadServeConfigUTF8BOM(t *testing.T) {
	content := string([]byte{0xEF, 0xBB, 0xBF}) + `{"autocommit":{"hour":3},"allow_ips":["127.0.0.1"]}`
	p := writeConfigFile(t, content)
	sf, fs := newServeFlagSetForTest(t, "-config", p)
	cfg, err := loadServeConfig(sf, fs)
	if err != nil {
		t.Fatalf("带 BOM 的配置应可解析：%v", err)
	}
	if cfg.hour != 3 || !reflect.DeepEqual(cfg.allowIPs, []string{"127.0.0.1"}) {
		t.Errorf("BOM 配置解析错误：hour=%d allowIPs=%v", cfg.hour, cfg.allowIPs)
	}
}

// TestLoadServeConfigErrors 验收：配置读取失败 / JSON 非法 / hour 越界
// 一律启动快速失败。
func TestLoadServeConfigErrors(t *testing.T) {
	// 配置文件不存在。
	sf, fs := newServeFlagSetForTest(t, "-config", filepath.Join(t.TempDir(), "nope.json"))
	if _, err := loadServeConfig(sf, fs); err == nil {
		t.Error("不存在的配置文件应报错")
	}

	// JSON 非法。
	p := writeConfigFile(t, `{not json`)
	sf, fs = newServeFlagSetForTest(t, "-config", p)
	if _, err := loadServeConfig(sf, fs); err == nil {
		t.Error("非法 JSON 应报错")
	}

	// hour 越界（flag 来源）。
	sf, fs = newServeFlagSetForTest(t, "-git-autocommit-hour", "24")
	if _, err := loadServeConfig(sf, fs); err == nil {
		t.Error("flag hour=24 应报错")
	}

	// hour 越界（配置来源）。
	p = writeConfigFile(t, `{"autocommit":{"hour":-1}}`)
	sf, fs = newServeFlagSetForTest(t, "-config", p)
	if _, err := loadServeConfig(sf, fs); err == nil {
		t.Error("配置 hour=-1 应报错")
	}
}
