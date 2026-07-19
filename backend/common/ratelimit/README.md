# common/ratelimit — レート制限基盤

横断のレート制限基盤。固定ウィンドウ（VAR-16: 超過時は記録を更新しない・`retry_after` は TTL 残秒数）を Redis Lua 1往復の原子実行で判定し、`Result{Allowed, RetryAfter, Remaining, ResetAt}` を返す。

## 使い方

呼び出し側（各コンテキストの adapters）が組み立てて注入する。レート値・キー・fail-mode は呼び出し側ごとに**独立管理**する（VAR-12〜14・VAR-16「同値でも独立した設定値として管理」）。

例: 登録は `backend/auth/adapters/ratelimit/registration.go` が `FixedWindowLimiter` を `FailOpen` でラップして組み立てる。

## fail-mode（エンドポイント別）

| モード | 障害時の挙動 | 用途 |
|---|---|---|
| `FailOpen` | 通過（可用性優先） | 登録・メール確認など。abuse対策の一時停止より正規ユーザーの継続を優先 |
| `FailClosed`（ゼロ値） | 拒否（安全優先） | ログイン試行など、通してはいけない重要操作 |

いずれも障害は NFR-08（外部依存失敗=ERROR）でログ化する。**制約: fail-closed の拒否は正当な429と区別できず `retry_after=0` になる**（障害由来を伝える設計は fail-closed 初回利用チケットで行う）。「アラート」はこの ERROR ログによる可観測化を指し、別途の通知機構（ページャ等）は POC 範囲外。

## キーのハッシュ化（KeyHasher）

- 登録（UC-002）のメールキーは素通し（`PassthroughHasher`）。同一値が DB に平文保存済みで秘匿実益が薄いため平文運用
- メール確認（UC-003）の IP キーは **HMAC-SHA256＋サーバ秘密鍵で匿名化して結線済み**（INF-14）。IP は純粋な個人データで、plain SHA-256 は総当たりで逆引き可能なため HMAC が必須
- `KeyHasher` は差し替え可能な差込口（新しいキー種別の導入時は秘匿必要度で hasher を選ぶ）

## Retry-After ヘッダ

- 429 応答の `Retry-After` ヘッダの**正本は `common/http` の `ProblemErrorHandler`**（`Problem.retry_after` 非 nil 時に設定）
- openapi 生成型の 429 レスポンス（`Headers.RetryAfter`）は未使用。生成型経由で返すよう変えるとゼロ値ヘッダが出る乖離に注意

## 多層防御

本番ではエッジ（WAF / API ゲートウェイ）のレート制限が一次防御であり、本基盤（アプリ層）は**二次防御**。POC はアプリ層のみを実装する。**制約: Echo 既定の RealIP は X-Forwarded-For を無条件信頼するため、信頼プロキシ設定（IPExtractor）なしでは IP キーはヘッダ偽装で回避可能**（本番はエッジ/IPExtractor で担保）。
