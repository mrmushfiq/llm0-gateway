package models

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ============================================================================
// Customer Rate Limiting Models
// ============================================================================

// LimitBehavior defines what happens when a customer hits their limit
type LimitBehavior string

const (
	LimitBehaviorBlock     LimitBehavior = "block"     // Return 429 error
	LimitBehaviorQueue     LimitBehavior = "queue"     // Delay request (not implemented yet)
	LimitBehaviorDowngrade LimitBehavior = "downgrade" // Use cheaper model
	LimitBehaviorWarn      LimitBehavior = "warn"      // Allow but warn
)

// ModelLimits represents per-model request limits
// Example: {"gpt-4": 100, "gpt-4o-mini": null, "claude-3-5-sonnet": 50}
// null = unlimited, number = max requests per day
type ModelLimits map[string]*int

// Value implements driver.Valuer for database storage
func (ml ModelLimits) Value() (driver.Value, error) {
	if ml == nil {
		return nil, nil
	}
	return json.Marshal(ml)
}

// Scan implements sql.Scanner for database retrieval
func (ml *ModelLimits) Scan(value interface{}) error {
	if value == nil {
		*ml = nil
		return nil
	}

	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("failed to unmarshal ModelLimits value")
	}

	return json.Unmarshal(bytes, ml)
}

// LabelLimits represents per-label request limits
// Example: {"feature:chat": 1000, "feature:summarization": 100, "team:support": 500}
type LabelLimits map[string]int

// Value implements driver.Valuer for database storage
func (ll LabelLimits) Value() (driver.Value, error) {
	if ll == nil {
		return nil, nil
	}
	return json.Marshal(ll)
}

// Scan implements sql.Scanner for database retrieval
func (ll *LabelLimits) Scan(value interface{}) error {
	if value == nil {
		*ll = nil
		return nil
	}

	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("failed to unmarshal LabelLimits value")
	}

	return json.Unmarshal(bytes, ll)
}

// CustomerLimit defines rate limit configuration for an end-user
type CustomerLimit struct {
	ID         uuid.UUID `db:"id"`
	ProjectID  uuid.UUID `db:"project_id"`
	CustomerID string    `db:"customer_id"`

	// Cost-based limits
	DailySpendLimitUSD   *float64 `db:"daily_spend_limit_usd"`
	MonthlySpendLimitUSD *float64 `db:"monthly_spend_limit_usd"`
	PerRequestMaxUSD     *float64 `db:"per_request_max_usd"`

	// Request-based limits
	RequestsPerMinute *int `db:"requests_per_minute"`
	RequestsPerHour   *int `db:"requests_per_hour"`
	RequestsPerDay    *int `db:"requests_per_day"`

	// Advanced limits (JSONB)
	ModelLimits ModelLimits `db:"model_limits"`
	LabelLimits LabelLimits `db:"label_limits"`

	// Behavior on limit
	OnLimitBehavior LimitBehavior `db:"on_limit_behavior"`
	DowngradeModel  *string       `db:"downgrade_model"`

	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// HasCostLimit returns true if any cost-based limit is configured
func (cl *CustomerLimit) HasCostLimit() bool {
	return cl.DailySpendLimitUSD != nil ||
		cl.MonthlySpendLimitUSD != nil ||
		cl.PerRequestMaxUSD != nil
}

// HasRequestLimit returns true if any request-based limit is configured
func (cl *CustomerLimit) HasRequestLimit() bool {
	return cl.RequestsPerMinute != nil ||
		cl.RequestsPerHour != nil ||
		cl.RequestsPerDay != nil
}

// HasModelLimit returns true if a limit exists for the specified model
func (cl *CustomerLimit) HasModelLimit(model string) bool {
	if cl.ModelLimits == nil {
		return false
	}
	_, exists := cl.ModelLimits[model]
	return exists
}

// GetModelLimit returns the request limit for a specific model
// Returns (limit, hasLimit)
func (cl *CustomerLimit) GetModelLimit(model string) (int, bool) {
	if cl.ModelLimits == nil {
		return 0, false
	}
	limit, exists := cl.ModelLimits[model]
	if !exists || limit == nil {
		return 0, false
	}
	return *limit, true
}

// HasLabelLimit returns true if a limit exists for the specified label
func (cl *CustomerLimit) HasLabelLimit(labelKey string) bool {
	if cl.LabelLimits == nil {
		return false
	}
	_, exists := cl.LabelLimits[labelKey]
	return exists
}

// GetLabelLimit returns the request limit for a specific label
func (cl *CustomerLimit) GetLabelLimit(labelKey string) (int, bool) {
	if cl.LabelLimits == nil {
		return 0, false
	}
	limit, exists := cl.LabelLimits[labelKey]
	return limit, exists
}

// ============================================================================
// Customer Spend Tracking Models
// ============================================================================

// ModelSpendBreakdown represents spend per model
// Example: {"gpt-4": {"spend": 1.234, "requests": 10}, "gpt-4o-mini": {"spend": 0.005, "requests": 50}}
type ModelSpendBreakdown map[string]struct {
	Spend    float64 `json:"spend"`
	Requests int     `json:"requests"`
}

// Value implements driver.Valuer for database storage
func (msb ModelSpendBreakdown) Value() (driver.Value, error) {
	if msb == nil {
		return nil, nil
	}
	return json.Marshal(msb)
}

// Scan implements sql.Scanner for database retrieval
func (msb *ModelSpendBreakdown) Scan(value interface{}) error {
	if value == nil {
		*msb = nil
		return nil
	}

	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("failed to unmarshal ModelSpendBreakdown value")
	}

	return json.Unmarshal(bytes, msb)
}

// LabelSpendBreakdown represents spend per label
// Example: {"feature:chat": {"spend": 0.5, "requests": 20}, "team:support": {"spend": 0.3, "requests": 10}}
type LabelSpendBreakdown map[string]struct {
	Spend    float64 `json:"spend"`
	Requests int     `json:"requests"`
}

// Value implements driver.Valuer for database storage
func (lsb LabelSpendBreakdown) Value() (driver.Value, error) {
	if lsb == nil {
		return nil, nil
	}
	return json.Marshal(lsb)
}

// Scan implements sql.Scanner for database retrieval
func (lsb *LabelSpendBreakdown) Scan(value interface{}) error {
	if value == nil {
		*lsb = nil
		return nil
	}

	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("failed to unmarshal LabelSpendBreakdown value")
	}

	return json.Unmarshal(bytes, lsb)
}

// CustomerSpend tracks actual usage per customer per time window
type CustomerSpend struct {
	ID         uuid.UUID `db:"id"`
	ProjectID  uuid.UUID `db:"project_id"`
	CustomerID string    `db:"customer_id"`

	// Time window
	Date time.Time `db:"date"` // YYYY-MM-DD
	Hour *int      `db:"hour"` // 0-23 for hourly, null for daily

	// Aggregate metrics
	TotalSpendUSD float64 `db:"total_spend_usd"`
	RequestCount  int     `db:"request_count"`

	// Breakdowns (JSONB)
	SpendByModel ModelSpendBreakdown `db:"spend_by_model"`
	SpendByLabel LabelSpendBreakdown `db:"spend_by_label"`

	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// IsHourly returns true if this is an hourly record (not a daily aggregate)
func (cs *CustomerSpend) IsHourly() bool {
	return cs.Hour != nil
}

// Labels represents custom attribution labels from headers
// Example: {"feature": "chat", "team": "support", "client": "agency_A"}
type Labels map[string]string

// Value implements driver.Valuer for database storage
func (l Labels) Value() (driver.Value, error) {
	if l == nil {
		return nil, nil
	}
	return json.Marshal(l)
}

// Scan implements sql.Scanner for database retrieval
func (l *Labels) Scan(value interface{}) error {
	if value == nil {
		*l = nil
		return nil
	}

	bytes, ok := value.([]byte)
	if !ok {
		return errors.New("failed to unmarshal Labels value")
	}

	return json.Unmarshal(bytes, l)
}

// ToLabelKeys converts Labels to label keys for limit lookups
// Example: {"feature": "chat", "team": "support"} → ["feature:chat", "team:support"]
func (l Labels) ToLabelKeys() []string {
	if l == nil {
		return nil
	}

	keys := make([]string, 0, len(l))
	for k, v := range l {
		keys = append(keys, k+":"+v)
	}
	return keys
}
