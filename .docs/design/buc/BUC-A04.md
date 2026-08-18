# BUC-A04 管理者アカウント無効化

## メタデータ

| 項目 | 値 |
|---|---|
| BUC ID | BUC-A04 |
| BUC名 | 管理者アカウント無効化 |
| アクター | ACT-02（管理者・`super_admin`のみ） |
| スコープ | Must |
| 関連FR | FR-15 |
| 関連NFR | NFR-06, NFR-07, NFR-08, NFR-09 |
| 関連情報 | INF-01（ユーザー情報）, INF-04（リフレッシュトークン） |
| 関連条件 | CND-14（稼働中の`super_admin`が2人以上存在すること。対象が`super_admin`の場合）・CND-17（ロール認可のOR評価・`super_admin`） |
| 事後状態 | STM-01.無効化済み |

---

## ユースケース記述

### 事前条件

- アクセストークンが有効であること
- 操作者が `super_admin` ロールを持つこと

### 基本フロー

1. 管理者は対象ユーザーIDを送信する
2. システムは対象ユーザー（削除済みを除く）をDBで検索する
3. システムは対象ユーザーが管理者ロール（`super_admin`・`operator`・`system_admin`）を持つことを確認する
4. システムは対象ユーザーが無効化済みでないことを確認する
5. システムは対象ユーザーが `super_admin` の場合、稼働中（`disabled`/`deleted` を除く・`status='inactive'`）の `super_admin` が2人以上存在することをDBで確認する（CND-14）
6. システムは対象ユーザーのアカウントを無効化する
7. システムは対象ユーザーの全リフレッシュトークンを失効させる（失効理由: `account_disabled`）

> ステップ5〜7は単一トランザクションで実行する（計数と無効化を原子化し、並行無効化のTOCTOUを閉じる・T-020 Q-2）

8. システムは監査ログ（アカウント無効化、INFO）を記録する
9. システムは200レスポンスを返す（`revocation_reason: account_disabled` を含める）

### 代替フロー

なし

### 例外フロー

> 全ログにはNFR-09の必須フィールド（`ts`・`lvl`・`svc`・`ctx`・`trace_id`/`span_id`・`req_id`・`msg`）を含めること。以下の例示は差分フィールド（`ctx`・`msg`・`lvl`）のみを記載する。

**M1. アクセストークン検証失敗（事前条件・FR-19）**

- a. システムは処理を中断し、401 (Unauthorized)、`application/problem+json`、`type: https://example.com/probs/invalid-token`（一様）＋`WWW-Authenticate: Bearer` を返す（応答・ログともFR-19既定）

**M2. `super_admin` ロール不足（事前条件・CND-17）**

- a. システムは処理を中断し、403 (Forbidden)、`application/problem+json`、`type: https://example.com/probs/forbidden` を返す
- b. 監査ログ対象外。ただしビジネス例外としてWARNINGログを出力する（`{ ctx: "account_disable", msg: "権限不足", lvl: "WARNING" }`。NFR-08）

**E1. 対象ユーザーが存在しない場合（ステップ2）**

- a. システムは処理を中断する
- b. システムは404 (Not Found)、`application/problem+json`、`type: https://example.com/probs/user-not-found` を返す
- c. 監査ログ対象外。ただしビジネス例外としてWARNINGログを出力する（`{ ctx: "account_disable", msg: "対象ユーザーが存在しない", lvl: "WARNING" }`。NFR-08）
- 備考: `userId` の形式不正（UUID parse不能）も本フロー（404 user-not-found）で扱う。形式差による存在有無の情報漏洩を避けるため、不正形式と不存在を区別しない

**E2. 対象ユーザーが管理者ロールを持たない場合（ステップ3）**

- a. システムは処理を中断する
- b. システムは400 (Bad Request)、`application/problem+json`、`type: https://example.com/probs/not-admin-account` を返す
- c. 監査ログ対象外。ただしビジネス例外としてWARNINGログを出力する（`{ ctx: "account_disable", msg: "管理者ロール未保持のアカウントへの無効化試行", lvl: "WARNING" }`。NFR-08）

**E3. 対象ユーザーが既に無効化済みの場合（ステップ4）**

- a. システムは処理を中断する
- b. システムは409 (Conflict)、`application/problem+json`、`type: https://example.com/probs/account-already-disabled` を返す
- c. 監査ログ対象外。ただしビジネス例外としてWARNINGログを出力する（`{ ctx: "account_disable", msg: "既に無効化済みのアカウント", lvl: "WARNING" }`。NFR-08）

**E4. 稼働中の `super_admin` が1人しか存在しない場合（ステップ5・CND-14）**

- a. システムは処理を中断する
- b. システムは409 (Conflict)、`application/problem+json`、`type: https://example.com/probs/last-super-admin` を返す
- c. 監査ログ対象外。ただしビジネス例外としてWARNINGログを出力する（`{ ctx: "account_disable", msg: "最後のsuper_adminの無効化試行", lvl: "WARNING" }`。NFR-08）
- 備考: 計数対象は無効化済み（`disabled`）・削除済み（`deleted`）を除く稼働中の `super_admin`（`status='inactive'`）。並行無効化のTOCTOU防止のため計数と無効化を単一トランザクション境界で原子的に評価する

**E5. トランザクション失敗（ステップ6〜7）**

- a. システムはトランザクション全体をロールバックする（アカウント無効化・全セッション失効のいずれも適用しない）
- b. システムは500 (Internal Server Error)、`application/problem+json`、`type: https://example.com/probs/internal-error` を返す
- c. 外部依存失敗としてERRORログを出力する（`{ ctx: "account_disable", msg: "アカウント無効化トランザクション失敗", lvl: "ERROR" }`。NFR-08）
- ロールバックスコープ: ステップ6〜7の全操作。アカウント状態・セッションのいずれも変更前の状態に戻す

---

## ロバストネス図

```plantuml
@startuml
skinparam componentStyle rectangle
skinparam backgroundColor White

actor "管理者\n(super_admin)" as 管理者

boundary "POST /admin/accounts/:userId/disable" as 無効化API
control "FR-19 JWT検証 + ロール認可(M1/M2)" as 認可MW
control "AccountDisableUseCase" as ユースケース
entity "UserRepository" as ユーザーRepo
entity "RefreshTokenRepository" as リフレッシュトークンRepo

管理者 --> 無効化API : userId\n[Authorization: Bearer <accessToken>]

無効化API --> 認可MW : M1(AT検証・401一様) / M2(super_admin・403)
認可MW --> ユースケース : disable(userId, operator=sub)

ユースケース --> ユーザーRepo : findById(userId, excludeDeleted: true)
ユースケース --> ユースケース : checkAdminRole(user)
ユースケース --> ユースケース : checkNotDisabled(user)
ユースケース --> ユーザーRepo : count稼働中super_admins()\n（status='inactive'・対象がsuper_adminの場合・無効化と同一Tx）
ユースケース --> ユーザーRepo : disable(userId)
ユースケース --> リフレッシュトークンRepo : revokeAllByUserId(userId, reason: account_disabled)

無効化API <-- ユースケース : 200 OK { revocation_reason: account_disabled }

@enduml
```

---

## シーケンス図

```mermaid
sequenceDiagram
  actor Admin as 管理者 (super_admin)
  participant DisableAPI as 無効化API
  participant UseCase as ユースケース
  participant UserRepo as ユーザーRepo
  participant RefreshRepo as リフレッシュトークンRepo
  Admin->>DisableAPI: POST /admin/accounts/:userId/disable<br/>[Authorization: Bearer <accessToken>]
  alt M1: AT検証失敗（FR-19）
  DisableAPI-->>Admin: 401 invalid-token（一様）+ WWW-Authenticate: Bearer
  end
  alt M2: super_adminロール不足（CND-17）
  DisableAPI-->>Admin: 403 forbidden
  end
  DisableAPI->>UseCase: disable(userId)
  UseCase->>UserRepo: findById(userId, excludeDeleted: true)
  UserRepo-->>UseCase: result
  alt E1: ユーザーが存在しない
  UseCase-->>DisableAPI: UserNotFoundError
  DisableAPI-->>Admin: 404 Not Found<br/>application/problem+json<br/>type: .../user-not-found
  end
  UseCase->>UseCase: checkAdminRole(user)
  alt E2: 管理者ロール未保持
  UseCase-->>DisableAPI: NotAdminAccountError
  DisableAPI-->>Admin: 400 Bad Request<br/>application/problem+json<br/>type: .../not-admin-account
  end
  UseCase->>UseCase: checkNotDisabled(user)
  alt E3: 既に無効化済み
  UseCase-->>DisableAPI: AccountAlreadyDisabledError
  DisableAPI-->>Admin: 409 Conflict<br/>application/problem+json<br/>type: .../account-already-disabled
  end
  critical トランザクション ステップ5〜7（計数と無効化を原子化・TOCTOU閉塞）
  opt 対象ユーザーが super_admin の場合
  UseCase->>UserRepo: count稼働中super_admins()（status='inactive'・行ロック/条件付きUPDATE）
  UserRepo-->>UseCase: count
  alt E4: 稼働中super_admin が1人のみ
  UseCase-->>DisableAPI: LastSuperAdminError
  DisableAPI-->>Admin: 409 Conflict<br/>application/problem+json<br/>type: .../last-super-admin
  end
  end
  UseCase->>UserRepo: disable(userId)（TransitionUserStatus・遷移元inactive）
  UserRepo-->>UseCase: updated
  UseCase->>RefreshRepo: revokeAllByUserId<br/>(userId, reason: account_disabled)
  RefreshRepo-->>UseCase: revokedCount
  end
  alt E5: トランザクション失敗
  Note right of UseCase: ERROR ログ<br/>{ ctx: "account_disable",<br/>msg: "アカウント無効化トランザクション失敗" }<br/>ロールバック: 全操作を取消
  UseCase-->>DisableAPI: InternalError
  DisableAPI-->>Admin: 500 Internal Server Error<br/>application/problem+json<br/>type: .../internal-error
  end
  Note right of Admin: INFO 監査ログ<br/>{ ctx: "account_disable", msg: "アカウント無効化" }
  UseCase-->>DisableAPI: success
  DisableAPI-->>Admin: 200 OK<br/>{ revocation_reason: account_disabled }
```

---

## 監査ログ

| イベント | レベル | ターゲット | 備考 |
|----------|--------|------------|------|
| アカウント無効化 | INFO | 対象user_id | 基本フロー完了時。操作者の管理者ID（AT `sub`）も `operator` として記録する（対象user_id・操作者subともUUID＝NFR-09機密に非該当） |

---

## 備考・設計上の決定事項

| 項目 | 決定内容 | 理由 |
|---|---|---|
| 無効化対象 | 管理者ロールを持つアカウントのみ無効化可能 | BUC名およびFR-15が「管理者アカウント」を対象としている |
| `super_admin` の保護 | 最後の `super_admin` は無効化不可 | CND-14（`super_admin`が2人以上存在すること）に準拠。システム管理不能状態を防止する |
| 全セッション無効化 | アカウント無効化と同時に全リフレッシュトークンを失効させる | FR-15準拠。無効化されたアカウントの既存セッションが残ることを防ぐ |
| 失効理由 | `account_disabled` を使用する | VAR-10（セッション失効理由コード）に準拠 |
| 自己無効化 | `super_admin` が自身を無効化することは、他に `super_admin` が存在する場合に限り許可する | カタログの条件（CND-14）は「2人以上存在すること」のみで、自己無効化の制限は定義されていない。条件を満たす限り許可する |
| 既に無効化済みの場合 | 409 Conflict を返す | BUC-A03（トークン強制失効）の既失効済みパターンと同一方針。管理者操作では正確なフィードバックを優先する |
