# コーディング規約

## 詳細規約（ローカル）

- Go実装の詳細規約は同ディレクトリの `coding-go.md`（git未管理・`.gitignore` で除外）に定める。**本ファイル（Git管理）と矛盾する場合は本ファイルが優先**し、ローカル規約は細則の位置づけとする。

## 言語・構成

- 言語: Go
- モノレポ構成: `auth-service`（メール/パスワード認証・JWT RS256発行）／ `app-service`（JWT検証ミドルウェア・ヘルスチェックAPI）
- JWEはスコープ外

## ログ

- slog または zap（Logging Guideline v1.0 準拠）
- stdout出力はローカル開発のみ

## エラーレスポンス

- RFC 9457（Problem Details）準拠
- 拡張フィールド: `revocation_reason`（セッション失効の型付き理由コード）

## 主要な設計決定（確定済み）

| 項目 | 決定内容 |
|------|----------|
| 削除方式 | 論理削除 + GDPR対応カラムnull化 |
| リフレッシュトークン | ローテーション + 再利用検知で全セッション無効化 |
| セッション失効 | 型付き理由コード |
| レート制限 | Redisベース（ログイン: 10回/15分、その他: 5分/メール） |

## 図・ドキュメント内のコード

- **シーケンス図・ER図は Mermaid**（`sequenceDiagram`・`erDiagram`）で書く。シーケンス図のエイリアスは `participant <英語ID> as <日本語ラベル>`。ER図はテーブル名・列名を英語（DB定義どおり）とし、カタログID（VAR-NN・STM-NN等）は列コメントに併記する
- その他の図（ロバストネス図・状態遷移図等）は PlantUML: `as` エイリアスは日本語ラベル、コード識別子は英語
- 図中の要素にはトレーサビリティIDを併記する（例: `participant API as SCR-01 登録API`・`usecase "UC-001 アカウントを登録する" as UC001`）

## テスト

- テスト名（またはテスト直上のコメント）に対応するユースケースID `UC-NNN` を含める。`grep UC-NNN` でUC仕様からテストまで辿れる状態を保つ
- UCを持たない基盤テスト（DB制約検証等）は、NFR-ID または カタログID（STM-NN/INF-NN）の参照で代替可（例: `backend/tests/component/users_status_check_test.go` の `STM-01/INF-01`。VAR-NN 等への拡張は必要になった時点で再検討する）
- コード内コメントのID参照はID単独可とする。ID＋名前併記の規律（鉄則4・`CLAUDE.md`）はドキュメント側に適用し、コード内コメントは本規定が例外を定める

## コメント

- 補足コメントにはアノテーションを付ける（大文字＋コロン。`TODO:`・`FIXME:`・`HACK:`・`XXX:`・`REVIEW:`・`OPTIMIZE:`・`NOTE:`・`WARNING:`）。`TODO:`/`FIXME:` にはチケットID等の対応手がかりを添える。詳細（各アノテーションの意味・`CHANGED:` の扱い）はローカル詳細規約 `coding-go.md` §8

## コミット

- コミットメッセージに対応チケットの `T-NNN` を含める
