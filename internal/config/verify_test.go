package config

import "testing"

// TestVerifyConfig_Validate covers the validation rules for the [verify]
// section added in v0.7.2. The section is optional, but when present it must
// contain at least one non-empty command.
func TestVerifyConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		verify  *VerifyConfig
		wantErr string // substring; "" means no error expected
	}{
		{
			name:   "section absent is fine",
			verify: nil,
		},
		{
			name:   "single command",
			verify: &VerifyConfig{Commands: []string{"go test ./..."}},
		},
		{
			name:   "multiple commands + require_clean",
			verify: &VerifyConfig{Commands: []string{"go build ./...", "go test ./..."}, RequireClean: true},
		},
		{
			name:    "empty commands list rejected",
			verify:  &VerifyConfig{Commands: []string{}},
			wantErr: "commands is empty",
		},
		{
			name:    "nil commands rejected (same as empty after TOML parse)",
			verify:  &VerifyConfig{Commands: nil},
			wantErr: "commands is empty",
		},
		{
			name:    "empty string command rejected",
			verify:  &VerifyConfig{Commands: []string{"go test ./...", "", "go vet ./..."}},
			wantErr: "[verify].commands[1] is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &RepoConfig{Verify: tt.verify}
			err := c.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() expected error containing %q, got nil", tt.wantErr)
			}
			if !containsStr(err.Error(), tt.wantErr) {
				t.Errorf("Validate() error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func containsStr(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
