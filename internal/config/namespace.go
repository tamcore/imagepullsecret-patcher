package config

import (
	"fmt"
	"os"
	"strings"
)

const namespaceEnvVar = "POD_NAMESPACE"

// saNamespacePath is the projected ServiceAccount namespace file. It is a
// variable so tests can point it at a temporary file.
var saNamespacePath = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"

// operatorNamespace returns the namespace the operator runs in, from the
// POD_NAMESPACE environment variable or the mounted ServiceAccount token.
func operatorNamespace() (string, error) {
	if ns := os.Getenv(namespaceEnvVar); ns != "" {
		return ns, nil
	}

	nsBytes, err := os.ReadFile(saNamespacePath)
	if err != nil {
		return "", fmt.Errorf("failed to read %q: %w", saNamespacePath, err)
	}
	return strings.TrimSpace(string(nsBytes)), nil
}
