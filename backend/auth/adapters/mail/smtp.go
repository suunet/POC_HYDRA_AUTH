package mail

import (
	"context"
	"fmt"
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
	body := "以下のトークンでメールアドレスを確認してください:\n\n" + plainToken + "\n"
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\n\r\n%s", m.from, to, "メールアドレスの確認", body)
	return smtp.SendMail(m.addr, nil, m.from, []string{to}, []byte(msg))
}
