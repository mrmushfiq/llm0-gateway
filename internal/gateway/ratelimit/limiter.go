package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mrmushfiq/llm0-gateway/internal/shared/database"
	"github.com/mrmushfiq/llm0-gateway/internal/shared/models"
	redisClient "github.com/mrmushfiq/llm0-gateway/internal/shared/redis"
)

// ============================================================================
// Customer Rate Limiter
// ============================================================================

// Limiter handles customer-level rate limiting checks
type Limiter struct {
	redis *redisClient.Client
	db    *database.DB
}

// NewLimiter creates a new customer rate limiter
func NewLimiter(redis *redisClient.Client, db *database.DB) *Limiter {
	return &Limiter{
		redis: redis,
		db:    db,
	}
}

// CheckRequest contains all information needed for a rate limit check
type CheckRequest struct {
	ProjectID  uuid.UUID
	CustomerID string
	Model      string
	CostUSD    float64
	Labels     models.Labels // Custom attribution labels
}

// CheckResult contains the outcome of a rate limit check
type CheckResult struct {
	Allowed bool
	Reason  string // Why it was blocked (if not allowed)

	// Spend limits
	DailySpend        float64
	DailySpendLimit   *float64
	MonthlySpend      float64
	MonthlySpendLimit *float64

	// Request limits
	MinuteRequests      int
	MinuteRequestsLimit *int
	HourRequests        int
	HourRequestsLimit   *int
	DailyRequests       int
	DailyRequestsLimit  *int

	// Degradation (if applicable)
	ShouldDegrade  bool
	DowngradeModel *string

	// Headers to include in response
	Headers map[string]string
}

// Check performs a comprehensive rate limit check for a customer
func (l *Limiter) Check(ctx context.Context, req *CheckRequest) (*CheckResult, error) {
	// Get customer limit configuration
	limit, err := l.db.GetCustomerLimit(ctx, req.ProjectID, req.CustomerID)
	if err != nil {
		return nil, fmt.Errorf("failed to get customer limit: %w", err)
	}

	// If no limit configured, allow the request
	if limit == nil {
		return &CheckResult{
			Allowed: true,
			Headers: make(map[string]string),
		}, nil
	}

	result := &CheckResult{
		Allowed:           true,
		DailySpendLimit:   limit.DailySpendLimitUSD,
		MonthlySpendLimit: limit.MonthlySpendLimitUSD,
		Headers:           make(map[string]string),
	}

	// 1. Check cost-based limits
	if limit.HasCostLimit() {
		blocked, reason, err := l.checkCostLimits(ctx, req, limit, result)
		if err != nil {
			return nil, err
		}
		if blocked {
			result.Allowed = false
			result.Reason = reason
			return result, nil
		}
	}

	// 2. Check request-based limits
	if limit.HasRequestLimit() {
		blocked, reason, err := l.checkRequestLimits(ctx, req, limit, result)
		if err != nil {
			return nil, err
		}
		if blocked {
			result.Allowed = false
			result.Reason = reason
			return result, nil
		}
	}

	// 3. Check model-specific limits
	if limit.HasModelLimit(req.Model) {
		blocked, reason, err := l.checkModelLimits(ctx, req, limit, result)
		if err != nil {
			return nil, err
		}
		if blocked {
			result.Allowed = false
			result.Reason = reason
			return result, nil
		}
	}

	// 4. Check label-based limits
	if len(req.Labels) > 0 {
		blocked, reason, err := l.checkLabelLimits(ctx, req, limit, result)
		if err != nil {
			return nil, err
		}
		if blocked {
			result.Allowed = false
			result.Reason = reason
			return result, nil
		}
	}

	// 5. Check per-request max cost
	if limit.PerRequestMaxUSD != nil && req.CostUSD > *limit.PerRequestMaxUSD {
		result.Allowed = false
		result.Reason = fmt.Sprintf("request cost ($%.4f) exceeds per-request limit ($%.2f)",
			req.CostUSD, *limit.PerRequestMaxUSD)
		return result, nil
	}

	// All checks passed
	return result, nil
}

// checkCostLimits verifies spend limits
func (l *Limiter) checkCostLimits(
	ctx context.Context,
	req *CheckRequest,
	limit *models.CustomerLimit,
	result *CheckResult,
) (bool, string, error) {
	now := time.Now()

	// Get current spend from Redis
	dailyWindow := fmt.Sprintf("daily:%s", now.Format("2006-01-02"))
	dailySpend, err := l.redis.GetCustomerSpend(ctx,
		req.ProjectID.String(), req.CustomerID, dailyWindow)
	if err != nil {
		return false, "", fmt.Errorf("failed to get daily spend: %w", err)
	}
	result.DailySpend = dailySpend

	monthlyWindow := fmt.Sprintf("monthly:%s", now.Format("2006-01"))
	monthlySpend, err := l.redis.GetCustomerSpend(ctx,
		req.ProjectID.String(), req.CustomerID, monthlyWindow)
	if err != nil {
		return false, "", fmt.Errorf("failed to get monthly spend: %w", err)
	}
	result.MonthlySpend = monthlySpend

	// Check daily limit
	if limit.DailySpendLimitUSD != nil {
		projectedDaily := dailySpend + req.CostUSD
		if projectedDaily > *limit.DailySpendLimitUSD {
			// Check degradation behavior
			if limit.OnLimitBehavior == models.LimitBehaviorDowngrade && limit.DowngradeModel != nil {
				result.ShouldDegrade = true
				result.DowngradeModel = limit.DowngradeModel
				return false, "", nil // Not blocked, but should degrade
			}

			return true, fmt.Sprintf("daily spend limit exceeded (current: $%.4f, limit: $%.2f)",
				dailySpend, *limit.DailySpendLimitUSD), nil
		}

		// Add warning header if approaching limit (80%)
		if dailySpend >= *limit.DailySpendLimitUSD*0.8 {
			pct := (dailySpend / *limit.DailySpendLimitUSD) * 100
			result.Headers["X-Warning"] = fmt.Sprintf("Customer approaching daily spend limit (%.0f%%)", pct)
		}
	}

	// Check monthly limit
	if limit.MonthlySpendLimitUSD != nil {
		projectedMonthly := monthlySpend + req.CostUSD
		if projectedMonthly > *limit.MonthlySpendLimitUSD {
			// Check degradation behavior
			if limit.OnLimitBehavior == models.LimitBehaviorDowngrade && limit.DowngradeModel != nil {
				result.ShouldDegrade = true
				result.DowngradeModel = limit.DowngradeModel
				return false, "", nil // Not blocked, but should degrade
			}

			return true, fmt.Sprintf("monthly spend limit exceeded (current: $%.4f, limit: $%.2f)",
				monthlySpend, *limit.MonthlySpendLimitUSD), nil
		}

		// Add warning header if approaching limit (80%)
		if monthlySpend >= *limit.MonthlySpendLimitUSD*0.8 {
			pct := (monthlySpend / *limit.MonthlySpendLimitUSD) * 100
			result.Headers["X-Warning"] = fmt.Sprintf("Customer approaching monthly spend limit (%.0f%%)", pct)
		}
	}

	return false, "", nil
}

// checkRequestLimits verifies request count limits
func (l *Limiter) checkRequestLimits(
	ctx context.Context,
	req *CheckRequest,
	limit *models.CustomerLimit,
	result *CheckResult,
) (bool, string, error) {
	now := time.Now()
	projectID := req.ProjectID.String()
	customerID := req.CustomerID

	// Check minute limit
	if limit.RequestsPerMinute != nil {
		minuteWindow := fmt.Sprintf("minute:%d", now.Unix()/60)
		count, err := l.redis.GetCustomerRequestCount(ctx, projectID, customerID, minuteWindow)
		if err != nil {
			return false, "", fmt.Errorf("failed to get minute request count: %w", err)
		}
		result.MinuteRequests = count
		result.MinuteRequestsLimit = limit.RequestsPerMinute

		if count >= *limit.RequestsPerMinute {
			return true, fmt.Sprintf("requests per minute limit exceeded (%d/%d)",
				count, *limit.RequestsPerMinute), nil
		}
	}

	// Check hour limit
	if limit.RequestsPerHour != nil {
		hourWindow := fmt.Sprintf("hour:%d", now.Unix()/3600)
		count, err := l.redis.GetCustomerRequestCount(ctx, projectID, customerID, hourWindow)
		if err != nil {
			return false, "", fmt.Errorf("failed to get hour request count: %w", err)
		}
		result.HourRequests = count
		result.HourRequestsLimit = limit.RequestsPerHour

		if count >= *limit.RequestsPerHour {
			return true, fmt.Sprintf("requests per hour limit exceeded (%d/%d)",
				count, *limit.RequestsPerHour), nil
		}
	}

	// Check daily limit
	if limit.RequestsPerDay != nil {
		dailyWindow := fmt.Sprintf("daily:%s", now.Format("2006-01-02"))
		count, err := l.redis.GetCustomerRequestCount(ctx, projectID, customerID, dailyWindow)
		if err != nil {
			return false, "", fmt.Errorf("failed to get daily request count: %w", err)
		}
		result.DailyRequests = count
		result.DailyRequestsLimit = limit.RequestsPerDay

		if count >= *limit.RequestsPerDay {
			return true, fmt.Sprintf("requests per day limit exceeded (%d/%d)",
				count, *limit.RequestsPerDay), nil
		}

		// Add warning if approaching limit
		if count >= int(float64(*limit.RequestsPerDay)*0.8) {
			pct := (float64(count) / float64(*limit.RequestsPerDay)) * 100
			result.Headers["X-Warning"] = fmt.Sprintf("Customer approaching daily request limit (%.0f%%)", pct)
		}
	}

	return false, "", nil
}

// checkModelLimits verifies per-model limits
func (l *Limiter) checkModelLimits(
	ctx context.Context,
	req *CheckRequest,
	limit *models.CustomerLimit,
	result *CheckResult,
) (bool, string, error) {
	modelLimit, hasLimit := limit.GetModelLimit(req.Model)
	if !hasLimit {
		return false, "", nil // No limit for this model
	}

	// Get today's request count for this model from database
	// (We'd need to track this in a separate Redis key per model)
	// For MVP, we'll implement basic daily model tracking

	now := time.Now()
	modelKey := fmt.Sprintf("requests:customer:%s:%s:model:%s:daily:%s",
		req.ProjectID.String(), req.CustomerID, req.Model, now.Format("2006-01-02"))

	count, err := l.redis.Get(ctx, modelKey)
	if err != nil && err.Error() != "redis: nil" {
		return false, "", fmt.Errorf("failed to get model request count: %w", err)
	}

	var currentCount int
	if count != "" {
		fmt.Sscanf(count, "%d", &currentCount)
	}

	if currentCount >= modelLimit {
		return true, fmt.Sprintf("daily limit for model %s exceeded (%d/%d)",
			req.Model, currentCount, modelLimit), nil
	}

	return false, "", nil
}

// checkLabelLimits verifies label-based limits
func (l *Limiter) checkLabelLimits(
	ctx context.Context,
	req *CheckRequest,
	limit *models.CustomerLimit,
	result *CheckResult,
) (bool, string, error) {
	// Check each label against configured limits
	for _, labelKey := range req.Labels.ToLabelKeys() {
		labelLimit, hasLimit := limit.GetLabelLimit(labelKey)
		if !hasLimit {
			continue
		}

		// Get today's request count for this label
		now := time.Now()
		labelRedisKey := fmt.Sprintf("requests:customer:%s:%s:label:%s:daily:%s",
			req.ProjectID.String(), req.CustomerID, labelKey, now.Format("2006-01-02"))

		count, err := l.redis.Get(ctx, labelRedisKey)
		if err != nil && err.Error() != "redis: nil" {
			return false, "", fmt.Errorf("failed to get label request count: %w", err)
		}

		var currentCount int
		if count != "" {
			fmt.Sscanf(count, "%d", &currentCount)
		}

		if currentCount >= labelLimit {
			return true, fmt.Sprintf("daily limit for label %s exceeded (%d/%d)",
				labelKey, currentCount, labelLimit), nil
		}
	}

	return false, "", nil
}

// RecordRequest records a successful request in all tracking systems
func (l *Limiter) RecordRequest(ctx context.Context, req *CheckRequest) error {
	projectID := req.ProjectID.String()
	customerID := req.CustomerID

	// 1. Track spend in Redis (daily + monthly)
	_, _, err := l.redis.TrackCustomerSpend(ctx, projectID, customerID, req.CostUSD)
	if err != nil {
		return fmt.Errorf("failed to track customer spend: %w", err)
	}

	// 2. Track request counts in Redis (minute, hour, daily)
	_, _, _, err = l.redis.TrackCustomerRequests(ctx, projectID, customerID, 1)
	if err != nil {
		return fmt.Errorf("failed to track customer requests: %w", err)
	}

	// 3. Track per-model requests
	now := time.Now()
	modelKey := fmt.Sprintf("requests:customer:%s:%s:model:%s:daily:%s",
		projectID, customerID, req.Model, now.Format("2006-01-02"))
	err = l.redis.Set(ctx, modelKey, 1, 0) // Increment by 1
	if err != nil {
		// Try to increment
		val, _ := l.redis.Get(ctx, modelKey)
		var count int
		if val != "" {
			fmt.Sscanf(val, "%d", &count)
		}
		count++
		err = l.redis.Set(ctx, modelKey, fmt.Sprintf("%d", count), 24*time.Hour)
		if err != nil {
			return fmt.Errorf("failed to track model requests: %w", err)
		}
	}

	// 4. Track per-label requests
	for _, labelKey := range req.Labels.ToLabelKeys() {
		labelRedisKey := fmt.Sprintf("requests:customer:%s:%s:label:%s:daily:%s",
			projectID, customerID, labelKey, now.Format("2006-01-02"))

		val, _ := l.redis.Get(ctx, labelRedisKey)
		var count int
		if val != "" {
			fmt.Sscanf(val, "%d", &count)
		}
		count++
		err = l.redis.Set(ctx, labelRedisKey, fmt.Sprintf("%d", count), 24*time.Hour)
		if err != nil {
			return fmt.Errorf("failed to track label requests: %w", err)
		}
	}

	// 5. Persist to database in background (async for performance)
	go l.persistToDatabase(context.Background(), req)

	return nil
}

// persistToDatabase writes tracking data to PostgreSQL for analytics
func (l *Limiter) persistToDatabase(ctx context.Context, req *CheckRequest) {
	now := time.Now()
	spend := &models.CustomerSpend{
		ProjectID:     req.ProjectID,
		CustomerID:    req.CustomerID,
		Date:          now,
		Hour:          nil, // Daily aggregate
		TotalSpendUSD: req.CostUSD,
		RequestCount:  1,
		SpendByModel: models.ModelSpendBreakdown{
			req.Model: struct {
				Spend    float64 `json:"spend"`
				Requests int     `json:"requests"`
			}{
				Spend:    req.CostUSD,
				Requests: 1,
			},
		},
		SpendByLabel: make(models.LabelSpendBreakdown),
	}

	// Add label breakdowns
	for _, labelKey := range req.Labels.ToLabelKeys() {
		spend.SpendByLabel[labelKey] = struct {
			Spend    float64 `json:"spend"`
			Requests int     `json:"requests"`
		}{
			Spend:    req.CostUSD,
			Requests: 1,
		}
	}

	err := l.db.RecordCustomerSpend(ctx, spend)
	if err != nil {
		fmt.Printf("⚠️ Failed to persist customer spend to database: %v\n", err)
	}
}
