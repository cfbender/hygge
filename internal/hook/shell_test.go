package hook

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestShellHook_EnvIsolation(t *testing.T) {
	// The shell hook must not forward ANTHROPIC_API_KEY (or any secret
	// env var not in the allowlist) to the subprocess.
	t.Setenv("ANTHROPIC_API_KEY", "sk-secret-should-not-leak")
	script := t.TempDir() + "/env-check.sh"
	code := "#!/bin/sh\nif [ -n \"$ANTHROPIC_API_KEY\" ]; then echo '{\"decision\":\"deny\",\"reason\":\"LEAKED\"}'; fi\n"
	if err := os.WriteFile(script, []byte(code), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	h := &shellHook{
		name:    "env-check",
		events:  []Event{EventPreTool},
		mode:    ModeSync,
		timeout: 5 * time.Second,
		command: script,
	}
	act, err := h.Run(context.Background(), Input{Event: EventPreTool})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if act.Decision == DecisionDeny && act.Reason == "LEAKED" {
		t.Fatal("ANTHROPIC_API_KEY must NOT be forwarded to hook subprocess")
	}
}

func TestShellHook_CustomEnv(t *testing.T) {
	script := t.TempDir() + "/env-read.sh"
	code := `#!/bin/sh
if [ "$MY_CUSTOM_VAR" = "hello" ]; then
  printf '{"decision":"deny","reason":"custom-env-ok"}'
fi
`
	if err := os.WriteFile(script, []byte(code), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	h := &shellHook{
		name:    "env-read",
		events:  []Event{EventPreTool},
		mode:    ModeSync,
		timeout: 5 * time.Second,
		command: script,
		env:     map[string]string{"MY_CUSTOM_VAR": "hello"},
	}
	act, err := h.Run(context.Background(), Input{Event: EventPreTool})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if act.Decision != DecisionDeny || act.Reason != "custom-env-ok" {
		t.Fatalf("want custom-env-ok deny, got decision=%s reason=%q", act.Decision, act.Reason)
	}
}

func TestRegistryClose_AsyncCap(t *testing.T) {
	// Fill the semaphore; the (maxAsyncInflight+1)th hook is dropped with
	// a warn but must not hang or panic.
	reg := New()
	blocker := make(chan struct{})
	dispatched := make(chan struct{}, maxAsyncInflight+2)

	for i := 0; i <= maxAsyncInflight; i++ {
		name := "cap-" + string(rune('a'+i%26))
		_ = reg.Register(&fakeHook{
			name:   name,
			events: []Event{EventPostTool},
			mode:   ModeAsync,
			runFn: func(_ context.Context, _ Input) (Action, error) {
				dispatched <- struct{}{}
				<-blocker
				return Action{Decision: DecisionAllow}, nil
			},
		})
	}

	in := Input{Event: EventPostTool}
	reg.RunPost(context.Background(), EventPostTool, in)

	// Give dispatched goroutines time to start.
	time.Sleep(20 * time.Millisecond)
	count := len(dispatched)

	// Unblock goroutines so Close can finish.
	close(blocker)
	reg.Close()

	// At most maxAsyncInflight could have been dispatched.
	if count > maxAsyncInflight {
		t.Fatalf("want at most %d dispatched, got %d", maxAsyncInflight, count)
	}
}
