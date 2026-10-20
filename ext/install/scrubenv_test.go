package install

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/AutumnsGrove/nova/ext"
)

// The installer starts git and the extension's build script, both of which run
// code the extension's author chose. Neither may inherit Nova's credentials.
func TestInstaller_GitDoesNotInheritScrubbedVariables(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("NOVA_T_SECRET", "hunter2")
	t.Setenv("NOVA_T_PLAIN", "ok")
	// A git alias that starts with "!" runs a shell command, which prints what it can see.
	out, err := execGit{scrub: []string{"NOVA_T_SECRET"}}.Run(context.Background(), t.TempDir(),
		"-c", `alias.peek=!echo "[$NOVA_T_SECRET][$NOVA_T_PLAIN]"`, "peek")
	if err != nil || out != "[][ok]" {
		t.Fatalf("git saw %q, %v", out, err)
	}
}

func TestInstaller_DefaultBuildRunnerDoesNotInheritScrubbedVariables(t *testing.T) {
	t.Setenv("NOVA_T_SECRET", "hunter2")
	i := NewInstaller(InstallOptions{ScrubEnv: []string{"NOVA_T_SECRET"}})
	runner, ok := i.run.(ext.ExecRunner)
	if !ok {
		t.Fatalf("default runner is %T, want ext.ExecRunner", i.run)
	}
	for _, kv := range runner.Env {
		if strings.HasPrefix(kv, "NOVA_T_SECRET=") {
			t.Fatal("the build runner's environment still holds the scrubbed variable")
		}
	}
	if len(runner.Env) == 0 {
		t.Fatal("the build runner has an empty environment, not a filtered one")
	}
}
