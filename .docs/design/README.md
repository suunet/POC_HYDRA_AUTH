# 仕様カタログ

要件の単一ソース・オブ・トゥルース。要素の種類ごとに1ファイルで管理し、ファイル間は**トレーサビリティID**で関連付ける。

## ファイル構成と読み順

| # | ファイル | 内容 | IDプレフィックス |
|---|---|---|---|
| 1 | [overview.md](overview.md) | システム概要・確定スコープ・システムコンテキスト図 | — |
| 2 | [actors.md](actors.md) | アクター | `ACT-NN` |
| 3 | [external-systems.md](external-systems.md) | 外部システム | `EXT-NN` |
| 4 | [buc.md](buc.md) | ビジネスユースケース（中核） | `BUC-XNN` |
| 5 | [information.md](information.md) | 情報 | `INF-NN` |
| 6 | [states.md](states.md) | 状態モデル・状態遷移 | `STM-NN`（個別状態は `STM-NN.状態名`） |
| 7 | [conditions.md](conditions.md) | 条件（ビジネスルール） | `CND-NN` |
| 8 | [variations.md](variations.md) | バリエーション（区分・値・ルール） | `VAR-NN` |
| 9 | [functional-requirements.md](functional-requirements.md) | 機能要求 | `FR-NN` |
| 10 | [non-functional-requirements.md](non-functional-requirements.md) | 非機能要求 | `NFR-NN` |

## ファイル間の参照関係

```
buc.md ──参照→ actors.md / external-systems.md / information.md / conditions.md
information.md ──参照→ states.md / variations.md
states.md ──参照→ buc.md（遷移トリガー）
conditions.md ──参照→ buc.md / variations.md / states.md
functional-requirements.md ──参照→ buc.md
```

## 記法ルール

- **ID規律:** 発番は各ファイル（正本）でのみ行う。相互参照は `ID（名前）` 形式（例: `INF-01（ユーザー情報）`）。IDは不変・再利用禁止。廃止時は行を残し `廃止` を明記する。影響範囲は `grep -rn "<ID>" .docs/` で調べる。
- **承認:** カタログの変更はチケットの関門（P4）とPRレビューで承認する。ファイル内にステータス表記は持たない（承認状態・履歴はGitで追う）。
- `UC-NNN` / `SCR-NN` / `EVT-NN` / `TMR-NN` は buc.md のフロー表（該当アクティビティ行）で発番する。UC-001〜019・SCR-01〜18（画面=APIエンドポイント）・EVT-01〜03 発番済み。`TMR-NN`（タイマー）は時間起動処理の出現まで0件（予約済み）。ユースケース仕様（`uc/UC-NNN.md`）の作成は各実装チケットのP3で行う。
- 図はPlantUML。`as` エイリアスは日本語ラベル、コード識別子は英語、要素にIDを併記する。

## 未決事項

- **認可サーバーの位置づけ**（トークン発行主体・EXT発番・NFR-02/INF-03（アクセストークン）の責務整理）は未決。**BUC-U04（ログイン）着手前に解消する**（T-003/T-005の質疑から引き継ぎ・2026-07-15記載）。
- **状態ごとのテーブル分離**（user_active/user_tmp/user_leave）は**見送り**（2026-07-16討議・決定）。理由: ①STM-01は無効化↔再有効化の双方向遷移を持ち、「ほぼ一方向のライフサイクル」に合わずテーブル移送の複雑さが増す ②POC規模では同方式が解決する書き込み競合・デッドロックのリスクが顕在化しない。**部分的な考え方は採用済み**（`deleted_at`による論理削除＝終端状態の分離）。**スケール時の再設計候補**: `status`/`updated_at`等の更新頻度が高いカラムを `user_state` 相当の別テーブルへ切り出す（`user_detail`分離に近い発想）。着手はUPDATE競合が実問題化した時点。
- **外部IdP連携（Google/Apple等）への拡張性**は未設計。設計時に考慮する点（T-005 Q-14・2026-07-16記載）:
  - `auth.users.password_hash` は現在 NOT NULL。IdPのみでパスワードを持たないアカウントが将来必要になる場合、null許容化のマイグレーションが要る
  - IdP識別子とRP側ユーザーの紐付けは `(idp種別, idp側ユーザー識別子)` の組を持つ子テーブル（例: `user_identities`）で表現し、`users` 本体には追加カラムを持たせない方針とする
  - メールアドレスの同一性だけでIdPアカウントと既存アカウントを自動連携しない（本人確認フローを経る）
  - 着手はIdP連携が実要件になった時点（BUC/UC追加のカタログ変更を伴う）

