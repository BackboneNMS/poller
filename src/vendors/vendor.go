// Package vendors holds what the poller knows about each manufacturer's devices beyond the standard MIBs. Every vendor
// is a Vendor in its own file, listed in registry. To support a new one, add a file defining it (only the name and
// enterprise numbers are required, everything else is an optional hook) and add it to registry.
package vendors

import (
	"fmt"
	"main/src/api"
	"main/src/optics"
	"strings"

	"github.com/gosnmp/gosnmp"
)

const oidEnterprises = "1.3.6.1.4.1"

// Vendor describes one family of devices, identified by the enterprise number in their sysObjectID. A manufacturer
// can have several families that need different handling (Nokia SR OS and Nokia ISAM, for example), each with the same
// Name. The hooks are only called for that family's devices, and only to fill in what the standard MIBs did not.
type Vendor struct {
	// Name is reported as the device's manufacturer
	Name string
	// EnterpriseNumbers are the IANA private enterprise numbers (the N in 1.3.6.1.4.1.N) of the family's sysObjectIDs
	EnterpriseNumbers []string
	// Model reads the model out of the sysDescr, for devices whose ENTITY-MIB has none
	Model func(description string) string
	// Hardware fills in hardware details ENTITY-MIB did not provide (serial number, versions), during discoveries
	Hardware func(client *gosnmp.GoSNMP, system *api.System) error
	// Resources fills in the CPU load and memory HOST-RESOURCES-MIB did not provide, on every poll. It is only called
	// when one of them is missing, and must leave values that are already set alone.
	Resources func(client *gosnmp.GoSNMP, resources *api.Resources) error
	// Optics reads transceiver light levels from the vendor's own tables, during discoveries. When it is not set or
	// finds nothing, SensorTables and then the standard ENTITY-SENSOR-MIB are tried.
	Optics func(client *gosnmp.GoSNMP, interfaces []api.Interface) (optics.Readings, error)
	// SensorTables are vendor variants of ENTITY-SENSOR-MIB to try for light levels before the standard one
	SensorTables []optics.SensorTable
}

// Unknown is returned for devices from enterprises without a vendor definition. It has no name or hooks, so only the
// standard MIBs are read.
var Unknown = &Vendor{}

var registry = []*Vendor{
	aviat,
	cambium,
	cisco,
	cyberPower,
	fiberStore,
	hewlettPackard,
	juniper,
	mikroTik,
	netonix,
	nokia,
	nokiaISAM,
	nokiaSROS,
	ubiquiti,
}

var byEnterpriseNumber = indexVendors(registry)

// indexVendors maps enterprise numbers to vendors, refusing to start with two vendors claiming the same number
func indexVendors(vendors []*Vendor) map[string]*Vendor {
	result := make(map[string]*Vendor)

	for _, vendor := range vendors {
		for _, number := range vendor.EnterpriseNumbers {
			if existing, ok := result[number]; ok {
				panic(fmt.Sprintf("vendors: enterprise number %s is claimed by both %s and %s", number, existing.Name, vendor.Name))
			}

			result[number] = vendor
		}
	}

	return result
}

// Identify returns the vendor of a device from its sysObjectID, or Unknown
func Identify(objectID string) *Vendor {
	if vendor, ok := byEnterpriseNumber[enterpriseNumber(objectID)]; ok {
		return vendor
	}

	return Unknown
}

// enterpriseNumber returns the private enterprise number in a sysObjectID, such as 2636 in 1.3.6.1.4.1.2636.1.1.1.2.31,
// or "" for an OID outside the enterprises subtree
func enterpriseNumber(objectID string) string {
	suffix, ok := strings.CutPrefix(strings.TrimPrefix(objectID, "."), oidEnterprises+".")

	if !ok {
		return ""
	}

	number, _, _ := strings.Cut(suffix, ".")

	return number
}
