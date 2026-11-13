// SPDX-License-Identifier: AGPL-3.0-or-later
package forum

import (
	"context"
	"github.com/airencracken/comfylib/admin"
	"github.com/airencracken/comfylib/analytics"
	"time"
)

// Metrics returns aggregate instance totals from one bounded read snapshot.
// Authorization is performed by the owner/admin HTTP handler before this call.
func (s *Store) Metrics(ctx context.Context, now time.Time) ([]admin.Metric, error) {
	return admin.Collect(ctx, s.db, []admin.Query{
		{Label: "Members", SQL: "SELECT count(*) FROM users WHERE suspended=0", Detail: "", Bytes: false},
		{Label: "Boards", SQL: "SELECT count(*) FROM boards", Detail: "", Bytes: false},
		{Label: "Conversations", SQL: "SELECT count(*) FROM topics", Detail: "", Bytes: false},
		{Label: "Posts", SQL: "SELECT count(*) FROM posts WHERE removed=0", Detail: "", Bytes: false},
		{Label: "New posts", SQL: "SELECT count(*) FROM posts WHERE removed=0 AND created_at>=? AND created_at<=?", Detail: "Past 7 UTC days", Bytes: false, Args: []any{analytics.WindowStart(now).Unix(), now.Unix()}},
		{Label: "Database size", SQL: "SELECT (SELECT page_count FROM pragma_page_count)*(SELECT page_size FROM pragma_page_size)", Bytes: true, Detail: "Main database allocation; temporary logs, cached media and backups are separate."},
	})
}
