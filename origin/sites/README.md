# デモ用オリジンサイト

キャッシュヒット率の検証用に、性格の違う3つのサイトを用意している。
`origin` バイナリを `-siteDir` 違いで3プロセス起動して使う。

| | siteDir | テーマ | 実画像 | ユニークURL | キャッシュ特性 |
|---|---|---|---|---|---|
| **a** ソコスト・ポートレート | `sites/a` | 人物 | 35枚 | 40 | ホットセット型。LRU(256)に余裕で収まる |
| **b** ビズストック | `sites/b` | ビジネス・モノ・アイソメ | 80枚 | 330 | ロングテール型。LRUを超えるので eviction が起きる |
| **c** シーズンボード | `sites/c` | 季節・イベント・食べ物 | 35枚 | 217 | 動的混在型。クエリ付きサムネと no-store を混ぜてある |

サイトbの4サイズ(96/192/320px + 原寸)は起動時に生成する。実際にダウンロードするのは原寸の150枚だけ。

`site.json` の `host` は各サイトが想定するホスト名を書いてあるだけで、
オリジン自身は Host ヘッダを見ていない。振り分けは呼び出す側の役割。

## 素材の取得

イラストは [ソコスト](https://soco-st.com/) から取得する（商用可・クレジット表記不要）。
著作権はソコスト側にあるためリポジトリには含めていない。初回は必ず実行すること。

```bash
./origin/fetch-assets.sh
```

`origin/sites/*/static/img/` に PNG が、`origin/sites/*/images.json` にマニフェストが作られる。
どちらも `.gitignore` 済み。相手サーバに配慮して 1 リクエスト/秒で取得するので3分ほどかかる。
`--force` を付けると取得済みのファイルも上書きする。

取得元は `fetch-assets.sh` の `SITES` 配列で `サイトID:カテゴリID:枚数` の形で指定する。
カテゴリIDは `https://soco-st.com/wp-json/wp/v2/categories?per_page=100` で引ける
（4=人物 99=ポーズ 9=ビジネス 109=モノ 197=アイソメトリック 8=季節 14=イベント・行事 97=食べ物・料理）。

## 起動

```bash
cd origin
go run . -nodeId origin-a -listenAddr 127.0.0.1:9901 -siteDir sites/a
go run . -nodeId origin-b -listenAddr 127.0.0.1:9902 -siteDir sites/b
go run . -nodeId origin-c -listenAddr 127.0.0.1:9903 -siteDir sites/c
```

`-siteDir` を省略するか `site.json` の無いディレクトリを指すと、従来の単一ページとして動く。

## エンドポイント

共通:

- `/healthz` — 死活確認
- `/json` — ノード情報 (PoPCache / Origin の ID)
- `/api/manifest` — 画像一覧とバリアント定義。負荷試験スクリプト用
- `/ncdn-cachemeter.js`, `/ncdn-base.css` — 計測パネルと共通CSS (バイナリに埋め込み)

サイト固有:

| サイト | パス |
|---|---|
| a | `/img/{file}` |
| b | `/p/{n}` `/c/{slug}` `/img/{thumb\|card\|medium\|full}/{file}` |
| c | `/img/{file}?w={96\|160\|240\|360\|480}` `/api/random` `/live/now` |

## 計測パネル

各サイトのページ上部に、画像URLへ一斉にリクエストを投げて `X-Cache: Hit/Miss`
を集計するパネルがある。リクエスト数・同時実行数・アクセス分布（順番に一巡 /
Zipf / 一様ランダム）を選べる。PoPCache 越しに開かないと `X-Cache` が付かないので、
その場合は「X-Cache なし」として数える。

サイトbを PoPCache (LRU 256) 越しに叩いたときの実測値:

| 分布 | 400リクエスト | ヒット率 |
|---|---|---|
| 順番に一巡 | Hit 0 / Miss 400 | 0.0% |
| Zipf | Hit 364 / Miss 36 | 91.0% |

ユニークURL 330本に対しLRUは256エントリしかないので、順番に舐めると
次に使うエントリが必ず直前に追い出される（LRUの最悪ケース）。
