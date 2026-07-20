# BUC-U02 メールアドレス確認

## メタデータ

| 項目 | 値 |
|---|---|
| BUC ID | BUC-U02 |
| BUC名 | メールアドレス確認 |
| アクター | ACT-01（ユーザー） |
| スコープ | Must |
| 関連FR | FR-03 |
| 関連情報 | INF-01（ユーザー情報）, INF-06（メール確認トークン）, INF-14（メール確認試行記録） |
| 関連条件 | CND-09（メール確認トークンが有効期限内であること） |
| 事後状態 | STM-01.未認証 |

---

## ユースケース記述

### 事前条件

- メール確認トークンが有効期限内であること

### 基本フロー

1. ユーザーはメール確認トークンを送信する
2. システムは送信元IP単位のレートリミットを検証・記録する（VAR-17）
3. システムはトークンをDBで検索する
4. システムはトークンの有効期限を検証する
5. システムはトークンを使用済みに更新する（使い切り）
6. システムはユーザーのステータスを `未認証` に更新する
7. システムは200レスポンスを返す

### 代替フロー

なし

### 例外フロー

> 全ログにはNFR-09の必須フィールド（`ts`・`lvl`・`svc`・`ctx`・`trace_id`/`span_id`・`req_id`・`msg`）を含めること。以下の例示は差分フィールド（`ctx`・`msg`・`lvl`）のみを記載する。

**E1. トークンが存在しない、または使用済みの場合（ステップ3）**

- a. システムは処理を中断する
- b. システムは400 (Bad Request)、`application/problem+json`、`type: https://example.com/probs/invalid-token` を返す
- c. 監査ログ対象外。ただしビジネス例外としてWARNINGログを出力する（`{ ctx: "email_verification", msg: "無効なメール確認トークン", lvl: "WARNING" }`。NFR-08）

**E2. トークンが有効期限切れの場合（ステップ4）**

- a. システムは処理を中断する
- b. システムは400 (Bad Request)、`application/problem+json`、`type: https://example.com/probs/token-expired` を返す
- c. 監査ログ対象外。ただしビジネス例外としてWARNINGログを出力する（`{ ctx: "email_verification", msg: "メール確認トークン期限切れ", lvl: "WARNING" }`。NFR-08）

**E4. レートリミット超過の場合（ステップ2・VAR-17）**

- a. システムは処理を中断する
- b. システムは429 (Too Many Requests)、`application/problem+json`、`type: https://example.com/probs/rate-limit-exceeded`、`retry_after`（秒・TTL残）および `Retry-After` ヘッダを返す
- c. 監査ログ対象外。ただしビジネス例外としてWARNINGログを出力する（`{ ctx: "email_verification", msg: "メール確認レートリミット超過", lvl: "WARNING" }`。NFR-08）

---

## ロバストネス図

```plantuml
@startuml
skinparam componentStyle rectangle
skinparam backgroundColor White

actor "ユーザー" as ユーザー

boundary "POST /auth/email-verify" as 確認API
control "EmailVerificationUseCase" as ユースケース
control "EmailVerifyRateLimiter" as レート制限
entity "EmailConfirmTokenRepository" as 確認トークンRepo
entity "UserRepository" as ユーザーRepo

ユーザー --> 確認API : token

確認API --> ユースケース : verify(token)

ユースケース --> レート制限 : checkAndRecord(ip)
ユースケース --> 確認トークンRepo : findByToken(token)
ユースケース --> ユースケース : validateExpiry(token)
ユースケース --> 確認トークンRepo : markAsUsed(token)
ユースケース --> ユーザーRepo : updateStatus(user{ status: "未認証" })

確認API <-- ユースケース : 200 OK

@enduml
```

---

## シーケンス図

```mermaid
sequenceDiagram
  actor User as ユーザー
  participant VerifyAPI as 確認API
  participant UseCase as ユースケース
  participant RateLimiter as レート制限
  participant TokenRepo as 確認トークンRepo
  participant UserRepo as ユーザーRepo
  User->>VerifyAPI: POST /auth/email-verify<br/>{ token }
  VerifyAPI->>UseCase: verify(token)
  UseCase->>RateLimiter: IPレート判定・記録（VAR-17）
  alt レートリミット超過
  UseCase-->>VerifyAPI: RateLimitedError
  VerifyAPI-->>User: 429 Too Many Requests<br/>application/problem+json<br/>type: .../rate-limit-exceeded
  end
  UseCase->>TokenRepo: findByToken(token)
  TokenRepo-->>UseCase: result
  alt トークンが存在しない・使用済み
  UseCase-->>VerifyAPI: InvalidTokenError
  VerifyAPI-->>User: 400 Bad Request<br/>application/problem+json<br/>type: .../invalid-token
  end
  UseCase->>UseCase: validateExpiry(token)
  alt トークンが有効期限切れ
  UseCase-->>VerifyAPI: TokenExpiredError
  VerifyAPI-->>User: 400 Bad Request<br/>application/problem+json<br/>type: .../token-expired
  end
  UseCase->>TokenRepo: markAsUsed(token)
  UseCase->>UserRepo: updateStatus(user{ status: "未認証" })
  UserRepo-->>UseCase: ok
  UseCase-->>VerifyAPI: success
  VerifyAPI-->>User: 200 OK
```

---

## 監査ログ

本BUCでは監査ログの対象操作なし。

---

## 備考・設計上の決定事項

| 項目 | 決定内容 | 理由 |
|---|---|---|
| トークン不存在・使用済みの統一エラー | 両ケースとも `invalid-token` で返す | トークンの状態詳細を返すことで攻撃者がトークン有効性を探索できるリスクを排除する |
| トークン有効期限 | 24時間 | VAR-06（メール確認トークン有効期限: 24時間）に定義 |
| トークンの使い切り | 検証成功後に即使用済みフラグを立てる | 同一トークンの再利用によるアカウント状態の不正操作を防ぐ |
| IP単位レートリミット | VAR-17（1分に10回・超過時は記録を更新しない・`retry_after`=TTL残秒） | 256bit乱数トークンの総当たりは非現実的だが、abuse・スキャン抑止の多層防御としてIP単位で制限する。キーのIPはHMAC-SHA256＋サーバ秘密鍵でハッシュ化（INF-14・個人データ配慮） |
| 例外フロー番号 | レートリミット超過は E4（E3は欠番） | UC-002/UC-003と例外番号のスロットを揃える（E4=レートリミット超過）。grepでの横断突合を優先 |