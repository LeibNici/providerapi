package plugin

import (
	"os"
	"strings"
	"testing"

	"github.com/LeibNici/providerapi/internal/config"
)

func TestPluginEnvReplacesInheritedCredential(t *testing.T) {
	t.Setenv("PROVIDERAPI_CREDENTIAL", "WRONG")
	t.Setenv("OPENROUTER_API_KEY", "RIGHT")
	t.Setenv("OTHER_VAR", "keep")

	env := pluginEnv(config.Credential{Env: "OPENROUTER_API_KEY"})
	for _, e := range env {
		if strings.HasPrefix(e, "PROVIDERAPI_CREDENTIAL=") && e != "PROVIDERAPI_CREDENTIAL=RIGHT" {
			t.Fatalf("unexpected credential entry: %s", e)
		}
	}
	found := false
	for _, e := range env {
		if e == "PROVIDERAPI_CREDENTIAL=RIGHT" {
			found = true
		}
		if e == "PROVIDERAPI_CREDENTIAL=WRONG" {
			t.Fatal("inherited WRONG credential not removed")
		}
	}
	if !found {
		t.Fatal("expected PROVIDERAPI_CREDENTIAL=RIGHT")
	}
	foundOther := false
	for _, e := range env {
		if e == "OTHER_VAR=keep" {
			foundOther = true
		}
	}
	if !foundOther {
		t.Fatal("expected OTHER_VAR preserved")
	}
}

func TestPluginEnvNoCredentialWhenUnset(t *testing.T) {
	_ = os.Unsetenv("PROVIDERAPI_CREDENTIAL")
	t.Setenv("UNRELATED", "x")
	env := pluginEnv(config.Credential{})
	for _, e := range env {
		if strings.HasPrefix(e, "PROVIDERAPI_CREDENTIAL=") {
			t.Fatalf("unexpected credential: %s", e)
		}
	}
}
