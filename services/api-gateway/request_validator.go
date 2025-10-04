package main
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	// ErrValidationFailed is returned when validation fails
	ErrValidationFailed = errors.New("validation failed")
	
	// Common regex patterns (compiled once for performance)
	uuidRegex    = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)
	alphaNumRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	
	// Size limits
	maxStringLen = 10000
	maxArrayLen  = 1000
	maxDepth     = 10
)

// ValidationError contains detailed validation errors
type ValidationError struct {
	Field   string
	Message string
	Value   interface{}
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("field '%s': %s", e.Field, e.Message)
}

// Schema defines validation rules for request payloads
type Schema struct {
	Required    []string
	Properties  map[string]PropertySchema
	MaxSize     int // max JSON size in bytes
}

// PropertySchema defines validation for a single property
type PropertySchema struct {
	Type        string   // string, number, integer, boolean, array, object
	MinLength   int
	MaxLength   int
	Min         float64
	Max         float64
	Pattern     *regexp.Regexp
	Enum        []string
	Format      string   // email, uuid, ipv4, ipv6, url, date-time
	Items       *PropertySchema // for arrays
	Required    bool
}

// RequestValidator validates incoming requests against schemas
type RequestValidator struct {
	schemas map[string]*Schema
}

// NewRequestValidator creates a validator with predefined schemas
func NewRequestValidator() *RequestValidator {
	rv := &RequestValidator{
		schemas: make(map[string]*Schema),
	}
	
	// Register common schemas
	rv.RegisterSchema("ingest_event", &Schema{
		Required: []string{"id", "timestamp", "type", "severity"},
		MaxSize:  1 << 20, // 1MB
		Properties: map[string]PropertySchema{
			"id": {
				Type:    "string",
				Format:  "uuid",
				Required: true,
			},
			"timestamp": {
				Type:    "integer",
				Min:     0,
				Required: true,
			},
			"type": {
				Type:    "string",
				Enum:    []string{"security", "network", "system", "application"},
				Required: true,
			},
			"severity": {
				Type:    "string",
				Enum:    []string{"low", "medium", "high", "critical"},
				Required: true,
			},
			"source": {
				Type:      "string",
				MinLength: 1,
				MaxLength: 256,
			},
			"metadata": {
				Type: "object",
			},
			"tags": {
				Type: "array",
				Items: &PropertySchema{
					Type:      "string",
					MaxLength: 64,
				},
			},
		},
	})
	
	rv.RegisterSchema("threat_report", &Schema{
		Required: []string{"threat_id", "detected_at", "confidence"},
		MaxSize:  512 * 1024, // 512KB
		Properties: map[string]PropertySchema{
			"threat_id": {
				Type:    "string",
				Format:  "uuid",
				Required: true,
			},
			"detected_at": {
				Type:    "integer",
				Min:     0,
				Required: true,
			},
			"confidence": {
				Type:    "number",
				Min:     0.0,
				Max:     1.0,
				Required: true,
			},
			"threat_type": {
				Type: "string",
				Enum: []string{"malware", "phishing", "ddos", "intrusion", "data_exfil", "unknown"},
			},
			"indicators": {
				Type: "array",
				Items: &PropertySchema{
					Type: "object",
				},
			},
		},
	})
	
	return rv
}

// RegisterSchema adds a new validation schema
func (rv *RequestValidator) RegisterSchema(name string, schema *Schema) {
	rv.schemas[name] = schema
}

// Validate validates data against named schema
func (rv *RequestValidator) Validate(schemaName string, data map[string]interface{}) error {
	schema, exists := rv.schemas[schemaName]
	if !exists {
		return fmt.Errorf("schema '%s' not found", schemaName)
	}
	
	// Check required fields
	for _, field := range schema.Required {
		if _, exists := data[field]; !exists {
			return ValidationError{
				Field:   field,
				Message: "required field missing",
			}
		}
	}
	
	// Validate each property
	for key, value := range data {
		propSchema, hasPropSchema := schema.Properties[key]
		if !hasPropSchema {
			// Unknown field - could warn or ignore based on strict mode
			continue
		}
		
		if err := rv.validateProperty(key, value, propSchema, 0); err != nil {
			return err
		}
	}
	
	return nil
}

func (rv *RequestValidator) validateProperty(field string, value interface{}, schema PropertySchema, depth int) error {
	if depth > maxDepth {
		return ValidationError{Field: field, Message: "max nesting depth exceeded"}
	}
	
	// Type checking
	switch schema.Type {
	case "string":
		str, ok := value.(string)
		if !ok {
			return ValidationError{Field: field, Message: "must be string", Value: value}
		}
		
		// Length checks
		if schema.MinLength > 0 && len(str) < schema.MinLength {
			return ValidationError{Field: field, Message: fmt.Sprintf("min length %d", schema.MinLength)}
		}
		if schema.MaxLength > 0 && len(str) > schema.MaxLength {
			return ValidationError{Field: field, Message: fmt.Sprintf("max length %d", schema.MaxLength)}
		}
		
		// Pattern matching
		if schema.Pattern != nil && !schema.Pattern.MatchString(str) {
			return ValidationError{Field: field, Message: "pattern mismatch"}
		}
		
		// Enum validation
		if len(schema.Enum) > 0 {
			found := false
			for _, allowed := range schema.Enum {
				if str == allowed {
					found = true
					break
				}
			}
			if !found {
				return ValidationError{Field: field, Message: fmt.Sprintf("must be one of: %v", schema.Enum)}
			}
		}
		
		// Format validation
		if schema.Format != "" {
			if err := validateFormat(str, schema.Format); err != nil {
				return ValidationError{Field: field, Message: err.Error()}
			}
		}
		
	case "number", "integer":
		var num float64
		switch v := value.(type) {
		case float64:
			num = v
		case int:
			num = float64(v)
		case int64:
			num = float64(v)
		default:
			return ValidationError{Field: field, Message: "must be number", Value: value}
		}
		
		if schema.Type == "integer" && num != float64(int64(num)) {
			return ValidationError{Field: field, Message: "must be integer"}
		}
		
		if schema.Min != 0 && num < schema.Min {
			return ValidationError{Field: field, Message: fmt.Sprintf("min value %v", schema.Min)}
		}
		if schema.Max != 0 && num > schema.Max {
			return ValidationError{Field: field, Message: fmt.Sprintf("max value %v", schema.Max)}
		}
		
	case "boolean":
		if _, ok := value.(bool); !ok {
			return ValidationError{Field: field, Message: "must be boolean", Value: value}
		}
		
	case "array":
		arr, ok := value.([]interface{})
		if !ok {
			return ValidationError{Field: field, Message: "must be array", Value: value}
		}
		
		if len(arr) > maxArrayLen {
			return ValidationError{Field: field, Message: fmt.Sprintf("max array length %d", maxArrayLen)}
		}
		
		// Validate array items
		if schema.Items != nil {
			for i, item := range arr {
				itemField := fmt.Sprintf("%s[%d]", field, i)
				if err := rv.validateProperty(itemField, item, *schema.Items, depth+1); err != nil {
					return err
				}
			}
		}
		
	case "object":
		_, ok := value.(map[string]interface{})
		if !ok {
			return ValidationError{Field: field, Message: "must be object", Value: value}
		}
		// Could recursively validate nested objects with their schemas
	}
	
	return nil
}

// validateFormat checks string formats
func validateFormat(value, format string) error {
	switch format {
	case "uuid":
		if !uuidRegex.MatchString(value) {
			return errors.New("invalid UUID format")
		}
	case "email":
		if _, err := mail.ParseAddress(value); err != nil {
			return errors.New("invalid email format")
		}
	case "url":
		if _, err := url.ParseRequestURI(value); err != nil {
			return errors.New("invalid URL format")
		}
	case "ipv4":
		ip := net.ParseIP(value)
		if ip == nil || ip.To4() == nil {
			return errors.New("invalid IPv4 format")
		}
	case "ipv6":
		ip := net.ParseIP(value)
		if ip == nil || ip.To4() != nil {
			return errors.New("invalid IPv6 format")
		}
	case "date-time":
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			return errors.New("invalid date-time format (RFC3339)")
		}
	}
	return nil
}

// ValidateJSON validates raw JSON against schema with size limit
func (rv *RequestValidator) ValidateJSON(schemaName string, jsonData []byte) error {
	schema, exists := rv.schemas[schemaName]
	if !exists {
		return fmt.Errorf("schema '%s' not found", schemaName)
	}
	
	// Size check before parsing
	if schema.MaxSize > 0 && len(jsonData) > schema.MaxSize {
		return ValidationError{
			Field:   "payload",
			Message: fmt.Sprintf("exceeds max size %d bytes", schema.MaxSize),
		}
	}
	
	// Parse JSON
	var data map[string]interface{}
	if err := json.Unmarshal(jsonData, &data); err != nil {
		return ValidationError{
			Field:   "payload",
			Message: "invalid JSON: " + err.Error(),
		}
	}
	
	return rv.Validate(schemaName, data)
}

// SanitizeString removes potentially dangerous characters
func SanitizeString(s string) string {
	// Remove control characters and limit length
	var b strings.Builder
	b.Grow(len(s))
	
	for _, r := range s {
		if r >= 32 && r != 127 { // printable ASCII and beyond
			b.WriteRune(r)
			if b.Len() >= maxStringLen {
				break
			}
		}
	}
	
	return b.String()
}

// IsAlphanumeric checks if string contains only safe characters
func IsAlphanumeric(s string) bool {
	return alphaNumRegex.MatchString(s)
}
