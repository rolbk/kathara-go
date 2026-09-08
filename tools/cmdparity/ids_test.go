package main

import "testing"

// TestIdentityHelpersMatchOracle pins the two derivations every recording's
// tokenization depends on against the values `Kathara.utils` produces on this
// host. A drift here silently stops tokenizing container names, which turns a
// cross-host recording into noise.
func TestIdentityHelpersMatchOracle(t *testing.T) {
	if got, want := GenerateURLSafeHash("kathara_vlab"), "rMHnlcI87I74aEZKpwj7Q"; got != want {
		t.Errorf("GenerateURLSafeHash(kathara_vlab) = %q, want %q", got, want)
	}
	if got, want := GenerateURLSafeHash("cmdparity"), "Q02dQDxng6ZSVadpsr2fA"; got != want {
		t.Errorf("GenerateURLSafeHash(cmdparity) = %q, want %q", got, want)
	}
	if got, want := Slug("root-iiddmulskdr2aecwlsdug"), "root-iiddmulskdr2aecwlsdug"; got != want {
		t.Errorf("Slug = %q, want %q", got, want)
	}
}
