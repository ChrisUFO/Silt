package templates

import (
	"strings"
	"testing"
)

func TestValidate_ValidTemplate(t *testing.T) {
	tpl := &Template{
		SchemaVersion: "1.0.0",
		ID:            "valid-id",
		Title:         "Valid",
		Category:      "notes",
		Body:          "# Hello\n",
	}
	if err := Validate(tpl); err != nil {
		t.Fatalf("Validate returned error for valid template: %v", err)
	}
}

func TestValidate_NilTemplate(t *testing.T) {
	if err := Validate(nil); err == nil {
		t.Error("expected error for nil template")
	}
}

func TestValidate_EmptyID(t *testing.T) {
	tpl := &Template{
		SchemaVersion: "1.0.0",
		ID:            "",
		Title:         "T",
		Category:      "notes",
		Body:          "body",
	}
	errs := Validate(tpl)
	if errs == nil {
		t.Fatal("expected validation error for empty id")
	}
	if !strings.Contains(errs.Error(), "id is required") {
		t.Errorf("expected 'id is required' in error, got %q", errs.Error())
	}
}

func TestValidate_BadID(t *testing.T) {
	cases := []string{"UPPER", "has space", "dot.id", "sla/sh", ""}
	for _, id := range cases {
		tpl := &Template{
			SchemaVersion: "1.0.0",
			ID:            id,
			Title:         "T",
			Category:      "notes",
			Body:          "body",
		}
		if Validate(tpl) == nil {
			t.Errorf("expected error for id %q", id)
		}
	}
}

func TestValidate_EmptyTitle(t *testing.T) {
	tpl := &Template{SchemaVersion: "1.0.0", ID: "ok", Title: "", Category: "notes", Body: "body"}
	if Validate(tpl) == nil {
		t.Error("expected error for empty title")
	}
}

func TestValidate_EmptyBody(t *testing.T) {
	tpl := &Template{SchemaVersion: "1.0.0", ID: "ok", Title: "T", Category: "notes", Body: "  "}
	if Validate(tpl) == nil {
		t.Error("expected error for empty body")
	}
}

func TestValidate_EmptyCategory(t *testing.T) {
	tpl := &Template{SchemaVersion: "1.0.0", ID: "ok", Title: "T", Category: "", Body: "body"}
	if Validate(tpl) == nil {
		t.Error("expected error for empty category")
	}
}

func TestValidate_UnknownCategoryAccepted(t *testing.T) {
	// An unknown-but-non-empty category is valid (additive/forward-compat).
	tpl := &Template{SchemaVersion: "1.0.0", ID: "ok", Title: "T", Category: "exotic", Body: "body"}
	if err := Validate(tpl); err != nil {
		t.Errorf("unknown category should be accepted (additive), got: %v", err)
	}
}

func TestValidate_BadSchemaVersion(t *testing.T) {
	cases := []string{"v1", "abc", "1.x"}
	for _, sv := range cases {
		tpl := &Template{SchemaVersion: sv, ID: "ok", Title: "T", Category: "notes", Body: "body"}
		if Validate(tpl) == nil {
			t.Errorf("expected error for schema_version %q", sv)
		}
	}
}

func TestValidate_ForwardVersionSchemaAccepted(t *testing.T) {
	// A higher-but-well-formed version is accepted (forward-compat).
	tpl := &Template{SchemaVersion: "2.0.0", ID: "ok", Title: "T", Category: "notes", Body: "body"}
	if err := Validate(tpl); err != nil {
		t.Errorf("forward schema_version should be accepted, got: %v", err)
	}
}

func TestValidate_BadPlaceholderName(t *testing.T) {
	cases := []string{
		"UPPER",    // capitals
		"embed:foo", // colon
		"1starts",   // starts with digit
		"has-dash",  // dash
		"",          // empty
	}
	for _, name := range cases {
		tpl := &Template{
			SchemaVersion: "1.0.0",
			ID:            "ok",
			Title:         "T",
			Category:      "notes",
			Body:          "body",
			Placeholders:  []Placeholder{{Name: name}},
		}
		errs := Validate(tpl)
		if errs == nil {
			t.Errorf("expected error for placeholder name %q", name)
		}
	}
}

func TestValidate_DuplicatePlaceholderName(t *testing.T) {
	tpl := &Template{
		SchemaVersion: "1.0.0",
		ID:            "ok",
		Title:         "T",
		Category:      "notes",
		Body:          "body",
		Placeholders: []Placeholder{
			{Name: "name"},
			{Name: "name"},
		},
	}
	errs := Validate(tpl)
	if errs == nil {
		t.Fatal("expected error for duplicate placeholder")
	}
	if !strings.Contains(errs.Error(), "duplicate") {
		t.Errorf("expected 'duplicate' in error, got %q", errs.Error())
	}
}

func TestIsValidID(t *testing.T) {
	valid := []string{"a", "my-template", "template_123", "daily-note"}
	for _, id := range valid {
		if !IsValidID(id) {
			t.Errorf("IsValidID(%q) = false, want true", id)
		}
	}
	invalid := []string{"", "UPPER", "has space", "sla/sh", "dot.id"}
	for _, id := range invalid {
		if IsValidID(id) {
			t.Errorf("IsValidID(%q) = true, want false", id)
		}
	}
}

func TestValidationErrors_Error(t *testing.T) {
	ve := ValidationErrors{
		{Field: "id", Message: "bad"},
		{Field: "body", Message: "empty"},
	}
	s := ve.Error()
	if !strings.Contains(s, "id") || !strings.Contains(s, "body") {
		t.Errorf("Error() should mention both fields: %q", s)
	}
}

func TestValidationErrors_EmptyError(t *testing.T) {
	ve := ValidationErrors{}
	if ve.Error() != "" {
		t.Errorf("empty ValidationErrors.Error() = %q, want empty", ve.Error())
	}
}
