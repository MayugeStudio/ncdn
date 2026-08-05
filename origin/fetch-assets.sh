#!/bin/bash
set -euo pipefail

cd "$(dirname "$0")"

readonly API="https://soco-st.com/wp-json/wp/v2/posts"
readonly CAT_API="https://soco-st.com/wp-json/wp/v2/categories"
readonly UPLOAD="https://soco-st.com/wp-content/themes/socost/upload"
readonly UA="ncdn-dev-asset-fetcher/1.0 (self-hosted CDN lab; https://github.com/yzp0n/ncdn)"
readonly SLEEP_SEC=1

FORCE=0
[ "${1:-}" = "--force" ] && FORCE=1

readonly SITES=(
    "a:4,99:35"
    "b:9,109,197:80"
    "c:8,14,97:35"
)

log() { echo "[fetch-assets] $*" >&2; }

CATEGORY_JSON="$(mktemp)"
trap 'rm -f "$CATEGORY_JSON"' EXIT
curl -sS -A "$UA" "${CAT_API}?per_page=100&_fields=id,slug,name" -o "$CATEGORY_JSON"

list_posts() {
    local categories="$1" want="$2"
    local per_page=$(( want * 2 ))
    [ "$per_page" -gt 100 ] && per_page=100

    local url="${API}?categories=${categories}&per_page=${per_page}&orderby=date&order=desc&_fields=id,title,categories"
    curl -sS -A "$UA" "$url" | python3 -c '
import json, sys

wanted = [int(c) for c in sys.argv[1].split(",")]
catmap = {c["id"]: c for c in json.load(open(sys.argv[2], encoding="utf-8"))}

seen = set()
for p in json.load(sys.stdin):
    pid = p["id"]
    if pid in seen:
        continue
    seen.add(pid)

    cid = next((c for c in wanted if c in p.get("categories", [])), None)
    cat = catmap.get(cid, {"slug": "other", "name": "その他"})

    title = p["title"]["rendered"].replace("\t", " ").strip()
    print("%s\t%s\t%s\t%s" % (pid, title, cat["slug"], cat["name"]))
' "$categories" "$CATEGORY_JSON" | head -n "$want"
}

is_png() {
    [ -s "$1" ] && [ "$(head -c 8 "$1" | od -An -tx1 | tr -d ' \n')" = "89504e470d0a1a0a" ]
}

total_dl=0
total_skip=0

for spec in "${SITES[@]}"; do
    IFS=':' read -r site categories want <<< "$spec"

    imgdir="sites/${site}/static/img"
    mkdir -p "$imgdir"

    log "site ${site}: カテゴリ ${categories} から ${want} 件を取得します"
    posts="$(list_posts "$categories" "$want")"

    got="$(echo "$posts" | grep -c . || true)"
    if [ "$got" -lt "$want" ]; then
        log "  警告: ${want} 件要求しましたが ${got} 件しか取れませんでした"
    fi

    manifest="sites/${site}/images.json"
    : > "${manifest}.tmp"

    while IFS=$'\t' read -r id title cat_slug cat_name; do
        [ -z "$id" ] && continue
        out="${imgdir}/${id}.png"

        if [ "$FORCE" -eq 0 ] && is_png "$out"; then
            total_skip=$(( total_skip + 1 ))
        else
            curl -sS -A "$UA" -o "${out}.part" "${UPLOAD}/${id}_color.png"
            if is_png "${out}.part"; then
                mv "${out}.part" "$out"
                total_dl=$(( total_dl + 1 ))
                log "  + ${id}.png ($(stat -c%s "$out") bytes) ${title}"
            else
                rm -f "${out}.part"
                log "  ! ${id}: PNG が取得できませんでした (スキップ)"
                continue
            fi
            sleep "$SLEEP_SEC"
        fi

        printf '%s\t%s\t%s\t%s\n' "$id" "$title" "$cat_slug" "$cat_name" >> "${manifest}.tmp"
    done <<< "$posts"

    python3 -c '
import json, os, sys
src, dst = sys.argv[1], sys.argv[2]
items = []
with open(src, encoding="utf-8") as f:
    for line in f:
        line = line.rstrip("\n")
        if not line:
            continue
        id_, title, cat_slug, cat_name = line.split("\t")
        items.append({
            "id": id_,
            "title": title,
            "file": f"{id_}.png",
            "bytes": os.path.getsize(os.path.join(os.path.dirname(dst), "static", "img", f"{id_}.png")),
            "categorySlug": cat_slug,
            "category": cat_name,
        })
with open(dst, "w", encoding="utf-8") as f:
    json.dump(items, f, ensure_ascii=False, indent=2)
    f.write("\n")
print(f"{len(items)} 件を {dst} に書き出しました", file=sys.stderr)
' "${manifest}.tmp" "$manifest"
    rm -f "${manifest}.tmp"
done

log "完了: ${total_dl} 件ダウンロード / ${total_skip} 件は取得済みのためスキップ"
