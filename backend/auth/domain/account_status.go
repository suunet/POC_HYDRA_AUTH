package domain

// STM-01（アカウント状態）の英語ID。各状態の意味・遷移の正本は .docs/design/states.md の状態図。
// auth.users.status の値であり、CHECK制約 users_status_check の許可値と一致する（列挙は states.md 状態図順）。
const (
	StatusMailUnverified = "mail_unverified"
	StatusInactive       = "inactive" // NOTE: 名称に反し「メール確認済み・ログイン可能」な状態（STM-01.未認証）
	StatusDisabled       = "disabled"
	StatusDeleted        = "deleted"
)
