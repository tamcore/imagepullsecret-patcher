package config

import (
	"os"
	"path/filepath"
	"testing"
)

func Test_operatorNamespace(t *testing.T) {
	t.Run("returns POD_NAMESPACE when set", func(t *testing.T) {
		// Arrange
		t.Setenv(namespaceEnvVar, "from-env")

		// Act
		ns, err := operatorNamespace()

		// Assert
		if err != nil {
			t.Fatalf("operatorNamespace() error = %v", err)
		}
		if ns != "from-env" {
			t.Errorf("operatorNamespace() = %q, want %q", ns, "from-env")
		}
	})

	t.Run("reads and trims the ServiceAccount namespace file", func(t *testing.T) {
		// Arrange
		t.Setenv(namespaceEnvVar, "")
		path := filepath.Join(t.TempDir(), "namespace")
		if err := os.WriteFile(path, []byte("   from-file\n"), 0o600); err != nil {
			t.Fatalf("failed to write namespace file: %v", err)
		}
		previous := saNamespacePath
		t.Cleanup(func() { saNamespacePath = previous })
		saNamespacePath = path

		// Act
		ns, err := operatorNamespace()

		// Assert
		if err != nil {
			t.Fatalf("operatorNamespace() error = %v", err)
		}
		if ns != "from-file" {
			t.Errorf("operatorNamespace() = %q, want %q", ns, "from-file")
		}
	})

	t.Run("errors when neither the env var nor the file is present", func(t *testing.T) {
		// Arrange
		t.Setenv(namespaceEnvVar, "")
		previous := saNamespacePath
		t.Cleanup(func() { saNamespacePath = previous })
		saNamespacePath = filepath.Join(t.TempDir(), "missing")

		// Act
		ns, err := operatorNamespace()

		// Assert
		if err == nil {
			t.Fatal("operatorNamespace() expected an error, got nil")
		}
		if ns != "" {
			t.Errorf("operatorNamespace() = %q, want empty string", ns)
		}
	})
}
