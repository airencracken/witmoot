// SPDX-License-Identifier: AGPL-3.0-or-later
package forum

import "database/sql"

// AnalyticsDB lets the shared maintenance handler prune the activity table.
// Witmoot records no per-member or per-item activity, so there is nothing to
// collect; owners see aggregate counts from Metrics instead.
func (s *Store) AnalyticsDB() *sql.DB { return s.db }
