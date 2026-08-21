package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// 哨兵错误（规范 §3.3）：供 errors.Is 判断，禁止字符串比较。
var (
	// ErrPathOutsideRoot 表示路径经沙箱校验后落在 memory/ 根目录之外。
	ErrPathOutsideRoot = errors.New("memory: path outside root")
	// ErrExists 表示目标文件已存在且 overwrite=false。
	ErrExists = errors.New("memory: file already exists")
)

// Store 是记忆库根目录的文件读写核心。
//
// 并发模型（设计文档 §6.2）：写操作（Write/Append）经互斥锁串行化，
// 写入固定顺序为「临时文件 → rename」；读操作（Read/List）不加锁、不阻塞写方。
type Store struct {
	root         string // 绝对路径、filepath.Clean 后的记忆库根目录
	resolvedRoot string // EvalSymlinks 解析后的根目录，用于符号链接逃逸判定
	mu           sync.Mutex
}

// OpenStore 打开 root 下的记忆库并返回 Store。
// root 必须已存在且为目录；返回的 Store 对根内所有操作做路径沙箱校验。
func OpenStore(root string) (*Store, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("memory: resolve root: %w", err)
	}
	abs = filepath.Clean(abs)
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("memory: open root %q: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("memory: root %q is not a directory", abs)
	}
	resolved, err := resolvePath(abs)
	if err != nil {
		return nil, fmt.Errorf("memory: resolve root %q: %w", abs, err)
	}
	return &Store{root: abs, resolvedRoot: resolved}, nil
}

// resolve 把相对路径 rel 解析为根内的绝对路径，做三层校验（规范 §5.1）：
//
//  1. 拒绝绝对路径与带卷名的路径（如 C:foo / C:\x）；
//  2. filepath.Clean 后判定落在根目录内（拒绝 .. 穿越）；
//  3. 组件级解析符号链接（含 Windows 目录联接 junction），判定解析后仍在根内。
//
// 返回的路径可直接用于文件操作；穿越返回 ErrPathOutsideRoot，
// 其余失败（根目录不可达、链接环等）返回包装后的底层错误。
func (s *Store) resolve(rel string) (string, error) {
	if rel == "" {
		rel = "."
	}
	if filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" {
		return "", ErrPathOutsideRoot
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", ErrPathOutsideRoot
	}
	joined := filepath.Join(s.root, clean)
	if !withinRoot(s.root, joined) {
		return "", ErrPathOutsideRoot
	}
	resolved, err := resolvePath(joined)
	if err != nil {
		return "", err
	}
	if !withinRoot(s.resolvedRoot, resolved) {
		return "", ErrPathOutsideRoot
	}
	return joined, nil
}

// withinRoot 报告 path 是否位于 root 之内（filepath.Rel 文本级判定；
// Windows 上为大小写不敏感比较）。
func withinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// maxSymlinkDepth 是符号链接/联接解析的最大递归深度，用于防御链接环。
const maxSymlinkDepth = 255

// resolvePath 逐组件解析 path 中的符号链接与 Windows 目录联接（junction），
// 返回解析后的规范绝对路径；不存在的尾部组件原样保留（待创建文件场景）。
//
// 为什么不用 filepath.EvalSymlinks：Windows 上 EvalSymlinks 不解析 junction
// （本机实测：对 junction 返回原路径、对 junction 内文件报错），而 junction
// 与符号链接一样可逃出根目录。os.Readlink 对两者都返回目标路径，故改用
// 组件级 Lstat + Readlink（Linux 上对符号链接行为与 EvalSymlinks 一致）。
func resolvePath(path string) (string, error) {
	return resolvePathDepth(path, 0)
}

func resolvePathDepth(p string, depth int) (string, error) {
	if depth > maxSymlinkDepth {
		return "", fmt.Errorf("memory: symlink loop detected at %q", p)
	}
	p = filepath.Clean(p)
	parts := strings.Split(p, string(filepath.Separator))
	var cur string
	for i, part := range parts {
		if part == "" {
			if i == 0 && filepath.IsAbs(p) {
				cur = string(filepath.Separator) // Unix 根 / 或 Windows 无盘绝对路径 \
			}
			continue
		}
		if i == 0 && strings.HasSuffix(part, ":") {
			cur = part + string(filepath.Separator) // Windows 卷名组件（如 D:）
			continue
		}
		cur = filepath.Join(cur, part)
		if _, err := os.Lstat(cur); err != nil {
			if os.IsNotExist(err) {
				for _, rest := range parts[i+1:] {
					if rest == "" {
						continue
					}
					cur = filepath.Join(cur, rest)
				}
				return cur, nil
			}
			return "", fmt.Errorf("memory: lstat %q: %w", cur, err)
		}
		// 符号链接/junction：Readlink 成功即视为链接（Windows junction 的
		// Lstat 不置 ModeSymlink，只能靠 Readlink 探测）；失败视为普通组件。
		target, err := os.Readlink(cur)
		if err == nil {
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(cur), target)
			}
			resolved, err := resolvePathDepth(target, depth+1)
			if err != nil {
				return "", err
			}
			cur = resolved
		}
	}
	return cur, nil
}

// Root 返回记忆库根目录的绝对路径（filepath.Clean 后）。
// 供阶段 2 索引全量重建（pkg/index.Rebuild 遍历目录树）等外部遍历使用。
func (s *Store) Root() string { return s.root }

// Read 读取根内相对路径 rel 的文件内容。读操作不加锁（设计文档 §6.2）。
func (s *Store) Read(rel string) ([]byte, error) {
	p, err := s.resolve(rel)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("memory: read %q: %w", rel, err)
	}
	return data, nil
}
