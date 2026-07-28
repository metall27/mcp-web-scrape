package browser

import (
	"encoding/json"
	"testing"
	"time"
)

// TestLoginResultJSON verifies the metadata-facing JSON shape of LoginResult —
// the contract js_tool.go surfaces in metadata.login_result.
func TestLoginResultJSON(t *testing.T) {
	// Full result: success with evidence.
	lr := LoginResult{
		Status:             LoginStatusSuccess,
		Evidence:           []string{"URL changed from x.com/login to x.com/dashboard", "cookies 0→2"},
		SubmitSelectorUsed: "button[type=submit]",
	}
	b, err := json.Marshal(lr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	j := string(b)
	for _, want := range []string{
		`"status":"success"`,
		`"URL changed from x.com/login to x.com/dashboard"`,
		`"cookies 0→2"`,
		`"submit_selector_used":"button[type=submit]"`,
	} {
		if !contains(j, want) {
			t.Errorf("LoginResult JSON missing %q; got %s", want, j)
		}
	}

	// Empty evidence → omitted.
	lr2 := LoginResult{Status: LoginStatusAuthFailed}
	b2, _ := json.Marshal(lr2)
	if contains(string(b2), "evidence") {
		t.Errorf("auth_failed with no evidence should omit evidence key; got %s", string(b2))
	}
	if !contains(string(b2), `"status":"auth_failed"`) {
		t.Errorf("auth_failed status missing; got %s", string(b2))
	}
}

// TestLoginActionValidation verifies the login entry in requiredFields rejects
// a login action missing any of its four required fields. This is the eager
// validation ParseActions runs before any Chrome work (#50 pattern).
func TestLoginActionValidation(t *testing.T) {
	cases := []struct {
		name   string
		action Action
		// wantMissing is the field name the validator should flag as missing.
		wantMissing string
	}{
		{"missing username_selector", Action{
			Type: "login", Username: "user", PasswordSelector: "input[type=password]", Password: "secret",
		}, "username_selector"},
		{"missing username", Action{
			Type: "login", UsernameSelector: "#user", PasswordSelector: "input[type=password]", Password: "secret",
		}, "username"},
		{"missing password_selector", Action{
			Type: "login", UsernameSelector: "#user", Username: "user", Password: "secret",
		}, "password_selector"},
		{"missing password", Action{
			Type: "login", UsernameSelector: "#user", Username: "user", PasswordSelector: "input[type=password]",
		}, "password"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAction(tc.action, 0)
			if err == nil {
				t.Fatalf("expected validation error for %s, got nil", tc.name)
			}
			ve, ok := err.(*ActionValidationError)
			if !ok {
				t.Fatalf("expected *ActionValidationError, got %T", err)
			}
			if !contains(ve.Reason, tc.wantMissing) {
				t.Errorf("error should mention %q; got %q", tc.wantMissing, ve.Reason)
			}
		})
	}

	// Valid login action (submit_selector optional) → no error.
	valid := Action{
		Type: "login", UsernameSelector: "#user", Username: "user",
		PasswordSelector: "input[type=password]", Password: "secret",
	}
	if err := validateAction(valid, 0); err != nil {
		t.Errorf("valid login action should pass validation; got %v", err)
	}

	// Valid login WITH optional submit_selector → no error.
	validSubmit := valid
	validSubmit.SubmitSelector = "button[type=submit]"
	if err := validateAction(validSubmit, 0); err != nil {
		t.Errorf("valid login with submit_selector should pass; got %v", err)
	}
}

// TestDiffKeys verifies the auth-key diff used by login verify to detect
// newly-appeared token/session keys.
func TestDiffKeys(t *testing.T) {
	cases := []struct {
		name   string
		before []string
		after  []string
		want   []string
	}{
		{"no change", []string{"auth-store"}, []string{"auth-store"}, nil},
		{"one new key", []string{"auth-store"}, []string{"auth-store", "session-id"}, []string{"session-id"}},
		{"multiple new", []string{}, []string{"token", "user", "jwt"}, []string{"token", "user", "jwt"}},
		{"key removed (not relevant)", []string{"a", "b"}, []string{"a"}, nil},
		{"empty both", nil, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := diffKeys(tc.before, tc.after)
			if len(got) != len(tc.want) {
				t.Fatalf("diffKeys(%v,%v) = %v, want %v", tc.before, tc.after, got, tc.want)
			}
			wantSet := map[string]bool{}
			for _, w := range tc.want {
				wantSet[w] = true
			}
			for _, g := range got {
				if !wantSet[g] {
					t.Errorf("unexpected key %q in diff", g)
				}
			}
		})
	}
}

// TestShortURL verifies the scheme-stripping helper for compact evidence.
func TestShortURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://x.com/login", "x.com/login"},
		{"http://localhost:8192/dashboard", "localhost:8192/dashboard"},
		{"x.com/no-scheme", "x.com/no-scheme"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := shortURL(tc.in); got != tc.want {
			t.Errorf("shortURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestLoginActionFieldsRoundTrip verifies a login Action survives JSON
// marshal/unmarshal — the fields the LLM supplies via the action schema must
// round-trip cleanly to the Action struct (ParseActions unmarshals via json).
func TestLoginActionFieldsRoundTrip(t *testing.T) {
	in := Action{
		Type:             "login",
		UsernameSelector: "#email",
		Username:         "user@example.com",
		PasswordSelector: "input[type=password]",
		Password:         "s3cret",
		SubmitSelector:   "button[type=submit]",
		Timeout:          10 * time.Second,
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Action
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.UsernameSelector != in.UsernameSelector ||
		out.Username != in.Username ||
		out.PasswordSelector != in.PasswordSelector ||
		out.Password != in.Password ||
		out.SubmitSelector != in.SubmitSelector {
		t.Errorf("login fields did not round-trip:\n in=%+v\nout=%+v", in, out)
	}
}
