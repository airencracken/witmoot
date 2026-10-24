// SPDX-License-Identifier: AGPL-3.0-or-later
package forum

import (
	"database/sql"
	"testing"
	"time"
)

func TestInvitationStatusAdapter(t *testing.T) {
	future := time.Now().Add(time.Hour).Unix()
	for _, tc := range []struct {
		invite Invitation
		want   string
	}{
		{Invitation{Uses: 10}, "open"},
		{Invitation{ExpiresAt: sql.NullInt64{Int64: 1, Valid: false}}, "open"},
		{Invitation{ExpiresAt: sql.NullInt64{Int64: future, Valid: true}}, "open"},
		{Invitation{ExpiresAt: sql.NullInt64{Int64: 0, Valid: true}}, "expired"},
		{Invitation{MaxUses: 1, Uses: 1}, "used up"},
		{Invitation{RevokedAt: sql.NullInt64{Valid: true}, ExpiresAt: sql.NullInt64{Valid: true}, MaxUses: 1, Uses: 1}, "revoked"},
	} {
		if got := tc.invite.Status(); got != tc.want {
			t.Fatalf("%+v: got %q, want %q", tc.invite, got, tc.want)
		}
	}
}
