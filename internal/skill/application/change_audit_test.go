package application

import "testing"

// TestIsBuiltinSkill pins the platform-seed prefix predicate. A previous
// implementation compared skillID[:7] ("builtin") against "builtin:" (8
// chars), which never matched and silently disabled every builtin guard in
// this package (publish/delete/set-editors/strip-instructions).
func TestIsBuiltinSkill(t *testing.T) {
	cases := []struct {
		name string
		id   string
		want bool
	}{
		{"exact prefix", "builtin:", true},
		{"platform-guide", "builtin:platform-guide", true},
		{"custom skill", "skill-1", false},
		{"prefix not a match", "builtinish", false},
		{"empty", "", false},
		{"shorter than prefix", "builti", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBuiltinSkill(tc.id); got != tc.want {
				t.Fatalf("isBuiltinSkill(%q) = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
}
