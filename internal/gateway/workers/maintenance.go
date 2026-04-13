package workers

import (
	"context"
	"fmt"
)

// cleanupOldLogs deletes logs older than retention period
func (s *Scheduler) cleanupOldLogs(ctx context.Context) error {
	// Delete logs based on project tier
	// Free tier: 7 days, Pro: 30 days, Startup: 90 days
	query := `
		WITH deleted AS (
			DELETE FROM gateway_logs gl
			USING api_keys ak, projects p
			WHERE gl.api_key_id = ak.id
			  AND ak.project_id = p.id
			  AND (
				  -- Free tier: 7 days (default if tier is NULL)
				  (COALESCE(p.tier, 'free') = 'free' AND gl.created_at < NOW() - INTERVAL '7 days')
				  -- Pro tier: 30 days
				  OR (p.tier = 'pro' AND gl.created_at < NOW() - INTERVAL '30 days')
				  -- Startup tier: 90 days
				  OR (p.tier = 'startup' AND gl.created_at < NOW() - INTERVAL '90 days')
			  )
			RETURNING gl.id
		)
		SELECT COUNT(*) FROM deleted
	`

	var deletedCount int
	err := s.db.QueryRowContext(ctx, query).Scan(&deletedCount)
	if err != nil {
		return fmt.Errorf("failed to cleanup old logs: %w", err)
	}

	if deletedCount > 0 {
		fmt.Printf("🗑️  Deleted %d old gateway_logs entries\n", deletedCount)
	} else {
		fmt.Println("✨ No old logs to delete")
	}

	// Log the cleanup
	logQuery := `
		INSERT INTO system_logs (event_type, message, metadata, created_at)
		VALUES ($1, $2, $3, NOW())
	`
	metadata := fmt.Sprintf(`{"deleted_count": %d}`, deletedCount)
	_, _ = s.db.ExecContext(ctx, logQuery,
		"log_cleanup",
		fmt.Sprintf("Cleaned up %d old gateway_logs entries", deletedCount),
		metadata,
	)

	return nil
}

// resetMonthlySpend resets monthly spend for all projects
func (s *Scheduler) resetMonthlySpend(ctx context.Context) error {
	query := `
		WITH updated AS (
			UPDATE projects
			SET 
				current_month_spend_usd = 0,
				spend_reset_at = date_trunc('month', NOW() + interval '1 month')
			WHERE spend_reset_at <= NOW()
			RETURNING id, name, current_month_spend_usd
		)
		SELECT 
			COUNT(*) as project_count,
			COALESCE(SUM(current_month_spend_usd), 0) as total_spend_before
		FROM updated
	`

	var projectCount int
	var totalSpendBefore float64
	err := s.db.QueryRowContext(ctx, query).Scan(&projectCount, &totalSpendBefore)
	if err != nil {
		return fmt.Errorf("failed to reset monthly spend: %w", err)
	}

	if projectCount > 0 {
		fmt.Printf("💰 Reset monthly spend for %d projects (total was $%.2f)\n", projectCount, totalSpendBefore)
	} else {
		fmt.Println("ℹ️  No projects needed monthly spend reset")
	}

	// Log the reset
	logQuery := `
		INSERT INTO system_logs (event_type, message, metadata, created_at)
		VALUES ($1, $2, $3, NOW())
	`
	metadata := fmt.Sprintf(`{
		"projects_reset": %d,
		"total_spend_before": %.6f,
		"reset_date": "%s"
	}`, projectCount, totalSpendBefore, ctx.Value("now"))

	_, _ = s.db.ExecContext(ctx, logQuery,
		"monthly_spend_reset",
		fmt.Sprintf("Reset monthly spend for %d projects (total: $%.2f)", projectCount, totalSpendBefore),
		metadata,
	)

	return nil
}
