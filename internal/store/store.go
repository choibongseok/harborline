// Package store 는 PostgreSQL 접근이다. 릴리스별 스키마 차이(state 컬럼)는 기능 플래그로 다룬다.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Features 는 빌드에 켜진 릴리스 기능이다.
type Features struct {
	Stats     bool // v1.1: /api/stats
	State     bool // v1.2: state·priority 컬럼을 읽고 쓴다(status 도 같이 쓴다)
	StateOnly bool // v2.0: status 컬럼이 없다
}

// Store 는 DB 핸들이다.
type Store struct {
	Pool *pgxpool.Pool
	F    Features
}

// Inquiry 는 문의다.
type Inquiry struct {
	ID            string       `json:"id"`
	Subject       string       `json:"subject"`
	Body          string       `json:"body"`
	CustomerEmail string       `json:"customer_email"`
	Status        string       `json:"status"`
	State         *int         `json:"state,omitempty"`
	Priority      *int         `json:"priority,omitempty"`
	CreatedAt     time.Time    `json:"created_at"`
	Replies       []Reply      `json:"replies,omitempty"`
	Attachments   []Attachment `json:"attachments,omitempty"`
}

// Reply 는 답변이다.
type Reply struct {
	ID        string    `json:"id"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// Attachment 는 첨부 메타데이터다.
type Attachment struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	SizeBytes  int64  `json:"size_bytes"`
	SHA256     string `json:"sha256"`
	StorageKey string `json:"-"`
}

// ErrNotFound 는 대상이 없을 때다.
var ErrNotFound = errors.New("찾을 수 없음")

func (s *Store) Ping(ctx context.Context) error { return s.Pool.Ping(ctx) }

var statusNames = []string{"open", "answered", "closed"}

func statusOf(i int) string {
	if i >= 0 && i < len(statusNames) {
		return statusNames[i]
	}
	return "open"
}
func stateOf(name string) int {
	for i, n := range statusNames {
		if n == name {
			return i
		}
	}
	return 0
}

// CreateInquiry 는 문의를 만든다.
func (s *Store) CreateInquiry(ctx context.Context, subject, body, email string) (string, error) {
	var id string
	var err error
	switch {
	case s.F.StateOnly:
		err = s.Pool.QueryRow(ctx, `INSERT INTO inquiries(subject, body, customer_email, state) VALUES ($1,$2,$3,0) RETURNING id`, subject, body, email).Scan(&id)
	case s.F.State:
		err = s.Pool.QueryRow(ctx, `INSERT INTO inquiries(subject, body, customer_email, status, state) VALUES ($1,$2,$3,'open',0) RETURNING id`, subject, body, email).Scan(&id)
	default:
		err = s.Pool.QueryRow(ctx, `INSERT INTO inquiries(subject, body, customer_email) VALUES ($1,$2,$3) RETURNING id`, subject, body, email).Scan(&id)
	}
	return id, err
}

func (s *Store) selectCols() string {
	switch {
	case s.F.StateOnly:
		return `id, subject, body, customer_email, state, priority, created_at`
	case s.F.State:
		return `id, subject, body, customer_email, state, priority, created_at`
	default:
		return `id, subject, body, customer_email, status, created_at`
	}
}

func (s *Store) scan(row pgx.Row) (Inquiry, error) {
	var q Inquiry
	if s.F.State || s.F.StateOnly {
		var st, pr int16
		if err := row.Scan(&q.ID, &q.Subject, &q.Body, &q.CustomerEmail, &st, &pr, &q.CreatedAt); err != nil {
			return q, err
		}
		si, pi := int(st), int(pr)
		q.State, q.Priority, q.Status = &si, &pi, statusOf(si)
		return q, nil
	}
	err := row.Scan(&q.ID, &q.Subject, &q.Body, &q.CustomerEmail, &q.Status, &q.CreatedAt)
	return q, err
}

// ListInquiries 는 최신순 목록이다. status 가 비면 전체.
func (s *Store) ListInquiries(ctx context.Context, status string, limit int) ([]Inquiry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT ` + s.selectCols() + ` FROM inquiries`
	args := []any{}
	if status != "" {
		if s.F.State || s.F.StateOnly {
			q += ` WHERE state = $1`
			args = append(args, stateOf(status))
		} else {
			q += ` WHERE status = $1`
			args = append(args, status)
		}
	}
	q += ` ORDER BY created_at DESC, id LIMIT ` + itoa(limit)
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Inquiry{}
	for rows.Next() {
		in, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b)
}

// GetInquiry 는 답변·첨부를 포함해 읽는다.
func (s *Store) GetInquiry(ctx context.Context, id string) (Inquiry, error) {
	in, err := s.scan(s.Pool.QueryRow(ctx, `SELECT `+s.selectCols()+` FROM inquiries WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return in, ErrNotFound
	}
	if err != nil {
		return in, err
	}
	rr, err := s.Pool.Query(ctx, `SELECT id, author, body, created_at FROM replies WHERE inquiry_id=$1 ORDER BY created_at, id`, id)
	if err != nil {
		return in, err
	}
	for rr.Next() {
		var r Reply
		if err := rr.Scan(&r.ID, &r.Author, &r.Body, &r.CreatedAt); err != nil {
			rr.Close()
			return in, err
		}
		in.Replies = append(in.Replies, r)
	}
	rr.Close()
	ar, err := s.Pool.Query(ctx, `SELECT id, name, size_bytes, sha256, storage_key FROM attachments WHERE inquiry_id=$1 ORDER BY created_at, id`, id)
	if err != nil {
		return in, err
	}
	defer ar.Close()
	for ar.Next() {
		var a Attachment
		if err := ar.Scan(&a.ID, &a.Name, &a.SizeBytes, &a.SHA256, &a.StorageKey); err != nil {
			return in, err
		}
		in.Attachments = append(in.Attachments, a)
	}
	return in, ar.Err()
}

// AddReply 는 답변을 저장하고 문의를 answered 로 바꾼다.
func (s *Store) AddReply(ctx context.Context, inquiryID, author, body string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO replies(inquiry_id, author, body) VALUES ($1,$2,$3)`, inquiryID, author, body); err != nil {
		return err
	}
	var tag int64
	switch {
	case s.F.StateOnly:
		ct, err := tx.Exec(ctx, `UPDATE inquiries SET state=1 WHERE id=$1`, inquiryID)
		if err != nil {
			return err
		}
		tag = ct.RowsAffected()
	case s.F.State:
		ct, err := tx.Exec(ctx, `UPDATE inquiries SET status='answered', state=1 WHERE id=$1`, inquiryID)
		if err != nil {
			return err
		}
		tag = ct.RowsAffected()
	default:
		ct, err := tx.Exec(ctx, `UPDATE inquiries SET status='answered' WHERE id=$1`, inquiryID)
		if err != nil {
			return err
		}
		tag = ct.RowsAffected()
	}
	if tag == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}

// SetPriority 는 v1.2 이상에서만 동작한다.
func (s *Store) SetPriority(ctx context.Context, id string, p int) error {
	if !(s.F.State || s.F.StateOnly) {
		return errors.New("이 릴리스는 priority 를 지원하지 않음")
	}
	_, err := s.Pool.Exec(ctx, `UPDATE inquiries SET priority=$2 WHERE id=$1`, id, p)
	return err
}

// AddAttachment 는 첨부 메타데이터를 저장한다.
func (s *Store) AddAttachment(ctx context.Context, inquiryID, name string, size int64, sha, key string) (string, error) {
	var id string
	err := s.Pool.QueryRow(ctx, `INSERT INTO attachments(inquiry_id, name, size_bytes, sha256, storage_key) VALUES ($1,$2,$3,$4,$5) RETURNING id`, inquiryID, name, size, sha, key).Scan(&id)
	return id, err
}

// GetAttachment 는 첨부 메타데이터를 읽는다.
func (s *Store) GetAttachment(ctx context.Context, id string) (Attachment, error) {
	var a Attachment
	err := s.Pool.QueryRow(ctx, `SELECT id, name, size_bytes, sha256, storage_key FROM attachments WHERE id=$1`, id).Scan(&a.ID, &a.Name, &a.SizeBytes, &a.SHA256, &a.StorageKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// Stats 는 상태별 건수다(v1.1 기능).
func (s *Store) Stats(ctx context.Context) (map[string]int, error) {
	col := "status"
	if s.F.StateOnly {
		col = "state::text"
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+col+`, count(*) FROM inquiries GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{"open": 0, "answered": 0, "closed": 0}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		if s.F.StateOnly {
			var i int
			for _, c := range k {
				i = i*10 + int(c-'0')
			}
			k = statusOf(i)
		}
		out[k] += n
	}
	return out, rows.Err()
}

// UserHash 는 자체 로그인용 사용자 조회다.
func (s *Store) UserHash(ctx context.Context, username string) (string, string, error) {
	var h, r string
	err := s.Pool.QueryRow(ctx, `SELECT password_hash, role FROM users WHERE username=$1`, username).Scan(&h, &r)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return h, r, err
}

// UpsertUser 는 사용자(bcrypt 해시)를 만든다.
func (s *Store) UpsertUser(ctx context.Context, username, hash, role string) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO users(username, password_hash, role) VALUES ($1,$2,$3) ON CONFLICT (username) DO UPDATE SET password_hash=EXCLUDED.password_hash, role=EXCLUDED.role`, username, hash, role)
	return err
}
