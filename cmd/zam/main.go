// 命令 zam 是记忆库文件读写 CLI（阶段 1），子命令：write / read / append / list / status。
//
// 用法示例：
//
//	zam write notes/test.md "content"
//	zam read notes/test.md
//	zam append agent/dev/2026-08.md "line"
//	zam list notes
//	zam status
//
// 所有路径都经 pkg/memory 路径沙箱校验：../、绝对路径、符号链接逃逸
// 一律返回错误退出（退出码 1），不会写穿记忆库根目录。
// 仅用标准库 flag，不引第三方 CLI 框架（任务书约束）。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"zipper-agent-memory/pkg/memory"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, rest := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "write":
		err = cmdWrite(rest)
	case "read":
		err = cmdRead(rest)
	case "append":
		err = cmdAppend(rest)
	case "list":
		err = cmdList(rest)
	case "status":
		err = cmdStatus(rest)
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "zam: unknown command %q\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "zam: %v\n", err)
		os.Exit(1)
	}
}

// newFlagSet 创建子命令 flag 集并注册通用 -root 参数。
func newFlagSet(name string) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	root := fs.String("root", "./memory", "记忆库根目录")
	return fs, root
}

func cmdWrite(args []string) error {
	fs, root := newFlagSet("zam write")
	overwrite := fs.Bool("overwrite", false, "目标已存在时覆盖（默认拒绝）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: zam write [-root dir] [-overwrite] <path> <content>")
	}
	st, err := memory.OpenStore(*root)
	if err != nil {
		return err
	}
	return st.Write(fs.Arg(0), []byte(fs.Arg(1)), *overwrite)
}

func cmdRead(args []string) error {
	fs, root := newFlagSet("zam read")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: zam read [-root dir] <path>")
	}
	st, err := memory.OpenStore(*root)
	if err != nil {
		return err
	}
	data, err := st.Read(fs.Arg(0))
	if err != nil {
		return err
	}
	if _, err := os.Stdout.Write(data); err != nil {
		return err
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		fmt.Println()
	}
	return nil
}

func cmdAppend(args []string) error {
	fs, root := newFlagSet("zam append")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: zam append [-root dir] <path> <content>")
	}
	st, err := memory.OpenStore(*root)
	if err != nil {
		return err
	}
	return st.Append(fs.Arg(0), fs.Arg(1))
}

func cmdList(args []string) error {
	fs, root := newFlagSet("zam list")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("usage: zam list [-root dir] [dir]")
	}
	dir := "."
	if fs.NArg() == 1 {
		dir = fs.Arg(0)
	}
	st, err := memory.OpenStore(*root)
	if err != nil {
		return err
	}
	entries, err := st.List(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir {
			fmt.Println(e.Name + "/")
		} else {
			fmt.Println(e.Name)
		}
	}
	return nil
}

func cmdStatus(args []string) error {
	fs, root := newFlagSet("zam status")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: zam status [-root dir]")
	}
	st, err := memory.OpenStore(*root)
	if err != nil {
		return err
	}
	info, err := st.Status()
	if err != nil {
		return err
	}
	fmt.Printf("root: %s\n", filepath.Clean(*root))
	fmt.Printf("files: %d\n", info.FileCount)
	fmt.Printf("size: %d bytes\n", info.TotalBytes)
	if !info.LastModified.IsZero() {
		fmt.Printf("last modified: %s\n", info.LastModified.Format("2006-01-02 15:04:05"))
	}
	return nil
}

func usage() {
	fmt.Fprintf(os.Stderr, `zam - ZipperAgentMemory 记忆库文件读写 CLI（阶段 1）

用法：
  zam <command> [flags] [args]

命令：
  write  写入/新建文件（默认不覆盖已存在文件，-overwrite 可覆盖）
  read   读取文件内容
  append 追加内容（自动加时间戳分隔行）
  list   列出目录下的文件/子目录
  status 记忆库统计（文件数 / 总大小 / 最近变更）

通用 flag：
  -root <dir>  记忆库根目录（默认 ./memory）

示例：
  zam write notes/test.md "content"
  zam read notes/test.md
  zam append agent/dev/2026-08.md "line"
  zam list notes
  zam status
`)
}
