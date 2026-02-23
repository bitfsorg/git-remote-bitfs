package config

import (
	"bufio"
	"bytes"
	"path/filepath"
	"strconv"
	"strings"
)

// AccessPolicy determines the access level for a given file path.
type AccessPolicy struct {
	rules []accessRule
}

type accessRule struct {
	pattern string // glob pattern
	access  string // "free", "private", "paid"
	price   uint64 // price in satoshis (for paid)
}

// ParseAttributes parses a .bitfsattributes file content.
// Format is similar to .gitattributes: each line has a glob pattern
// followed by key=value attributes.
//
// Example:
//
//	* access=private
//	docs/** access=free
//	premium/** access=paid price=100
func ParseAttributes(content []byte) (*AccessPolicy, error) {
	p := &AccessPolicy{}
	scanner := bufio.NewScanner(bytes.NewReader(content))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments.
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue // need at least pattern + one attribute
		}

		rule := accessRule{
			pattern: fields[0],
			access:  "private", // default
		}

		for _, attr := range fields[1:] {
			key, val, ok := strings.Cut(attr, "=")
			if !ok {
				continue
			}
			switch key {
			case "access":
				rule.access = val
			case "price":
				v, err := strconv.ParseUint(val, 10, 64)
				if err == nil {
					rule.price = v
				}
			}
		}

		p.rules = append(p.rules, rule)
	}

	return p, scanner.Err()
}

// GetAccess returns the access level and price for a given file path.
// Later rules override earlier ones (last match wins).
// If no rule matches, defaults to "private" with price 0.
//
// Pattern matching follows .gitattributes conventions:
//   - "*" matches all files
//   - Patterns without "/" are matched against the file's basename
//   - Patterns with "/" are matched against the full path
//   - "**" matches across directory boundaries
func (p *AccessPolicy) GetAccess(path string) (access string, price uint64) {
	access = "private"
	price = 0

	for _, rule := range p.rules {
		if matchPattern(rule.pattern, path) {
			access = rule.access
			price = rule.price
		}
	}

	return access, price
}

// matchPattern matches a .gitattributes-style pattern against a path.
func matchPattern(pattern, path string) bool {
	// Handle "**" patterns (double-glob).
	if strings.Contains(pattern, "**") {
		return matchDoubleGlob(pattern, path)
	}

	// If pattern contains no '/', match against basename only.
	// This follows .gitattributes semantics: "*.md" matches "docs/README.md".
	if !strings.Contains(pattern, "/") {
		basename := filepath.Base(path)
		matched, err := filepath.Match(pattern, basename)
		return err == nil && matched
	}

	// Pattern contains '/' — match against the full path.
	matched, err := filepath.Match(pattern, path)
	return err == nil && matched
}

// matchDoubleGlob provides basic ** glob matching.
// Supports patterns like "dir/**" meaning "anything under dir/".
func matchDoubleGlob(pattern, path string) bool {
	// Handle "prefix/**" patterns.
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		return strings.HasPrefix(path, prefix+"/") || path == prefix
	}
	// Handle "**/suffix" patterns.
	if strings.HasPrefix(pattern, "**/") {
		suffix := strings.TrimPrefix(pattern, "**/")
		matched, _ := filepath.Match(suffix, filepath.Base(path))
		return strings.HasSuffix(path, suffix) || matched
	}
	return false
}

// DefaultPolicy returns a policy where everything is private.
func DefaultPolicy() *AccessPolicy {
	return &AccessPolicy{
		rules: []accessRule{
			{pattern: "*", access: "private", price: 0},
		},
	}
}
