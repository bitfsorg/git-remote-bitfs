package config

import (
	"testing"
)

func TestDefaultPolicy(t *testing.T) {
	p := DefaultPolicy()

	access, price := p.GetAccess("anything.txt")
	if access != "private" {
		t.Errorf("default access = %q, want %q", access, "private")
	}
	if price != 0 {
		t.Errorf("default price = %d, want 0", price)
	}
}

func TestParseAttributes(t *testing.T) {
	tests := []struct {
		name    string
		content string
		path    string
		access  string
		price   uint64
	}{
		{
			name:    "all free",
			content: "* access=free",
			path:    "anything.txt",
			access:  "free",
			price:   0,
		},
		{
			name:    "all private",
			content: "* access=private",
			path:    "secret.txt",
			access:  "private",
			price:   0,
		},
		{
			name: "override with double-glob",
			content: `* access=private
docs/** access=free`,
			path:   "docs/readme.txt",
			access: "free",
			price:  0,
		},
		{
			name: "double-glob nested",
			content: `* access=private
docs/** access=free`,
			path:   "docs/api/endpoints.txt",
			access: "free",
			price:  0,
		},
		{
			name: "non-matching stays private",
			content: `* access=private
docs/** access=free`,
			path:   "src/main.go",
			access: "private",
			price:  0,
		},
		{
			name: "last match wins",
			content: `* access=free
* access=private`,
			path:   "file.txt",
			access: "private",
			price:  0,
		},
		{
			name: "comment lines skipped",
			content: `# This is a comment
* access=free
# Another comment`,
			path:   "file.txt",
			access: "free",
			price:  0,
		},
		{
			name: "empty lines skipped",
			content: `
* access=free

`,
			path:   "file.txt",
			access: "free",
			price:  0,
		},
		{
			name:    "price attribute",
			content: "premium/** access=paid price=100",
			path:    "premium/video.mp4",
			access:  "paid",
			price:   100,
		},
		{
			name: "specific file pattern",
			content: `* access=private
README.md access=free`,
			path:   "README.md",
			access: "free",
			price:  0,
		},
		{
			name: "glob extension pattern",
			content: `* access=private
*.md access=free`,
			path:   "README.md",
			access: "free",
			price:  0,
		},
		{
			name:    "no matching rule defaults private",
			content: "docs/** access=free",
			path:    "src/main.go",
			access:  "private",
			price:   0,
		},
		{
			name:    "empty content defaults private",
			content: "",
			path:    "anything.txt",
			access:  "private",
			price:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := ParseAttributes([]byte(tt.content))
			if err != nil {
				t.Fatalf("ParseAttributes() error: %v", err)
			}

			access, price := p.GetAccess(tt.path)
			if access != tt.access {
				t.Errorf("GetAccess(%q) access = %q, want %q", tt.path, access, tt.access)
			}
			if price != tt.price {
				t.Errorf("GetAccess(%q) price = %d, want %d", tt.path, price, tt.price)
			}
		})
	}
}

func TestParseAttributesMultipleAttrs(t *testing.T) {
	content := `premium/** access=paid price=500`

	p, err := ParseAttributes([]byte(content))
	if err != nil {
		t.Fatalf("ParseAttributes() error: %v", err)
	}

	access, price := p.GetAccess("premium/course.pdf")
	if access != "paid" {
		t.Errorf("access = %q, want %q", access, "paid")
	}
	if price != 500 {
		t.Errorf("price = %d, want 500", price)
	}
}

func TestParseAttributesLastMatchWins(t *testing.T) {
	content := `* access=free
docs/** access=private
docs/** access=paid price=200`

	p, err := ParseAttributes([]byte(content))
	if err != nil {
		t.Fatalf("ParseAttributes() error: %v", err)
	}

	// docs/file should match the last docs/** rule
	access, price := p.GetAccess("docs/file.txt")
	if access != "paid" {
		t.Errorf("access = %q, want %q", access, "paid")
	}
	if price != 200 {
		t.Errorf("price = %d, want 200", price)
	}

	// non-docs file should match the * rule
	access, price = p.GetAccess("src/main.go")
	if access != "free" {
		t.Errorf("access = %q, want %q", access, "free")
	}
	if price != 0 {
		t.Errorf("price = %d, want 0", price)
	}
}
