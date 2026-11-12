package vendors

import "testing"

func TestRegistry(t *testing.T) {
	for _, vendor := range registry {
		if len(vendor.Name) < 1 || len(vendor.EnterpriseNumbers) < 1 {
			t.Errorf("vendor %+v needs a name and at least one enterprise number", vendor)
		}
	}

	defer func() {
		if recover() == nil {
			t.Fatal("expected two vendors claiming the same enterprise number to be refused")
		}
	}()

	indexVendors([]*Vendor{{Name: "A", EnterpriseNumbers: []string{"1"}}, {Name: "B", EnterpriseNumbers: []string{"1"}}})
}

func TestIdentify(t *testing.T) {
	for objectID, expected := range map[string]string{
		"1.3.6.1.4.1.9.1.2134":         "Cisco",
		".1.3.6.1.4.1.2636.1.1.1.2.31": "Juniper",
		"1.3.6.1.4.1.6527.1.3.17":      "Nokia",
		"1.3.6.1.4.1.637.61.1":         "Nokia",
		"1.3.6.1.4.1.99999.1":          "",
		"1.3.6.1.2.1.1":                "",
		"":                             "",
	} {
		if vendor := Identify(objectID); vendor.Name != expected {
			t.Errorf("%q: expected %q, got %q", objectID, expected, vendor.Name)
		}
	}

	if Identify("1.3.6.1.4.1.6527.1.3.17") != nokiaSROS || Identify("1.3.6.1.4.1.637.61.1") != nokiaISAM {
		t.Fatal("expected each Nokia family to get its own definition")
	}
}

func TestModels(t *testing.T) {
	for _, test := range []struct {
		vendor      *Vendor
		description string
		expected    string
	}{
		{juniper, "Juniper Networks, Inc. ex3300-48p Ethernet Switch, kernel JUNOS 15.1R7.9", "EX3300-48P"},
		{juniper, "Juniper Networks", ""},
		{mikroTik, "RouterOS CCR2004-16G-2S+", "CCR2004-16G-2S+"},
		{mikroTik, "MikroTik", ""},
		{netonix, "Netonix WS-12-250-AC", "WS-12-250-AC"},
		{nokiaSROS, "TiMOS-B-20.10.R3 both/x86_64 Nokia 7750 SR Copyright (c) 2000-2020 Nokia.", "7750 SR"},
	} {
		if model := test.vendor.Model(test.description); model != test.expected {
			t.Errorf("%s %q: expected %q, got %q", test.vendor.Name, test.description, test.expected, model)
		}
	}
}
