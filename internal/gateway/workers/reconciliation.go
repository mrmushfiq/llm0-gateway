package workers

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// reconcileCustomerSpend compares Redis counters with PostgreSQL records
func (s *Scheduler) reconcileCustomerSpend(ctx context.Context) error {
	// Get yesterday's date (keys to reconcile)
	yesterday := time.Now().UTC().AddDate(0, 0, -1)
	dateStr := yesterday.Format("2006-01-02")

	fmt.Printf("🔍 Reconciling customer spend for %s\n", dateStr)

	// 1. Get all Redis keys for this date
	pattern := fmt.Sprintf("customer_spend:*:*:%s", dateStr)
	keys, err := s.redis.Keys(ctx, pattern)
	if err != nil {
		return fmt.Errorf("failed to get Redis keys: %w", err)
	}

	if len(keys) == 0 {
		fmt.Printf("ℹ️  No customer spend keys found for %s (might be a new deployment)\n", dateStr)
		return nil
	}

	fmt.Printf("📊 Found %d customer spend keys in Redis\n", len(keys))

	discrepancies := 0
	redisMissing := 0
	postgresMissing := 0
	totalChecked := 0

	// 2. For each Redis key, check if PostgreSQL record exists
	for _, key := range keys {
		// Parse key: customer_spend:{project_id}:{customer_id}:{date}
		parts := strings.Split(key, ":")
		if len(parts) != 4 {
			fmt.Printf("⚠️  Invalid key format: %s\n", key)
			continue
		}

		projectID, err := uuid.Parse(parts[1])
		if err != nil {
			fmt.Printf("⚠️  Invalid project_id in key %s: %v\n", key, err)
			continue
		}

		customerID := parts[2]
		totalChecked++

		// Get Redis value
		valueStr, err := s.redis.Get(ctx, key)
		if err != nil {
			redisMissing++
			fmt.Printf("⚠️  Failed to get Redis value for %s: %v\n", key, err)
			continue
		}

		// Parse as float
		redisSpend, err := strconv.ParseFloat(valueStr, 64)
		if err != nil {
			redisMissing++
			fmt.Printf("⚠️  Failed to parse Redis value for %s: %v\n", key, err)
			continue
		}

		// Get PostgreSQL value
		pgSpend, err := s.db.GetCustomerDailySpend(ctx, projectID, customerID, yesterday)
		if err != nil {
			postgresMissing++
			fmt.Printf("⚠️  Failed to get PostgreSQL value for customer %s: %v\n", customerID, err)
			continue
		}

		// Compare (allow small floating point differences)
		diff := math.Abs(redisSpend - pgSpend)
		if diff > 0.000001 { // 1 millionth of a cent
			discrepancies++
			fmt.Printf("⚠️  Discrepancy: customer=%s project=%s | Redis: $%.6f | PostgreSQL: $%.6f | Diff: $%.6f\n",
				customerID, projectID, redisSpend, pgSpend, diff)
		}
	}

	// 3. Calculate data loss rate
	dataLossRate := 0.0
	if totalChecked > 0 {
		dataLossRate = float64(discrepancies) / float64(totalChecked) * 100
	}

	// 4. Log results
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("Reconciliation Results - %s\n", dateStr)
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("Total customers checked:    %d\n", totalChecked)
	fmt.Printf("Redis keys found:           %d\n", len(keys))
	fmt.Printf("Discrepancies found:        %d (%.2f%%)\n", discrepancies, dataLossRate)
	fmt.Printf("Redis read errors:          %d\n", redisMissing)
	fmt.Printf("PostgreSQL read errors:     %d\n", postgresMissing)
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	// 5. Alert if data loss is high
	if dataLossRate > 1.0 {
		fmt.Printf("🚨 HIGH DATA LOSS RATE: %.2f%% (threshold: 1.0%%)\n", dataLossRate)
		fmt.Println("🚨 ACTION REQUIRED: Check PostgreSQL connection and async write errors")
	} else if dataLossRate > 0.1 {
		fmt.Printf("⚠️  Elevated data loss rate: %.2f%% (threshold: 0.1%%)\n", dataLossRate)
	} else {
		fmt.Printf("✅ Data loss rate acceptable: %.2f%%\n", dataLossRate)
	}

	// 6. Insert reconciliation log into database
	query := `
		INSERT INTO system_logs (event_type, message, metadata, created_at)
		VALUES ($1, $2, $3, NOW())
	`
	metadata := fmt.Sprintf(`{
		"date": "%s",
		"total_checked": %d,
		"discrepancies": %d,
		"data_loss_rate": %.4f,
		"redis_errors": %d,
		"postgres_errors": %d
	}`, dateStr, totalChecked, discrepancies, dataLossRate, redisMissing, postgresMissing)

	_, err = s.db.ExecContext(ctx, query,
		"customer_spend_reconciliation",
		fmt.Sprintf("Reconciled %d customer spend records (%.2f%% data loss)", totalChecked, dataLossRate),
		metadata,
	)
	if err != nil {
		fmt.Printf("⚠️  Failed to log reconciliation to database: %v\n", err)
	}

	return nil
}
