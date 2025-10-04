package internal
package internal

import (
	"regexp"
	"strings"
)

// PIIRedactor automatically detects and redacts sensitive personal information
// Supports: SSN, credit cards, emails, phone numbers, IP addresses
type PIIRedactor struct {
	patterns map[string]*regexp.Regexp
	enabled  bool
}

func NewPIIRedactor(enabled bool) *PIIRedactor {
	return &PIIRedactor{
		enabled: enabled,
		patterns: map[string]*regexp.Regexp{
			// US Social Security Number: XXX-XX-XXXX
			"ssn": regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
			
			// Credit Card: 16 digits with optional spaces/dashes
			"credit_card": regexp.MustCompile(`\b\d{4}[\s\-]?\d{4}[\s\-]?\d{4}[\s\-]?\d{4}\b`),
			
			// Email address
			"email": regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Z|a-z]{2,}\b`),
			
			// Phone number: US format
			"phone": regexp.MustCompile(`\b\d{3}[-.\s]?\d{3}[-.\s]?\d{4}\b`),
			
			// IPv4 address
			"ipv4": regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`),
			
			// API keys/tokens (generic pattern: 32-64 hex chars)
			"api_key": regexp.MustCompile(`\b[a-fA-F0-9]{32,64}\b`),
		},
	}
}

// Redact applies PII redaction to input string
func (pr *PIIRedactor) Redact(input string) string {
	if !pr.enabled {
		return input
	}

	result := input

	// Redact SSN
	result = pr.patterns["ssn"].ReplaceAllString(result, "***-**-****")

	// Redact credit cards (show last 4 digits only)
	result = pr.patterns["credit_card"].ReplaceAllStringFunc(result, func(match string) string {
		cleaned := strings.ReplaceAll(strings.ReplaceAll(match, "-", ""), " ", "")
		if len(cleaned) >= 4 {
			return "************" + cleaned[len(cleaned)-4:]
		}
		return "****************"
	})

	// Redact emails (show domain only)
	result = pr.patterns["email"].ReplaceAllStringFunc(result, func(match string) string {
		parts := strings.Split(match, "@")
		if len(parts) == 2 {
			return "***@" + parts[1]
		}
		return "***@***"
	})

	// Redact phone numbers
	result = pr.patterns["phone"].ReplaceAllString(result, "***-***-****")

	// Redact IPv4 (show first octet only for debugging)
	result = pr.patterns["ipv4"].ReplaceAllStringFunc(result, func(match string) string {
		parts := strings.Split(match, ".")
		if len(parts) == 4 {
			return parts[0] + ".***.***.***"
		}
		return "***.***.***.***"
	})

	// Redact API keys
	result = pr.patterns["api_key"].ReplaceAllStringFunc(result, func(match string) string {
		if len(match) > 8 {
			return match[:4] + "..." + match[len(match)-4:]
		}
		return "***"
	})

	return result
}

// DetectPII returns list of PII types found in input
func (pr *PIIRedactor) DetectPII(input string) []string {
	var detected []string

	for name, pattern := range pr.patterns {
		if pattern.MatchString(input) {
			detected = append(detected, name)
		}
	}

	return detected
}

// ComplianceChecker validates audit logs against compliance policies
type ComplianceChecker struct {
	policies map[string]CompliancePolicy
}

type CompliancePolicy struct {
	Name            string
	RequiredFields  []string // fields that must be present
	ForbiddenFields []string // fields that must NOT be present (PII)
	RetentionDays   int      // how long logs must be retained
	Description     string
}

func NewComplianceChecker() *ComplianceChecker {
	return &ComplianceChecker{
		policies: map[string]CompliancePolicy{
			"gdpr": {
				Name:            "GDPR",
				RequiredFields:  []string{"timestamp", "actor", "action"},
				ForbiddenFields: []string{"ssn", "credit_card", "raw_pii"},
				RetentionDays:   90, // 90 days minimum for security logs
				Description:     "EU General Data Protection Regulation",
			},
			"hipaa": {
				Name:            "HIPAA",
				RequiredFields:  []string{"timestamp", "actor", "resource", "action"},
				ForbiddenFields: []string{"patient_id", "medical_record", "diagnosis"},
				RetentionDays:   2555, // 7 years retention required
				Description:     "Health Insurance Portability and Accountability Act",
			},
			"soc2": {
				Name:            "SOC 2",
				RequiredFields:  []string{"timestamp", "actor", "action", "resource", "result"},
				ForbiddenFields: []string{},
				RetentionDays:   365, // 1 year minimum
				Description:     "Service Organization Control 2",
			},
			"pci_dss": {
				Name:            "PCI DSS",
				RequiredFields:  []string{"timestamp", "actor", "action", "system"},
				ForbiddenFields: []string{"pan", "cvv", "track_data"}, // payment card data
				RetentionDays:   90,
				Description:     "Payment Card Industry Data Security Standard",
			},
		},
	}
}

// Validate checks if entry complies with policy
func (cc *ComplianceChecker) Validate(entry Entry, policyName string) (bool, []string) {
	policy, exists := cc.policies[policyName]
	if !exists {
		return false, []string{"unknown policy: " + policyName}
	}

	violations := []string{}

	// Check required fields (simplified: check if metadata contains field names)
	metadata := strings.ToLower(entry.Metadata)
	for _, required := range policy.RequiredFields {
		if !strings.Contains(metadata, strings.ToLower(required)) {
			violations = append(violations, "missing required field: "+required)
		}
	}

	// Check forbidden fields
	for _, forbidden := range policy.ForbiddenFields {
		if strings.Contains(metadata, strings.ToLower(forbidden)) {
			violations = append(violations, "contains forbidden field: "+forbidden)
		}
	}

	return len(violations) == 0, violations
}

// GetPolicy returns compliance policy details
func (cc *ComplianceChecker) GetPolicy(name string) (CompliancePolicy, bool) {
	policy, exists := cc.policies[name]
	return policy, exists
}

// ListPolicies returns all available compliance policies
func (cc *ComplianceChecker) ListPolicies() []string {
	names := []string{}
	for name := range cc.policies {
		names = append(names, name)
	}
	return names
}
