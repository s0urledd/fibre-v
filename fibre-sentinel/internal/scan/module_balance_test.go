package scan

import "testing"

// The derivation is the SDK's, checked against an address anyone can look
// up: Celestia's fee collector module account.
func TestModuleAddress(t *testing.T) {
	got, err := ModuleAddress("fee_collector")
	if err != nil {
		t.Fatal(err)
	}
	if want := "celestia17xpfvakm2amg962yls6f84z3kell8c5lpnjs3s"; got != want {
		t.Errorf("fee_collector: %s, want %s", got, want)
	}
	if a, _ := ModuleAddress(FibreModuleName); a == got || len(a) != len(got) {
		t.Errorf("fibre module address %q", a)
	}
}
