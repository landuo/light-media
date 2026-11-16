package app

import (
	"context"
	"database/sql"
)

// ValidateAssembledSQL prepares the statements that are built by concatenating
// constants. The literal scanner cannot see them whole, so the schema split is
// verified here instead of at the first request.
func ValidateAssembledSQL(ctx context.Context, db *sql.DB) []error {
	queries := []string{
		transferHistoryCTE + transferHistorySelect,
		`SELECT ` + jobSummaryColumns + ` FROM (SELECT ? AS id,? AS kind,? AS payload) j` + jobSummaryJoins,
		liveOrganizeGroups,
		`SELECT 1 FROM organize_queue_member m WHERE ` + activeOrganizeGroup,
		matchingOrganizeGroups,
		notificationListSQL,
		notificationUnreadSQL,
		originalCurrentSubscriptionAliases,
	}
	var errs []error
	for _, query := range queries {
		stmt, err := db.PrepareContext(ctx, query)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		stmt.Close()
	}
	return errs
}
