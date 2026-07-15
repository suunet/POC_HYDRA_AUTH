# BUC-U02 メールアドレス確認

## メタデータ

| 項目 | 値 |
|---|---|
| BUC ID | BUC-U02 |
| BUC名 | メールアドレス確認 |
| アクター | ACT-01（ユーザー） |
| スコープ | Must |
| 関連FR | FR-03 |
| 関連情報 | INF-01（ユーザー情報）, INF-06（メール確認トークン） |
| 関連条件 | CND-09（メール確認トークンが有効期限内であること） |
| 事後状態 | STM-01.未認証 |

---

## ユースケース記述

### 事前条件

- メール確認トークンが有効期限内であること

### 基本フロー

1. ユーザーはメール確認トークンを送信する
2. システムはトークンをDBで検索する
3. システムはトークンの有効期限を検証する
4. システムはトークンを使用済みに更新する（使い切り）
5. システムはユーザーのステータスを `未認証` に更新する
6. システムは200レスポンスを返す

### 代替フロー

なし

### 例外フロー

> 全ログにはNFR-09の必須フィールド（`ts`・`lvl`・`svc`・`ctx`・`trace_id`/`span_id`・`req_id`・`msg`）を含めること。以下の例示は差分フィールド（`ctx`・`msg`・`lvl`）のみを記載する。

**E1. トークンが存在しない、または使用済みの場合（ステップ2）**

- a. システムは処理を中断する
- b. システムは400 (Bad Request)、`application/problem+json`、`type: https://example.com/probs/invalid-token` を返す
- c. 監査ログ対象外。ただしビジネス例外としてWARNINGログを出力する（`{ ctx: "email_verification", msg: "無効なメール確認トークン", lvl: "WARNING" }`。NFR-08）

**E2. トークンが有効期限切れの場合（ステップ3）**

- a. システムは処理を中断する
- b. システムは400 (Bad Request)、`application/problem+json`、`type: https://example.com/probs/token-expired` を返す
- c. 監査ログ対象外。ただしビジネス例外としてWARNINGログを出力する（`{ ctx: "email_verification", msg: "メール確認トークン期限切れ", lvl: "WARNING" }`。NFR-08）

---

## ロバストネス図

```plantuml
@startuml
skinparam componentStyle rectangle
skinparam backgroundColor White

actor "ユーザー" as ユーザー

boundary "POST /auth/email-verify" as 確認API
control "EmailVerificationUseCase" as ユースケース
entity "EmailConfirmTokenRepository" as 確認トークンRepo
entity "UserRepository" as ユーザーRepo

ユーザー --> 確認API : token

確認API --> ユースケース : verify(token)

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
  actor ユーザー as ユーザー
  participant 確認API as POST /auth/email-verify
  participant ユースケース as EmailVerificationUseCase
  participant 確認トークンRepo as EmailConfirmTokenRepository (DB)
  participant ユーザーRepo as UserRepository (DB)
  ユーザー->>確認API: POST /auth/email-verify<br/>{ token }
  確認API->>ユースケース: verify(token)
  ユースケース->>確認トークンRepo: findByToken(token)
  確認トークンRepo-->>ユースケース: result
  alt トークンが存在しない・使用済み
  ユースケース-->>確認API: InvalidTokenError
  確認API-->>ユーザー: 400 Bad Request<br/>application/problem+json<br/>type: .../invalid-token
  end
  ユースケース->>ユースケース: validateExpiry(token)
  alt トークンが有効期限切れ
  ユースケース-->>確認API: TokenExpiredError
  確認API-->>ユーザー: 400 Bad Request<br/>application/problem+json<br/>type: .../token-expired
  end
  ユースケース->>確認トークンRepo: markAsUsed(token)
  ユースケース->>ユーザーRepo: updateStatus(user{ status: "未認証" })
  ユーザーRepo-->>ユースケース: ok
  ユースケース-->>確認API: success
  確認API-->>ユーザー: 200 OK
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