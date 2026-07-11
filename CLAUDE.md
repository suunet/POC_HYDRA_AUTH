# CLAUDE.md

## プロジェクト概要

Go製認証システムのPOC。仕様駆動（仕様カタログ → ユースケース仕様 → チケット → コード）で開発する。

- `auth-service` — 自前IdP。メール/パスワード認証・JWT（RS256）発行
- `app-service` — JWT検証ミドルウェア・ヘルスチェックAPI

## ドキュメントマップ

| 見たいもの | 場所 |
|---|---|
| ドキュメント区画の定義 | `.docs/README.md` |
| 仕様カタログ（要件のSSOT・ID発番元） | `.docs/design/` |
| ユースケース仕様・ドメインモデル | `.docs/design/uc/` |
| プロセス全体像 | `.docs/operations/aidd/README.md` |
| ステータス遷移の正本 | `.docs/operations/aidd/workflow.md` |
| 規約 | `.docs/operations/policies/` |
| ひな形 | `.docs/operations/templates/` |
| チケット（git未管理） | `.docs/_stash/tickets/T-NNN/` |
| チケット作業の入口 | `/start-ticket`（`.claude/skills/`。作業系persona=DEV、レビュー系=BJサブエージェント） |

## 鉄則

1. **1フェーズ実行・停止:** チケット作業は `.docs/operations/aidd/workflow.md` の遷移表に従い、現在ステータスの1フェーズ分だけ実行して停止する。次フェーズへ自律的に進まない。
2. **人間関門のバイパス禁止:** `draft→ready-to-prepare`・`ready-to-approve→ready-to-implement`・`ready-to-implement→implementing` の3遷移と指示書§4.5はAIが書かない。ステータスは進める方向にしか書かない。
3. **上流優先:** 下流の詳細化で上流（仕様カタログ）の不足が判明したら、先に上流を承認付きで直し、下流を追従させる。
4. **ID規律:** 要素の相互参照はトレーサビリティID＋名前（例: `UC-001（アカウントを登録する）`）。発番は正本ファイルのみ・IDは不変・再利用禁止。影響範囲はIDでgrepして調べる。
5. **チケットは指示に徹する:** 恒久化すべき決定はチケットに留めず、Git管理の成果物（仕様カタログ・ユースケース仕様・ポリシー・コード）へ反映する。コミットメッセージに `T-NNN` を含める。
6. **チャットを正本にしない:** 合意は質疑・指示書・仕様カタログに書いて確定させる。
7. **合意前の案は `.docs/_stash/share/` で作る**（git未管理）。合意後に一般化して正式配置する。

## コーディング規約（要約）

- Go / slog または zap（Logging Guideline v1.0）/ RFC 9457
- PlantUML: `as` エイリアスは日本語ラベル、コード識別子は英語、要素にID併記
- 詳細は `.docs/operations/policies/coding.md`
