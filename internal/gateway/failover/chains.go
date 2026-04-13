package failover

import "strings"

// Preset failover chains for Pro tier users
// Based on best cost/performance ratios and reliability
//
// Strategy:
// - Primary: Best cost/performance for the model class
// - First fallback: Different provider, similar capability
// - Second fallback: Budget option, still good quality
//
// Models verified as of Feb 2026:
// - OpenAI: gpt-4o, gpt-4o-mini, gpt-4-turbo, gpt-3.5-turbo
// - Anthropic: claude-sonnet-4-6, claude-opus-4-6, claude-haiku-4-5-20251001,
//              claude-sonnet-4-5-20250929, claude-opus-4-5-20251101, claude-3-haiku-20240307
// - Google: gemini-2.5-flash, gemini-2.5-pro, gemini-2.0-flash

// DefaultFailoverChains defines preset failover chains for different model classes
var DefaultFailoverChains = map[string]FailoverChain{
	// ── OpenAI flagship ──────────────────────────────────────────────────────────
	"gpt-4o": {
		Steps: []FailoverStep{
			{Provider: "openai", Model: "gpt-4o", ProviderName: "OpenAI"},
			{Provider: "anthropic", Model: "claude-sonnet-4-6", ProviderName: "Anthropic"},
			{Provider: "google", Model: "gemini-2.5-pro", ProviderName: "Google"},
		},
	},
	"gpt-4-turbo": {
		Steps: []FailoverStep{
			{Provider: "openai", Model: "gpt-4-turbo", ProviderName: "OpenAI"},
			{Provider: "anthropic", Model: "claude-sonnet-4-6", ProviderName: "Anthropic"},
			{Provider: "google", Model: "gemini-2.5-pro", ProviderName: "Google"},
		},
	},

	// ── OpenAI cost-optimized ─────────────────────────────────────────────────
	"gpt-4o-mini": {
		Steps: []FailoverStep{
			{Provider: "openai", Model: "gpt-4o-mini", ProviderName: "OpenAI"},
			{Provider: "anthropic", Model: "claude-haiku-4-5-20251001", ProviderName: "Anthropic"},
			{Provider: "google", Model: "gemini-2.5-flash", ProviderName: "Google"},
		},
	},
	"gpt-3.5-turbo": {
		Steps: []FailoverStep{
			{Provider: "openai", Model: "gpt-3.5-turbo", ProviderName: "OpenAI"},
			{Provider: "anthropic", Model: "claude-3-haiku-20240307", ProviderName: "Anthropic"},
			{Provider: "google", Model: "gemini-2.0-flash", ProviderName: "Google"},
		},
	},

	// ── Anthropic Claude 4.6 (latest) ────────────────────────────────────────
	"claude-sonnet-4-6": {
		Steps: []FailoverStep{
			{Provider: "anthropic", Model: "claude-sonnet-4-6", ProviderName: "Anthropic"},
			{Provider: "openai", Model: "gpt-4o", ProviderName: "OpenAI"},
			{Provider: "google", Model: "gemini-2.5-pro", ProviderName: "Google"},
		},
	},
	"claude-opus-4-6": {
		Steps: []FailoverStep{
			{Provider: "anthropic", Model: "claude-opus-4-6", ProviderName: "Anthropic"},
			{Provider: "openai", Model: "gpt-4o", ProviderName: "OpenAI"},
			{Provider: "google", Model: "gemini-2.5-pro", ProviderName: "Google"},
		},
	},

	// ── Anthropic Claude 4.5 ─────────────────────────────────────────────────
	"claude-opus-4-5-20251101": {
		Steps: []FailoverStep{
			{Provider: "anthropic", Model: "claude-opus-4-5-20251101", ProviderName: "Anthropic"},
			{Provider: "openai", Model: "gpt-4o", ProviderName: "OpenAI"},
			{Provider: "google", Model: "gemini-2.5-pro", ProviderName: "Google"},
		},
	},
	"claude-sonnet-4-5-20250929": {
		Steps: []FailoverStep{
			{Provider: "anthropic", Model: "claude-sonnet-4-5-20250929", ProviderName: "Anthropic"},
			{Provider: "openai", Model: "gpt-4o", ProviderName: "OpenAI"},
			{Provider: "google", Model: "gemini-2.5-pro", ProviderName: "Google"},
		},
	},
	"claude-haiku-4-5-20251001": {
		Steps: []FailoverStep{
			{Provider: "anthropic", Model: "claude-haiku-4-5-20251001", ProviderName: "Anthropic"},
			{Provider: "openai", Model: "gpt-4o-mini", ProviderName: "OpenAI"},
			{Provider: "google", Model: "gemini-2.5-flash", ProviderName: "Google"},
		},
	},

	// ── Anthropic Claude 4 ───────────────────────────────────────────────────
	"claude-sonnet-4-20250514": {
		Steps: []FailoverStep{
			{Provider: "anthropic", Model: "claude-sonnet-4-20250514", ProviderName: "Anthropic"},
			{Provider: "openai", Model: "gpt-4o", ProviderName: "OpenAI"},
			{Provider: "google", Model: "gemini-2.5-pro", ProviderName: "Google"},
		},
	},
	"claude-opus-4-20250514": {
		Steps: []FailoverStep{
			{Provider: "anthropic", Model: "claude-opus-4-20250514", ProviderName: "Anthropic"},
			{Provider: "openai", Model: "gpt-4o", ProviderName: "OpenAI"},
			{Provider: "google", Model: "gemini-2.5-pro", ProviderName: "Google"},
		},
	},

	// ── Anthropic Claude 3 ───────────────────────────────────────────────────
	"claude-3-haiku-20240307": {
		Steps: []FailoverStep{
			{Provider: "anthropic", Model: "claude-3-haiku-20240307", ProviderName: "Anthropic"},
			{Provider: "openai", Model: "gpt-4o-mini", ProviderName: "OpenAI"},
			{Provider: "google", Model: "gemini-2.0-flash", ProviderName: "Google"},
		},
	},

	// ── Google Gemini ─────────────────────────────────────────────────────────
	"gemini-2.5-pro": {
		Steps: []FailoverStep{
			{Provider: "google", Model: "gemini-2.5-pro", ProviderName: "Google"},
			{Provider: "openai", Model: "gpt-4o", ProviderName: "OpenAI"},
			{Provider: "anthropic", Model: "claude-sonnet-4-6", ProviderName: "Anthropic"},
		},
	},
	"gemini-2.5-flash": {
		Steps: []FailoverStep{
			{Provider: "google", Model: "gemini-2.5-flash", ProviderName: "Google"},
			{Provider: "openai", Model: "gpt-4o-mini", ProviderName: "OpenAI"},
			{Provider: "anthropic", Model: "claude-haiku-4-5-20251001", ProviderName: "Anthropic"},
		},
	},
	"gemini-2.0-flash": {
		Steps: []FailoverStep{
			{Provider: "google", Model: "gemini-2.0-flash", ProviderName: "Google"},
			{Provider: "openai", Model: "gpt-4o-mini", ProviderName: "OpenAI"},
			{Provider: "anthropic", Model: "claude-3-haiku-20240307", ProviderName: "Anthropic"},
		},
	},
	"gemini-2.0-flash-lite": {
		Steps: []FailoverStep{
			{Provider: "google", Model: "gemini-2.0-flash-lite", ProviderName: "Google"},
			{Provider: "openai", Model: "gpt-3.5-turbo", ProviderName: "OpenAI"},
			{Provider: "anthropic", Model: "claude-3-haiku-20240307", ProviderName: "Anthropic"},
		},
	},
}

// GetFailoverChain returns the failover chain for a given model
// Returns nil if no failover chain is defined (Free tier / unsupported model)
func GetFailoverChain(model string) *FailoverChain {
	// Try exact match first
	if chain, ok := DefaultFailoverChains[model]; ok {
		return &chain
	}

	// Try prefix match for versioned models (e.g., "gpt-4o-2024-05-13" -> "gpt-4o")
	for baseModel, chain := range DefaultFailoverChains {
		if strings.HasPrefix(model, baseModel) {
			return &chain
		}
	}

	return nil
}

// HasFailoverChain checks if a model has a defined failover chain
func HasFailoverChain(model string) bool {
	return GetFailoverChain(model) != nil
}
