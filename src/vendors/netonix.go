package vendors

import "strings"

var netonix = &Vendor{
	Name:              "Netonix",
	EnterpriseNumbers: []string{"46242"},
	Model:             netonixModel,
}

// netonixModel reads a sysDescr such as "Netonix WS-12-250-AC"
func netonixModel(description string) string {
	if fields := strings.Fields(description); len(fields) > 1 && fields[0] == "Netonix" {
		return fields[1]
	}

	return ""
}
