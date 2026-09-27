#!/usr/bin/env bash
# gen_source_doc.sh — 生成软著申请用《源程序.txt》
#
# 规则（依据中国版权保护中心材料要求）：
#   1. 收集全部 .go / .tf / .sh / Dockerfile 源文件（排除 *_test.go、vendor/、.git/）；
#   2. 按稳定顺序（路径排序）拼接，去除空行；
#   3. 每 50 行一页，页首插入页眉“软件名 V1.0 第X页/共Y页”；
#   4. 总页数不足 60 页时全量提交；超过 60 页时输出前后各连续 30 页。
#
# 输出：
#   copyright-docs/源程序.txt
#   copyright-docs/源程序行数统计.txt
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="$ROOT/copyright-docs"
OUT="$OUT_DIR/源程序.txt"
STAT="$OUT_DIR/源程序行数统计.txt"
TITLE="OpenCloudLab云主机快速创建平台软件"
VERSION="V1.0"
LINES_PER_PAGE=50
MAX_PAGES=60

mkdir -p "$OUT_DIR"

TMP_BODY="$(mktemp)"
TMP_ALL="$(mktemp)"
trap 'rm -f "$TMP_BODY" "$TMP_ALL"' EXIT

# 收集源文件（稳定顺序），拼接并去空行
{
  find "$ROOT" -type f \( -name '*.go' -o -name '*.tf' -o -name '*.sh' -o -name 'Dockerfile*' \) \
    ! -path '*/vendor/*' ! -path '*/.git/*' ! -name '*_test.go' \
    ! -path "$OUT_DIR/*" -print0 | sort -z | while IFS= read -r -d '' f; do
      rel="${f#"$ROOT"/}"
      printf '// ===== 文件: %s =====\n' "$rel"
      cat "$f"
      printf '\n'
    done
} | sed '/^[[:space:]]*$/d' > "$TMP_ALL"

TOTAL_LINES=$(wc -l < "$TMP_ALL" | tr -d ' ')
TOTAL_PAGES=$(( (TOTAL_LINES + LINES_PER_PAGE - 1) / LINES_PER_PAGE ))

# 截取前 30 页 + 后 30 页（不足 60 页则全量）
if [ "$TOTAL_PAGES" -gt "$MAX_PAGES" ]; then
  KEEP=$(( MAX_PAGES * LINES_PER_PAGE / 2 ))   # 30 页 = 1500 行
  HEAD_END=$(( KEEP ))
  TAIL_START=$(( TOTAL_LINES - KEEP + 1 ))
  {
    sed -n "1,${HEAD_END}p" "$TMP_ALL"
    echo "……（中间部分略，共 ${TOTAL_PAGES} 页，仅提交前后各 30 页）……"
    sed -n "${TAIL_START},${TOTAL_LINES}p" "$TMP_ALL"
  } > "$TMP_BODY"
  PAGES_IN_DOC=$MAX_PAGES
else
  cp "$TMP_ALL" "$TMP_BODY"
  PAGES_IN_DOC=$TOTAL_PAGES
fi

# 分页输出，插入页眉（页数按正文实际行数计算）
BODY_LINES=$(wc -l < "$TMP_BODY" | tr -d ' ')
PAGES_IN_DOC=$(( (BODY_LINES + LINES_PER_PAGE - 1) / LINES_PER_PAGE ))
{
  n=0
  page=0
  while IFS= read -r line; do
    if [ $(( n % LINES_PER_PAGE )) -eq 0 ]; then
      page=$(( page + 1 ))
      printf '\n%s  %s  第%d页/共%d页\n' "$TITLE" "$VERSION" "$page" "$PAGES_IN_DOC"
    fi
    n=$(( n + 1 ))
    printf '%s\n' "$line"
  done < "$TMP_BODY"
} > "$OUT"

# 行数统计
{
  echo "源程序行数统计（${TITLE} ${VERSION}）"
  echo "统计命令: find . -name '*.go' | xargs wc -l   （排除 *_test.go 与 vendor）"
  echo
  echo "== Go 源程序（不含 *_test.go、vendor）=="
  ( cd "$ROOT" && find . -name '*.go' ! -path './vendor/*' ! -name '*_test.go' -print0 \
      | sort -z | xargs -0 wc -l )
  echo
  echo "== Go 测试代码（*_test.go，不计入申请行数）=="
  ( cd "$ROOT" && find . -name '*_test.go' ! -path './vendor/*' -print0 \
      | sort -z | xargs -0 wc -l )
  echo
  echo "== OpenTofu 模板（*.tf）=="
  ( cd "$ROOT" && find . -name '*.tf' -print0 | sort -z | xargs -0 wc -l )
  echo
  echo "== 脚本与构建文件（*.sh / Dockerfile）=="
  ( cd "$ROOT" && find . -type f \( -name '*.sh' -o -name 'Dockerfile*' \) \
      ! -path './.git/*' ! -path "$OUT_DIR" -print0 | sort -z | xargs -0 wc -l )
  echo
  echo "去空行后总行数: $TOTAL_LINES"
  echo "分页（每页 ${LINES_PER_PAGE} 行）总页数: $TOTAL_PAGES"
  echo "生成文件: ${OUT}（文档页数 ${PAGES_IN_DOC}）"
} > "$STAT"

echo "done: $OUT"
echo "done: $STAT"
