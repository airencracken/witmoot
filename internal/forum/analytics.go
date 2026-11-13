// SPDX-License-Identifier: AGPL-3.0-or-later
package forum

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/airencracken/comfylib/analytics"
)

// AnalyticsDB is the database the shared site health counters live in.
func (s *Store) AnalyticsDB() *sql.DB { return s.db }

// Activity is the seven-day site health and security report for the owner
// dashboard. It holds counts only: no accounts, content, or addresses.
func (s *Store) Activity(ctx context.Context, now time.Time) (analytics.Summary, error) {
	return analytics.Collect(ctx, s.db, "", now)
}

// Flush writes request counts that are still in memory. Call it on shutdown.
func (a *App) Flush(ctx context.Context) error { return a.monitor.Flush(ctx) }

// countSecurity adds one to a security counter. A failure is logged and never
// changes the response.
func (a *App) countSecurity(r *http.Request, record func(context.Context, analytics.Executor, time.Time) error) {
	if err := record(r.Context(), a.store.db, time.Now()); err != nil {
		slog.Warn("count security event", "error", err)
	}
}
