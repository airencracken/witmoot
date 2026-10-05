package forum

import (
	"fmt"
	"math"
	"net/http/httptest"
	"testing"
)

func TestHTTPSBaseURLForcesHostCookies(t *testing.T) {
	app, err := New(testStore(t), Config{BaseURL: "https://board.example.org", SecureCookies: false})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest("GET", "/login", nil))
	cookies := response.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no CSRF cookie")
	}
	for _, cookie := range cookies {
		if !cookie.Secure || cookie.Domain != "" || cookie.Path != "/" || cookie.Name != "__Host-witmoot_csrf" {
			t.Fatalf("HTTPS origin has weak cookie: %+v", cookie)
		}
	}
}

func TestDeletedAccountSchemaAndIdentifierBoundaries(t *testing.T) {
	for _, id := range []int64{2, 42, math.MaxInt64} {
		t.Run(fmt.Sprint(id), func(t *testing.T) {
			store := testStore(t)
			if err := store.CreateOwner(t.Context(), "keeper", "owner hash"); err != nil {
				t.Fatal(err)
			}
			if _, err := store.db.Exec("INSERT INTO users(id,username,password_hash,created_at,email) VALUES(?,'leaving','verified hash',1,'private@example.org')", id); err != nil {
				t.Fatal(err)
			}
			for _, query := range []string{"UPDATE users SET deleted=1 WHERE id=?", "UPDATE users SET deleted=2 WHERE id=?"} {
				if _, err := store.db.Exec(query, id); err == nil {
					t.Fatal("schema accepts credential-bearing deleted account")
				}
			}
			if err := store.DeleteAccount(t.Context(), id, "leaving", "verified hash"); err != nil {
				t.Fatal(err)
			}
			for _, assignment := range []string{"email='private@example.org'", "password_hash='credential'", "suspended=0", "role='owner'", "can_invite=1"} {
				if _, err := store.db.Exec("UPDATE users SET "+assignment+" WHERE id=?", id); err == nil {
					t.Fatalf("deleted account reactivated via %s", assignment)
				}
			}
		})
	}
}
