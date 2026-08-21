#!/usr/bin/env bash
#
# migrate.sh — ZipperAgentMemory 记忆库迁移工具（阶段 4）
#
# 双模式迁移（design.md §7）：
#   1. 目录复制：pack 打包 memory/ 内容（tar.gz，缺 tar 时回退 zip）→
#      目标机 restore 解压 → rebuild-index 即可使用（纯文件，不含 git 历史）；
#   2. git 同步：bundle 产出单文件 memory.bundle（git bundle create --all，
#      含全部历史）→ 目标机 restore clone 出完整 git 仓库。
#
# 用法：
#   ./migrate.sh pack   <memory_dir> [输出文件]          # 默认输出 <目录名>.tar.gz
#   ./migrate.sh bundle <memory_dir> [输出文件]          # 默认输出 memory.bundle
#   ./migrate.sh restore <来源> <目标目录> [--rebuild-index]
#   ./migrate.sh help
#
# 说明：
#   - 全部命令以参数数组形式调用 git/tar/zip（无 shell 拼接，编码规范 §5.4）；
#   - 环境变量 DRY_RUN=1 只打印将执行的命令，不实际执行（每步默认 echo 提示）；
#   - pack 默认排除 .git（git 历史请走 bundle 模式）；restore 目标目录必须
#     不存在或为空；
#   - restore 后需重建 FTS 索引（索引是 derived state，design.md §9 R7）：
#       zipper-agent-memoryd rebuild-index -root <目标目录>
#     传 --rebuild-index 时自动执行（二进制经 PATH 或 ZAM_BIN 环境变量定位）；
#   - bundle 要求 memory/ 已是 git 仓库且至少有一个提交（git-init 子命令可初始化，
#     autocommit 或手动 git commit 产生首个提交）。
#
set -euo pipefail

DRY_RUN="${DRY_RUN:-0}"

info() { printf '==> %s\n' "$*"; }
log()  { printf '%s\n' "$*"; }
warn() { printf '!!  %s\n' "$*" >&2; }
die()  { warn "$*"; exit 1; }

# run 执行命令；DRY_RUN=1 时仅打印（参数数组：直接展开 "$@"，无 shell 拼接）。
run() {
    if [ "$DRY_RUN" = "1" ]; then
        printf '    (dry-run)'
        printf ' %q' "$@"
        printf '\n'
        return 0
    fi
    "$@"
}

# abspath 规范化目录为绝对路径（目录必须存在）。
abspath() {
    local d="$1"
    [ -d "$d" ] || die "目录不存在: $d"
    (cd "$d" && pwd)
}

cmd_pack() {
    local mem out base parent
    mem="$(abspath "$1")"
    out="${2:-}"
    base="$(basename "$mem")"
    [ -z "$out" ] && out="${base}.tar.gz"
    parent="$(dirname "$mem")"
    info "pack: 打包 $mem 内容 → $out（排除 .git，git 历史请用 bundle 模式）"
    if command -v tar >/dev/null 2>&1; then
        # 归档 memory/ 的「内容」（不包含目录本身），restore 可直接解压进目标目录。
        run tar -C "$mem" --exclude='./.git' -czf "$out" .
    elif command -v zip >/dev/null 2>&1; then
        # zip 无 -C 等价物，须在子 shell 内 cd 到 $mem；out 先转为绝对路径。
        local out_abs
        out_abs="$(cd "$(dirname "$out")" && pwd)/$(basename "$out")"
        (cd "$mem" && run zip -r -q "$out_abs" . -x './.git/*' './.git')
    else
        die "pack: 未找到 tar 或 zip"
    fi
    log_done="$(ls -lh "$out" 2>/dev/null | awk '{print $5}')" || log_done="?"
    log "    pack 完成：$out（大小 ${log_done:-?}）"
    log "    迁移到目标机：./migrate.sh restore $out <目标目录> && zipper-agent-memoryd rebuild-index -root <目标目录>"
}

cmd_bundle() {
    local mem out out_abs
    mem="$(abspath "$1")"
    out="${2:-memory.bundle}"
    git -C "$mem" rev-parse --git-dir >/dev/null 2>&1 || \
        die "bundle: $mem 不是 git 仓库（先执行 zipper-agent-memoryd git-init -root $mem）"
    git -C "$mem" rev-parse --verify HEAD >/dev/null 2>&1 || \
        die "bundle: $mem 尚无任何提交（先产生一次变更触发 autocommit，或手动 git commit）"
    # git -C 会改变命令的相对路径解析基准：bundle 输出路径须先转绝对，
    # 否则会落在仓库内而非调用方当前目录。
    out_abs="$(cd "$(dirname "$out")" && pwd)/$(basename "$out")"
    info "bundle: git bundle create --all → $out（单文件，含全部历史）"
    run git -C "$mem" bundle create "$out_abs" --all
    log "    bundle 完成：$out"
    log "    迁移到目标机：./migrate.sh restore $out <目标目录>（clone 出完整仓库）"
}

cmd_restore() {
    local src="$1" dest="$2" rebuild="${3:-}"
    [ -e "$src" ] || die "restore: 来源不存在: $src"
    if [ -e "$dest" ] && [ -n "$(ls -A "$dest" 2>/dev/null)" ]; then
        die "restore: 目标目录必须不存在或为空: $dest"
    fi
    case "$src" in
        *.bundle)
            info "restore: git clone bundle → $dest（完整 git 仓库）"
            run git clone "$src" "$dest"
            ;;
        *.tar.gz|*.tgz)
            info "restore: 解压 tar.gz → $dest"
            run mkdir -p "$dest"
            run tar -C "$dest" -xzf "$src"
            ;;
        *.zip)
            info "restore: 解压 zip → $dest"
            run mkdir -p "$dest"
            run unzip -q "$src" -d "$dest"
            ;;
        *)
            die "restore: 无法识别的来源类型（支持 .tar.gz/.tgz/.zip/.bundle）: $src"
            ;;
    esac
    rebuild_index "$dest" "$rebuild"
}

# rebuild_index 重建目标目录的 FTS 索引（derived state，迁移后必须重建）。
rebuild_index() {
    local dest="$1" rebuild="${2:-}"
    local bin="${ZAM_BIN:-zipper-agent-memoryd}"
    if [ "$rebuild" = "--rebuild-index" ]; then
        command -v "$bin" >/dev/null 2>&1 || \
            die "restore: 未找到 $bin（可设置 ZAM_BIN 指向二进制，或手动执行 rebuild-index）"
        info "restore: $bin rebuild-index -root $dest"
        run "$bin" rebuild-index -root "$dest"
    else
        log "    下一步：zipper-agent-memoryd rebuild-index -root $dest（索引是 derived state，必须重建后方可搜索）"
    fi
    log "    验证：zipper-agent-memoryd search -root $dest '关键词'"
}

usage() {
    # 打印头部注释块（跳过首行 shebang，遇到首个非 # 行即停）。
    awk 'NR==1 {next} /^#/ {sub(/^# ?/, ""); print} !/^#/ {exit}' "$0"
    exit "${1:-0}"
}

main() {
    local cmd="${1:-}"; shift 2>/dev/null || true
    case "$cmd" in
        pack)
            [ $# -ge 1 ] && [ $# -le 2 ] || { usage 2; }
            cmd_pack "$1" "${2:-}"
            ;;
        bundle)
            [ $# -ge 1 ] && [ $# -le 2 ] || { usage 2; }
            cmd_bundle "$1" "${2:-}"
            ;;
        restore)
            [ $# -ge 2 ] && [ $# -le 3 ] || { usage 2; }
            [ $# -eq 2 ] || { [ "$3" = "--rebuild-index" ] || die "restore: 未知参数 $3（仅支持 --rebuild-index）"; }
            cmd_restore "$1" "$2" "${3:-}"
            ;;
        help|-h|--help)
            usage 0
            ;;
        *)
            usage 2
            ;;
    esac
}

main "$@"
