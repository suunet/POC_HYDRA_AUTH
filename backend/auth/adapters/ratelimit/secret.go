package ratelimit

// DevDefaultHMACSecret はレート制限キー匿名化（INF-14）の公知な開発用既定鍵。
// 本番でこの値が使われる構成は鍵秘匿が崩れるため不可。
const DevDefaultHMACSecret = "local-dev-secret"

// ResolveHMACSecret は env 値から実効鍵と weak（＝公知既定鍵）判定を返す。
// NOTE: 未設定だけでなく既定鍵の明示設定も weak として検知する（本番で気付かず既定鍵を使う事故を防ぐ・T-006 c6-BJ#5）。
func ResolveHMACSecret(env string) (secret string, weak bool) {
	if env == "" {
		return DevDefaultHMACSecret, true
	}
	return env, env == DevDefaultHMACSecret
}
