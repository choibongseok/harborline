// Package mail 은 메일 발송 어댑터다(클라우드 메일 API ↔ 고객 SMTP).
package mail

import "context"

// Message 는 보낼 메일이다.
type Message struct{ From, To, Subject, Body string }

// Sender 는 코어가 아는 유일한 메일 인터페이스다.
type Sender interface {
	Kind() string
	Send(ctx context.Context, m Message) error
}
