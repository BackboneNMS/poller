package vendors

import (
	"main/src/cli"
	"regexp"
)

// arista is only identified by name over SNMP, and its configuration is backed up like Cisco's
var arista = &Vendor{
	Name:              "Arista",
	EnterpriseNumbers: []string{"30065"},
	ConfigPlatforms:   []*ConfigPlatform{aristaEOS},
}

var aristaEOS = &ConfigPlatform{
	ID:          "ARISTA_EOS",
	Name:        "Arista EOS",
	Description: regexp.MustCompile(`Arista Networks EOS`),
	Clean:       dropLines(`^! Time:`),
	Profile: cli.Profile{
		Prompt:   ciscoStylePrompt,
		Enable:   "enable",
		Commands: fixedCommands("terminal length 0", "terminal width 32767", "show running-config"),
	},
}
