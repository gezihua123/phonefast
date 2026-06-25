package adb

import "testing"

func TestScrcpyVersion(t *testing.T) {
	jar := "../../android/scrcpy-server.jar"
	// Relative path works when tests run from package dir.
	v := scrcpyVersion("testdata/missing.jar") // no jar → fallback
	if v != "3.3.4" {
		t.Errorf("fallback: want 3.3.4, got %q", v)
	}

	v = scrcpyVersion(jar)
	if !looksLikeVersion(v) {
		t.Errorf("jar scan: not a valid version: %q", v)
	}
	t.Logf("detected version: %s", v)
}
