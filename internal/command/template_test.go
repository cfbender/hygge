package command

import (
	"context"
	"strings"
	"testing"
)

func TestNextToken(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in       string
		wantTok  string
		wantRest string
		wantErr  bool
	}{
		{in: "", wantTok: "", wantRest: ""},
		{in: "   ", wantTok: "", wantRest: ""},
		{in: "abc", wantTok: "abc", wantRest: ""},
		{in: "abc def", wantTok: "abc", wantRest: " def"},
		{in: "  abc def", wantTok: "abc", wantRest: " def"},
		{in: `"hello world" tail`, wantTok: "hello world", wantRest: " tail"},
		{in: `"esc\"aped"`, wantTok: `esc"aped`, wantRest: ""},
		{in: `"unterminated`, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			t.Parallel()
			tok, rest, err := nextToken(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if tok != c.wantTok {
				t.Errorf("tok = %q, want %q", tok, c.wantTok)
			}
			if rest != c.wantRest {
				t.Errorf("rest = %q, want %q", rest, c.wantRest)
			}
		})
	}
}

func TestParseArgs(t *testing.T) {
	t.Parallel()

	t.Run("no specs returns full tail", func(t *testing.T) {
		t.Parallel()
		v, tail, err := parseArgs("  hello there  ", nil)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if v != nil {
			t.Errorf("values = %v, want nil", v)
		}
		if tail != "hello there  " {
			t.Errorf("tail = %q", tail)
		}
	})

	t.Run("single arg captures whole tail", func(t *testing.T) {
		t.Parallel()
		specs := []ArgSpec{{Name: "code", Required: true}}
		v, _, err := parseArgs("def foo() pass", specs)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got := v["code"]; got != "def foo() pass" {
			t.Errorf("code = %q", got)
		}
	})

	t.Run("two args splits at whitespace, second captures rest", func(t *testing.T) {
		t.Parallel()
		specs := []ArgSpec{
			{Name: "name", Required: true},
			{Name: "body", Required: true},
		}
		v, _, err := parseArgs("alice hello world", specs)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if v["name"] != "alice" {
			t.Errorf("name = %q", v["name"])
		}
		if v["body"] != "hello world" {
			t.Errorf("body = %q", v["body"])
		}
	})

	t.Run("quoted first arg", func(t *testing.T) {
		t.Parallel()
		specs := []ArgSpec{
			{Name: "title", Required: true},
			{Name: "body", Required: true},
		}
		v, _, err := parseArgs(`"with spaces" rest of message`, specs)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if v["title"] != "with spaces" {
			t.Errorf("title = %q", v["title"])
		}
		if v["body"] != "rest of message" {
			t.Errorf("body = %q", v["body"])
		}
	})

	t.Run("missing required arg errors", func(t *testing.T) {
		t.Parallel()
		specs := []ArgSpec{{Name: "code", Required: true}}
		_, _, err := parseArgs("", specs)
		if err == nil {
			t.Fatal("expected missing-required error")
		}
		if !strings.Contains(err.Error(), "code") {
			t.Errorf("error should name arg, got %v", err)
		}
	})

	t.Run("missing optional arg is fine", func(t *testing.T) {
		t.Parallel()
		specs := []ArgSpec{{Name: "code", Required: false}}
		v, _, err := parseArgs("", specs)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if _, ok := v["code"]; ok {
			t.Errorf("expected no value for missing optional, got %v", v)
		}
	})

	t.Run("unterminated quote errors", func(t *testing.T) {
		t.Parallel()
		specs := []ArgSpec{
			{Name: "a", Required: true},
			{Name: "b", Required: true},
		}
		_, _, err := parseArgs(`"never closed`, specs)
		if err == nil {
			t.Fatal("expected parse error")
		}
	})
}

func TestPlaceholdersIn(t *testing.T) {
	t.Parallel()
	got := placeholdersIn("hello {{name}} and {{ topic }} and {{tail}} again {{name}}")
	want := []string{"name", "topic", "tail"}
	if !equalStrings(got, want) {
		t.Errorf("placeholdersIn = %v, want %v", got, want)
	}
}

func TestRenderTemplate(t *testing.T) {
	t.Parallel()
	out := renderTemplate("Hi {{name}}! Body: {{body}} (tail={{tail}}) literal:{{unknown}}",
		map[string]string{"name": "Alice", "body": "msg here"},
		"trailing text",
	)
	if !strings.Contains(out, "Hi Alice!") {
		t.Errorf("name not substituted: %s", out)
	}
	if !strings.Contains(out, "Body: msg here") {
		t.Errorf("body not substituted: %s", out)
	}
	if !strings.Contains(out, "tail=trailing text") {
		t.Errorf("tail not substituted: %s", out)
	}
	if !strings.Contains(out, "literal:{{unknown}}") {
		t.Errorf("unknown should remain literal: %s", out)
	}
}

func TestTemplateCommandExecuteHappyPath(t *testing.T) {
	t.Parallel()
	tc := &templateCommand{
		name:        "review",
		description: "Review code",
		source:      "user",
		args:        []ArgSpec{{Name: "code", Required: true}},
		prompt:      "Review:\n\n{{code}}",
	}
	out, err := tc.Execute(context.Background(), nil, "func foo() {}")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.Message, "Review:") {
		t.Errorf("message missing prefix: %q", out.Message)
	}
	if !strings.Contains(out.Message, "func foo() {}") {
		t.Errorf("message missing body: %q", out.Message)
	}
}

func TestTemplateCommandExecuteMissingRequired(t *testing.T) {
	t.Parallel()
	tc := &templateCommand{
		name:        "review",
		description: "Review code",
		source:      "user",
		args:        []ArgSpec{{Name: "code", Required: true}},
		prompt:      "Review:\n\n{{code}}",
	}
	out, err := tc.Execute(context.Background(), nil, "")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Message != "" {
		t.Errorf("expected empty Message, got %q", out.Message)
	}
	if !strings.Contains(out.Notice, "missing required arg") {
		t.Errorf("expected validation notice, got %q", out.Notice)
	}
}

func TestTemplateCommandTailOnly(t *testing.T) {
	t.Parallel()
	tc := &templateCommand{
		name:        "brief",
		description: "TLDR",
		source:      "user",
		args:        nil, // no named args; entire input → {{tail}}
		prompt:      "Summarise: {{tail}}",
	}
	out, _ := tc.Execute(context.Background(), nil, "./src/main.go")
	if !strings.Contains(out.Message, "Summarise: ./src/main.go") {
		t.Errorf("message = %q", out.Message)
	}
}
