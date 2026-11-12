package vendors

import (
	"main/src/cli"
	"regexp"
)

// Vendors that are only identified by name, with nothing read over SNMP beyond the standard MIBs

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
	ConfigPlatforms:   []*ConfigPlatform{hpeProCurve},
}

// hpeProCurve covers ProCurve and ArubaOS-Switch, whose sysDescr has a revision such as "revision WC.16.10.0001".
// They show a banner that waits for a key press before the prompt, which logging in answers.
var hpeProCurve = &ConfigPlatform{
	ID:          "HPE_PROCURVE",
	Name:        "HPE ProCurve / ArubaOS-Switch",
	Description: regexp.MustCompile(`revision [A-Z]{1,2}\.\d+`),
	Clean:       dropLines(`^Running configuration:$`),
	Profile: cli.Profile{
		Prompt:   ciscoStylePrompt,
		Enable:   "enable",
		Commands: fixedCommands("no page", "show running-config"),
	},
}

// nokia is Nokia's own enterprise number; most Nokia network equipment uses those of nokiaSROS and nokiaISAM instead
var nokia = &Vendor{
	Name:              "Nokia",
	EnterpriseNumbers: []string{"94"},
}

var ubiquiti = &Vendor{
	Name:              "Ubiquiti",
	EnterpriseNumbers: []string{"41112"},
	ConfigPlatforms:   []*ConfigPlatform{ubiquitiEdgeOS},
}

// ubiquitiEdgeOS covers EdgeOS and the VyOS it is based on, which log in to a shell with a prompt like
// "ubnt@edge-01:~$". Its SNMP agent often reports the generic net-snmp sysObjectID, so it is detected by sysDescr.
var ubiquitiEdgeOS = &ConfigPlatform{
	ID:          "UBIQUITI_EDGEOS",
	Name:        "Ubiquiti EdgeOS / VyOS",
	Description: regexp.MustCompile(`(?i)EdgeOS|EdgeRouter|VyOS|Vyatta`),
	Profile: cli.Profile{
		Prompt:   regexp.MustCompile(`^[\w.\-]+@[\w.\-]+(:[~\w/.\-]*)?[$#] ?$`),
		Commands: fixedCommands("terminal length 0", "show configuration commands | cat"),
	},
}
