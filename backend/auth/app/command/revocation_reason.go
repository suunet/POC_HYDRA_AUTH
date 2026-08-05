package command

// VAR-10（セッション失効理由コード）のうち発行実績のある値。発行箇所はこの定数を正とする
// （テストの期待値は独立ピンとして生リテラルを維持し、定数自体の誤変更を検知する）。
// role_changed / forced_revocation は発行機能（BUC-A03 トークン強制失効・BUC-A07 ロール変更）の実装時に追加する。
const (
	RevocationReasonPasswordChanged    = "password_changed"
	RevocationReasonTokenReuseDetected = "token_reuse_detected"
	RevocationReasonAccountDeleted     = "account_deleted"
	RevocationReasonAccountDisabled    = "account_disabled"
)
