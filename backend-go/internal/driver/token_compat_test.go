package driver

import "testing"

func TestDjangoCompat(t *testing.T) {
	const secret = "dev-insecure-secret-change-me-min-32-bytes"
	const tok = "019f7cbd-b6d3-737e-8fcc-88491c7ae226:1wp1nd:gVWsf_mtRwl3jdIZae-EieaGV0dfF4r3wdrimcrArQE"
	v, err := UnsignToken(secret, tok)
	if err != nil || v != "019f7cbd-b6d3-737e-8fcc-88491c7ae226" {
		t.Fatalf("unsign django token: %q %v", v, err)
	}
	mine := SignToken(secret, "abc")
	if v, err := UnsignToken(secret, mine); err != nil || v != "abc" {
		t.Fatalf("roundtrip: %q %v", v, err)
	}
	if _, err := UnsignToken(secret, tok+"x"); err == nil {
		t.Fatal("tampered token accepted")
	}
}
