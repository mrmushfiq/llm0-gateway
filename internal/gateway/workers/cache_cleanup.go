package workers

import (
	"context"
	"fmt"
)

// cleanupExpiredCache deletes expired entries from exact_cache
func (s *Scheduler) cleanupExpiredCache(ctx context.Context) error {
	query := `
		WITH deleted AS (
			DELETE FROM exact_cache
			WHERE expires_at < NOW()
			RETURNING id
		)
		SELECT COUNT(*) FROM deleted
	`

	var deletedCount int
	err := s.db.QueryRowContext(ctx, query).Scan(&deletedCount)
	if err != nil {
		return fmt.Errorf("failed to cleanup exact cache: %w", err)
	}

	if deletedCount > 0 {
		fmt.Printf("🗑️  Deleted %d expired exact_cache entries\n", deletedCount)
	} else {
		fmt.Println("✨ No expired exact_cache entries to delete")
	}

	// Log if significant cleanup
	if deletedCount > 100 {
		logQuery := `
			INSERT INTO system_logs (event_type, message, metadata, created_at)
			VALUES ($1, $2, $3, NOW())
		`
		metadata := fmt.Sprintf(`{"deleted_count": %d}`, deletedCount)
		_, _ = s.db.ExecContext(ctx, logQuery,
			"cache_cleanup",
			fmt.Sprintf("Cleaned up %d expired exact_cache entries", deletedCount),
			metadata,
		)
	}

	return nil
}

// cleanupSemanticCache deletes expired entries from semantic_cache
func (s *Scheduler) cleanupSemanticCache(ctx context.Context) error {
	query := `
		WITH deleted AS (
			DELETE FROM semantic_cache
			WHERE created_at + (ttl_seconds || ' seconds')::interval < NOW()
			RETURNING id
		)
		SELECT COUNT(*) FROM deleted
	`

	var deletedCount int
	err := s.db.QueryRowContext(ctx, query).Scan(&deletedCount)
	if err != nil {
		return fmt.Errorf("failed to cleanup semantic cache: %w", err)
	}

	if deletedCount > 0 {
		fmt.Printf("🗑️  Deleted %d expired semantic_cache entries\n", deletedCount)
	} else {
		fmt.Println("✨ No expired semantic_cache entries to delete")
	}

	// Log if significant cleanup
	if deletedCount > 100 {
		logQuery := `
			INSERT INTO system_logs (event_type, message, metadata, created_at)
			VALUES ($1, $2, $3, NOW())
		`
		metadata := fmt.Sprintf(`{"deleted_count": %d}`, deletedCount)
		_, _ = s.db.ExecContext(ctx, logQuery,
			"semantic_cache_cleanup",
			fmt.Sprintf("Cleaned up %d expired semantic_cache entries", deletedCount),
			metadata,
		)
	}

	return nil
}
