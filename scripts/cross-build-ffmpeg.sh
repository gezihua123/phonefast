#!/usr/bin/env bash
#
# cross-build-ffmpeg.sh — 使用 zig cc 统一交叉编译 FFmpeg 静态库
#
# 用法:
#   bash scripts/cross-build-ffmpeg.sh x86_64-linux-gnu
#   bash scripts/cross-build-ffmpeg.sh aarch64-linux-gnu
#   bash scripts/cross-build-ffmpeg.sh x86_64-windows-gnu
#   bash scripts/cross-build-ffmpeg.sh x86_64-darwin
#   bash scripts/cross-build-ffmpeg.sh aarch64-darwin
#
# 产物:
#   ./build/cross-ffmpeg/<target>/lib/libavcodec.a 等 7 个 .a 文件
#   ./build/cross-ffmpeg/<target>/include/
#   ./build/cross-ffmpeg/<target>/lib/pkgconfig/

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
OUTPUT_DIR="$ROOT_DIR/build/cross-ffmpeg"

# ── 目标 → zig triple + 平台参数 ──
# 每个目标定义: zig_triple, target_os, extra_flags, config_patches

declare_target() {
    case "$1" in
        x86_64-linux-gnu)
            ZIG_TRIPLE="x86_64-linux-gnu"
            TARGET_OS="linux"
            ARCH="x86_64"
            CONFIG_PATCH="sysctl"
            ;;
        aarch64-linux-gnu)
            ZIG_TRIPLE="aarch64-linux-gnu"
            TARGET_OS="linux"
            ARCH="aarch64"
            CONFIG_PATCH="sysctl"
            ;;
        x86_64-windows-gnu)
            ZIG_TRIPLE="x86_64-windows-gnu"
            TARGET_OS="mingw32"
            ARCH="x86_64"
            CONFIG_PATCH="mingw_math"
            ;;
        x86_64-darwin)
            ZIG_TRIPLE="x86_64-macos-none"
            TARGET_OS="darwin"
            ARCH="x86_64"
            CONFIG_PATCH=""
            ;;
        aarch64-darwin)
            ZIG_TRIPLE="aarch64-macos-none"
            TARGET_OS="darwin"
            ARCH="aarch64"
            CONFIG_PATCH=""
            ;;
        *)
            echo "不支持的目标: $1"
            exit 1
            ;;
    esac
}

FFMPEG_VERSION="7.1.5"
FFMPEG_URL="https://github.com/FFmpeg/FFmpeg/archive/refs/tags/n${FFMPEG_VERSION}.tar.gz"

main() {
    if [ $# -lt 1 ]; then
        echo "用法: $0 <target-triple>"
        echo "  x86_64-linux-gnu   aarch64-linux-gnu"
        echo "  x86_64-windows-gnu"
        echo "  x86_64-darwin      aarch64-darwin"
        exit 1
    fi

    local target="$1"
    declare_target "$target"

    local install_dir="$OUTPUT_DIR/$target"
    local src_dir="$OUTPUT_DIR/src"

    echo "[+] 目标:    $target"
    echo "[+] zig:     $ZIG_TRIPLE"
    echo "[+] 安装到:  $install_dir"
    echo ""

    # ── 下载 FFmpeg 源码 ──
    mkdir -p "$src_dir"
    local archive_name="FFmpeg-n${FFMPEG_VERSION}.tar.gz"

    if [ ! -f "$src_dir/$archive_name" ]; then
        echo "[+] 下载 FFmpeg ${FFMPEG_VERSION} ..."
        curl -L "$FFMPEG_URL" -o "$src_dir/$archive_name"
    fi

    local src_subdir="FFmpeg-n${FFMPEG_VERSION}"
    if [ ! -d "$src_dir/$src_subdir" ]; then
        echo "[+] 解压 ..."
        tar xf "$src_dir/$archive_name" -C "$src_dir"
    fi

    local build_dir="$src_dir/build-${target}"
    rm -rf "$build_dir"
    mkdir -p "$build_dir"
    cd "$build_dir"

    # ── 工具链 + flags 选择 ──
    # 三个维度独立决策: toolchain / asm / pic / threading
    local CC CXX AR RANLIB NM CROSS_FLAG ASM_FLAG PIC_FLAG THREAD_FLAG EXTRA_CFLAGS
    EXTRA_CFLAGS=""

    if [ "$TARGET_OS" = "darwin" ]; then
        # ── macOS: clang (zig 缺 SDK) ──
        if [ "$ARCH" = "x86_64" ]; then
            CC="clang -arch x86_64"; CXX="clang++ -arch x86_64"
        else
            CC="clang"; CXX="clang++"
        fi
        # 关键: 强制 Apple 原生 ar/ranlib/nm。
        # 若 PATH 上有 GNU binutils (brew install binutils), 其 ar 产出 GNU 格式 .a
        # (符号表成员名为 '/'), Apple ld 不认 → "archive member '/' not a mach-o file"。
        # Apple 原生工具产出 BSD 格式 .a (#1 长名), Apple ld 直接接受, 无需 libtool 重封装。
        AR="/usr/bin/ar"; RANLIB="/usr/bin/ranlib"; NM="/usr/bin/nm"; CROSS_FLAG=""
        PIC_FLAG="--enable-pic"
        THREAD_FLAG=""
    elif [ "$(uname -s)" = "Linux" ]; then
        # ── Linux host (CI ubuntu runner): GCC 系 ──
        # 用于 GitHub Actions 的 ubuntu runner:
        #   x86_64-linux-gnu   → 原生 gcc (amd64 runner)
        #   aarch64-linux-gnu  → aarch64-linux-gnu-gcc 交叉 (amd64 runner) / 原生 (arm64 runner)
        #   x86_64-windows-gnu → mingw-w64 交叉
        case "$target" in
            x86_64-linux-gnu)
                CC="gcc"; CXX="g++"; AR="ar"; RANLIB="ranlib"; NM="nm"
                CROSS_FLAG=""
                ;;
            aarch64-linux-gnu)
                CC="aarch64-linux-gnu-gcc"; CXX="aarch64-linux-gnu-g++"
                AR="aarch64-linux-gnu-ar"; RANLIB="aarch64-linux-gnu-ranlib"
                NM="aarch64-linux-gnu-nm"
                CROSS_FLAG="--enable-cross-compile --cross-prefix=aarch64-linux-gnu-"
                ;;
            x86_64-windows-gnu)
                CC="x86_64-w64-mingw32-gcc"; CXX="x86_64-w64-mingw32-g++"
                AR="x86_64-w64-mingw32-ar"; RANLIB="x86_64-w64-mingw32-ranlib"
                NM="x86_64-w64-mingw32-nm"
                CROSS_FLAG="--enable-cross-compile --cross-prefix=x86_64-w64-mingw32-"
                ;;
        esac
        # Linux 静态库链进 Go 二进制不需要 PIC (Go 链接器自行重定位)。
        # 且 GCC + --enable-pic + x86 inline asm 会触发 "impossible constraint in 'asm'
        # (mathops.h NEG_USR32)。故 Linux 不开 PIC。
        PIC_FLAG=""
        THREAD_FLAG=""           # mingw 用默认 w32threads，linux 用 pthreads
    else
        # ── macOS host → zig 交叉编译 Linux/Windows ──
        command -v zig >/dev/null 2>&1 || { echo "需要安装 zig: brew install zig"; exit 1; }
        CC="zig cc -target $ZIG_TRIPLE"; CXX="zig c++ -target $ZIG_TRIPLE"
        AR="zig ar"; RANLIB="zig ranlib"; NM="true"
        CROSS_FLAG="--enable-cross-compile"
        # zig + --enable-pic + linux 已验证可编译可链接 (asm-on, 21M 二进制, 解码 PASS)。
        # 保留 PIC: zig 下不触发 GCC 那种 asm 约束冲突。
        PIC_FLAG=$([ "$TARGET_OS" = "linux" ] && echo "--enable-pic" || echo "")
        THREAD_FLAG=""
        [ "$TARGET_OS" = "linux" ] && EXTRA_CFLAGS="-fPIC"
        [ "$TARGET_OS" = "mingw32" ] && THREAD_FLAG="--disable-w32threads --disable-pthreads"
    fi

    # ── asm 决策 (统一，跨 toolchain) ──
    # x86_64:   需 nasm 汇编 SSE/AVX/AVX2/AVX-512。有则开，无则降级 --disable-asm。
    # aarch64:  NEON 走 assembler (zig 内置 / clang gas)，无需 nasm，始终开。
    # 装好 nasm 即可全平台 asm-on: bash scripts/build_env.sh install
    if [ "$ARCH" = "aarch64" ]; then
        ASM_FLAG=""   # NEON，无需 nasm
    elif [ "$ARCH" = "x86_64" ]; then
        if command -v nasm >/dev/null 2>&1; then
            ASM_FLAG=""   # nasm 在 → SSE/AVX/AVX2 全开
        else
            ASM_FLAG="--disable-asm"
            echo "[!] nasm 未安装 → 降级 --disable-asm (纯 C 解码慢 2-4×)"
            echo "    安装: bash scripts/build_env.sh install  (或 brew install nasm)"
        fi
    else
        ASM_FLAG="--disable-asm"
    fi

    # ── 配置 FFmpeg ──
    echo "[+] 配置 FFmpeg (asm=${ASM_FLAG:-on}, pic=${PIC_FLAG:-off}) ..."
    CC="$CC" CXX="$CXX" AR="$AR" RANLIB="$RANLIB" NM="$NM" \
    "$src_dir/$src_subdir/configure" \
        --prefix="$install_dir" \
        --cc="$CC" \
        --cxx="$CXX" \
        --ar="$AR" \
        --ranlib="$RANLIB" \
        --nm="$NM" \
        $CROSS_FLAG \
        --target-os="$TARGET_OS" \
        --arch="$ARCH" \
        --disable-programs \
        --disable-doc \
        --disable-debug \
        --disable-everything \
        --enable-decoder=h264 \
        --enable-parser=h264 \
        --enable-demuxer=h264 \
        --enable-protocol=file \
        $ASM_FLAG \
        $PIC_FLAG \
        $THREAD_FLAG \
        ${EXTRA_CFLAGS:+--extra-cflags="$EXTRA_CFLAGS"} \
        --pkg-config=/usr/bin/false

    # ── config.h 补丁 (跨平台兼容) ──
    case "$CONFIG_PATCH" in
        sysctl)
            # zig glibc 提供 sysctl 符号但不提供 <sys/sysctl.h> (glibc ≥2.32 移除)
            echo "[+] Patching config.h: disable HAVE_SYSCTL / HAVE_SYSCTLBYNAME"
            sed -i.bak 's/#define HAVE_SYSCTL 1/#define HAVE_SYSCTL 0/' config.h
            sed -i.bak 's/#define HAVE_SYSCTLBYNAME 1/#define HAVE_SYSCTLBYNAME 0/' config.h
            rm -f config.h.bak
            ;;
        getenv)
            # mingw64 stdlib.h 声明了 getenv()，与 FFmpeg 的 #define getenv(x) NULL 冲突
            echo "[+] Patching config.h: remove #define getenv(x) NULL"
            sed -i.bak 's/#define getenv(x) NULL/\/\/ #define getenv(x) NULL (removed for mingw64 compat)/' config.h
            rm -f config.h.bak
            ;;
        mingw_math)
            # zig mingw 的 configure 函数探测对 C99 math 全部失败 (HAVE_*=0)，
            # 导致 FFmpeg libm.h 用 static inline 重定义 trunc/round/copysign/...
            # 但 mingw math.h 已把这些声明为 extern → "static declaration follows
            # non-static declaration" 编译错误。mingw 实际提供全套 C99 math，
            # 故把这些 HAVE_* 翻成 1，让 FFmpeg 直接用系统版本。
            echo "[+] Patching config.h: enable mingw C99 math HAVE_* (trunc/round/cbrt/...)"
            for f in TRUNC TRUNCF ROUND ROUNDF COPYSIGN COPYSIGNF \
                     CBRT CBRTF EXP2 EXP2F HYPOT LDEXPF LOG2 LOG2F \
                     RINT LRINT LRINTF POWF ERF ERFF \
                     ISNAN ISFINITE ISINF; do
                sed -i.bak "s/#define HAVE_$f 0/#define HAVE_$f 1/" config.h
            done
            # getenv: mingw stdlib.h 声明 getenv() 为函数，FFmpeg 的
            # #define getenv(x) NULL 会破坏它，注释掉。
            sed -i.bak 's/#define getenv(x) NULL/\/\/ #define getenv(x) NULL (removed for mingw compat)/' config.h
            rm -f config.h.bak
            ;;
    esac

    # ── 编译 + 安装 ──
    echo "[+] 编译 FFmpeg ..."
    make -j"$(nproc 2>/dev/null || sysctl -n hw.logicalcpu 2>/dev/null || echo 4)" 2>&1 | tail -5

    echo "[+] 安装到 $install_dir ..."
    make install

    # 注: 早期版本曾用 libtool -static 重封装 darwin .a 以修复 SYMDEF，但该步骤用
    # `ar -x` + `*.o` 重新打包，会丢失与根目录 .o 同名的 aarch64 成员
    # (aarch64/swscale.o 与 swscale.o 解压后互相覆盖)，导致 NEON init 符号丢失、
    # Go 链接报 "symbol(s) not found for architecture arm64"。
    # 当前 darwin 分支用 Apple 原生 ar/ranlib，make install 已生成有效 SYMDEF，
    # 无需重封装。故移除该步骤。

    echo ""
    echo "[+] 编译完成:"
    ls -lh "$install_dir/lib/libavcodec"* 2>/dev/null || true
    ls -lh "$install_dir/lib/libswscale"*  2>/dev/null || true
    echo ""
    echo "使用方式:"
    echo "  export PKG_CONFIG_PATH=\"$install_dir/lib/pkgconfig\""
    echo "  export CC=\"zig cc -target $ZIG_TRIPLE\""
    echo "  CGO_ENABLED=1 go build ./cmd/phonefast/"
}

main "$@"
