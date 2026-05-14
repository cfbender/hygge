package tool

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cfbender/hygge/internal/skill"
)

// loadOneSkill writes a single skill into home/.agents/skills/<name>.md
// and returns a Registry loaded from that home.
func loadOneSkill(t *testing.T, name, body string) *skill.Registry {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	contents := "---\nname: " + name + "\ndescription: desc-" + name +
		"\nwhen_to_use: when-" + name + "\n---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := skill.Load(skill.LoadOptions{HomeDir: home, Pwd: home})
	if err != nil {
		t.Fatalf("skill.Load: %v", err)
	}
	return reg
}

func TestSkillTool_HappyPath(t *testing.T) {
	reg := loadOneSkill(t, "foo", "Body of foo skill.")
	tool := NewSkillTool(reg)

	args := json.RawMessage(`{"name":"foo"}`)
	res, err := tool.Execute(context.Background(), args, ExecContext{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Errorf("IsError = true, want false; content=%q", res.Content)
	}
	if !strings.Contains(res.Content, "Body of foo skill.") {
		t.Errorf("Content = %q; want it to include the body", res.Content)
	}
	if res.Metadata["name"] != "foo" {
		t.Errorf("Metadata[name] = %v", res.Metadata["name"])
	}
}

func TestSkillTool_NotFound(t *testing.T) {
	reg := loadOneSkill(t, "foo", "foo body")
	tool := NewSkillTool(reg)

	args := json.RawMessage(`{"name":"bar"}`)
	res, err := tool.Execute(context.Background(), args, ExecContext{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Error("IsError = false, want true")
	}
	if !strings.Contains(res.Content, "bar") {
		t.Errorf("Content = %q; missing requested name", res.Content)
	}
	if !strings.Contains(res.Content, "foo") {
		t.Errorf("Content = %q; expected available skills (foo) in message", res.Content)
	}
}

func TestSkillTool_EmptyRegistry(t *testing.T) {
	tool := NewSkillTool(&skill.Registry{})
	args := json.RawMessage(`{"name":"anything"}`)
	res, err := tool.Execute(context.Background(), args, ExecContext{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Error("IsError = false, want true")
	}
	if !strings.Contains(res.Content, "no skills configured") {
		t.Errorf("Content = %q; want 'no skills configured'", res.Content)
	}
}

func TestSkillTool_NilRegistry(t *testing.T) {
	tool := NewSkillTool(nil)
	args := json.RawMessage(`{"name":"x"}`)
	res, err := tool.Execute(context.Background(), args, ExecContext{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Error("IsError = false, want true")
	}
}

func TestSkillTool_MissingName(t *testing.T) {
	reg := loadOneSkill(t, "foo", "x")
	tool := NewSkillTool(reg)
	args := json.RawMessage(`{}`)
	_, err := tool.Execute(context.Background(), args, ExecContext{})
	if err == nil {
		t.Fatal("expected ToolError, got nil")
	}
	var te *ToolError
	if !errors.As(err, &te) {
		t.Fatalf("err = %T, want *ToolError", err)
	}
	if te.Code != CodeInvalidArgs {
		t.Errorf("Code = %q, want %q", te.Code, CodeInvalidArgs)
	}
}

func TestSkillTool_BlankName(t *testing.T) {
	reg := loadOneSkill(t, "foo", "x")
	tool := NewSkillTool(reg)
	args := json.RawMessage(`{"name":"   "}`)
	_, err := tool.Execute(context.Background(), args, ExecContext{})
	if err == nil {
		t.Fatal("expected ToolError, got nil")
	}
}

func TestSkillTool_UnknownField(t *testing.T) {
	reg := loadOneSkill(t, "foo", "x")
	tool := NewSkillTool(reg)
	args := json.RawMessage(`{"name":"foo","extra":"nope"}`)
	_, err := tool.Execute(context.Background(), args, ExecContext{})
	if err == nil {
		t.Fatal("expected ToolError for unknown field")
	}
}

func TestSkillTool_Schema(t *testing.T) {
	tool := NewSkillTool(nil)
	schema := tool.InputSchema()
	if schema["additionalProperties"] != false {
		t.Errorf("additionalProperties = %v, want false", schema["additionalProperties"])
	}
	if tool.Name() != "skill" {
		t.Errorf("Name = %q", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("Description empty")
	}
}

func TestDefaultWith_RegistersSkillToolWhenProvided(t *testing.T) {
	reg := loadOneSkill(t, "demo", "demo body")
	tools := DefaultWith(DefaultOptions{SkillRegistry: reg})

	got, ok := tools.Get("skill")
	if !ok {
		t.Fatal("DefaultWith did not register the skill tool")
	}
	if got.Name() != "skill" {
		t.Errorf("Name = %q", got.Name())
	}
}

func TestDefault_OmitsSkillTool(t *testing.T) {
	tools := Default()
	if _, ok := tools.Get("skill"); ok {
		t.Error("Default() should NOT include the skill tool when no registry is supplied")
	}
}
