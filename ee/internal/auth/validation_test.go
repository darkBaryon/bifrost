// 本文件验证联合记录与调用方声明的身份边界，避免拆分后只验证会话、不验证账号。
package auth

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/session"
)

func TestValidateRecord(t *testing.T) {
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	base := Record{Account: Credentials{User: User{ID: "account", Status: StatusActive, MustChangePassword: true}, AuthVersion: 3},
		Session: session.Record{ID: "session", AccountID: "account", IssuedAuthVersion: 3, ExpiresAt: at.Add(time.Hour)}}
	for _, test := range []struct {
		name   string
		change func(*Record)
	}{
		{"missing account", func(r *Record) { r.Account.ID = "" }},
		{"disabled", func(r *Record) { r.Account.Status = "disabled" }},
		{"missing session", func(r *Record) { r.Session.ID = "" }},
		{"different account", func(r *Record) { r.Session.AccountID = "other" }},
		{"expired boundary", func(r *Record) { r.Session.ExpiresAt = at }},
		{"revoked", func(r *Record) { r.Session.RevokedAt = &at }},
		{"old version", func(r *Record) { r.Session.IssuedAuthVersion-- }},
	} {
		t.Run(test.name, func(t *testing.T) {
			record := base
			test.change(&record)
			if _, err := ValidateRecord(record, at); err != ErrUnauthorized {
				t.Fatalf("invalid record accepted: %v", err)
			}
		})
	}
	principal, err := ValidateRecord(base, at)
	if err != nil || !principal.MustChangePassword {
		t.Fatalf("limited session must remain authenticated: %v", err)
	}
	for _, change := range []func(*session.Principal){
		func(p *session.Principal) { p.AccountID = "other" },
		func(p *session.Principal) { p.SessionID = "other" },
		func(p *session.Principal) { p.AuthVersion-- },
	} {
		claimed := principal
		change(&claimed)
		if _, err := RevalidateRecord(base, claimed, at); err != ErrUnauthorized {
			t.Fatalf("forged principal accepted: %v", err)
		}
	}
	principal.MustChangePassword = false
	actual, err := RevalidateRecord(base, principal, at)
	if err != nil || !actual.MustChangePassword {
		t.Fatal("trusted claimed password state")
	}
}

func TestSafeError(t *testing.T) {
	for _, business := range []error{ErrInvalid, ErrLimited, session.ErrUnauthorized, session.ErrUnavailable} {
		if got := SafeError(fmt.Errorf("internal detail: %w", business)); got != business {
			t.Fatalf("lost business error: %v", got)
		}
	}
	if SafeError(nil) != nil || SafeError(errors.New("private SQL password")) != ErrUnavailable {
		t.Fatal("unexpected safe error mapping")
	}
}
