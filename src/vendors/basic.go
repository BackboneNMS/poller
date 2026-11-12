package vendors

// Vendors that are only identified by name, with nothing read beyond the standard MIBs

var aviat = &Vendor{
	Name:              "Aviat",
	EnterpriseNumbers: []string{"2509"},
}

var cambium = &Vendor{
	Name:              "Cambium",
	EnterpriseNumbers: []string{"161", "17713"},
}

var cyberPower = &Vendor{
	Name:              "CyberPower",
	EnterpriseNumbers: []string{"3808"},
}

var fiberStore = &Vendor{
	Name:              "FiberStore",
	EnterpriseNumbers: []string{"52642"},
}

var hewlettPackard = &Vendor{
	Name:              "Hewlett Packard",
	EnterpriseNumbers: []string{"11", "232"},
}

// nokia is Nokia's own enterprise number; most Nokia network equipment uses those of nokiaSROS and nokiaISAM instead
var nokia = &Vendor{
	Name:              "Nokia",
	EnterpriseNumbers: []string{"94"},
}

var ubiquiti = &Vendor{
	Name:              "Ubiquiti",
	EnterpriseNumbers: []string{"41112"},
}
