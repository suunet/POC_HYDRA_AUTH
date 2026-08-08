package mail

import (
	"context"
	"fmt"
	"mime"
	"net/smtp"
)

type SMTPMailer struct {
	addr string
	from string
}

func NewSMTPMailer(addr, from string) *SMTPMailer {
	return &SMTPMailer{addr: addr, from: from}
}

func (m *SMTPMailer) SendConfirmationEmail(ctx context.Context, to, plainToken string) error {
	body := "以下のトークンでメールアドレスを確認してください:\r\n\r\n" + plainToken + "\r\n"
	return m.send(to, "メールアドレスの確認", body)
}

func (m *SMTPMailer) SendPasswordResetEmail(ctx context.Context, to, plainToken string) error {
	// NOTE: 有効期限はVAR-05（30分）。本文へ内部IDは出さない
	body := "以下のトークンでパスワードを再設定してください（有効期限30分）:\r\n\r\n" + plainToken + "\r\n"
	return m.send(to, "パスワードの再設定", body)
}

func (m *SMTPMailer) send(to, subjectText, body string) error {
	// NOTE: 非ASCII本文/件名のためMIMEヘッダを明示（charset欠落だと受信側がlatin-1解釈し文字化けする）
	subject := mime.BEncoding.Encode("UTF-8", subjectText)
	msg := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s",
		m.from, to, subject, body)
	return smtp.SendMail(m.addr, nil, m.from, []string{to}, []byte(msg))
}
