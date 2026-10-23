package k8scompat

import (
	"os"
	"testing"
)

// enabledEnvVar gates every compat test behind an explicit opt-in so they
// never run as part of the default `task test`/pre-commit loop, only under
// `task test-compat` (which sets this). A `testing.Short()` check is not
// enough here: the repo's own `task test` never passes -short, so that
// alone would still let these slow, network-heavy tests run by default.
const enabledEnvVar = "K8SCOMPAT_TESTS"

func isEnabled() bool {
	return os.Getenv(enabledEnvVar) == "1"
}

// RequireEnabled skips t unless the compat suite has been explicitly
// opted into (see enabledEnvVar), builds and runs real containers.
func RequireEnabled(t *testing.T) {
	t.Helper()
	if !isEnabled() {
		t.Skipf("set %s=1 to run (builds and runs real containers); see `task test-compat`", enabledEnvVar)
	}
}
