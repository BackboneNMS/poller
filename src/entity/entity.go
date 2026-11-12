// Package entity reads ENTITY-MIB, the physical makeup of a device (chassis, modules, ports, transceivers, sensors),
// and works out which interface each physical entity belongs to.
package entity

import (
	"main/src/api"
	"main/src/snmp"
	"strconv"
	"strings"

	"github.com/gosnmp/gosnmp"
)

const (
	oidPhysicalEntry        = "1.3.6.1.2.1.47.1.1.1.1"
	oidAliasMappingEntry    = "1.3.6.1.2.1.47.1.3.2.1"
	oidInterfaceIndexColumn = "1.3.6.1.2.1.2.2.1.1."

	// entPhysicalTable columns
	ColumnDescription     = 2
	ColumnContainedIn     = 4
	ColumnClass           = 5
	ColumnName            = 7
	ColumnHardwareVersion = 8
	ColumnFirmwareVersion = 9
	ColumnSoftwareVersion = 10
	ColumnSerialNumber    = 11
	ColumnManufacturer    = 12
	ColumnModel           = 13

	// entPhysicalClass values
	ClassChassis = 3
	ClassSensor  = 8
	ClassModule  = 9
	ClassPort    = 10

	maxDepth = 16
)

// Table holds a device's physical entities (entPhysicalTable rows keyed by entPhysicalIndex) and the interfaces they
// are mapped to in entAliasMappingTable
type Table struct {
	Rows       map[string]snmp.Row
	interfaces map[string]int
}

// Walk reads the physical entities and their interface mappings
func Walk(client *gosnmp.GoSNMP) (*Table, error) {
	rows, err := snmp.WalkTable(
		client,
		oidPhysicalEntry,
		ColumnDescription,
		ColumnContainedIn,
		ColumnClass,
		ColumnName,
		ColumnHardwareVersion,
		ColumnFirmwareVersion,
		ColumnSoftwareVersion,
		ColumnSerialNumber,
		ColumnManufacturer,
		ColumnModel,
	)

	if err != nil {
		return nil, err
	}

	aliasRows, err := snmp.WalkTable(client, oidAliasMappingEntry, 2)

	if err != nil {
		return nil, err
	}

	return NewTable(rows, aliasRows), nil
}

// NewTable builds a table from entPhysicalTable rows and entAliasMappingTable rows (column 2,
// entAliasMappingIdentifier, which points at the ifIndex column of the interface table)
func NewTable(rows, aliasRows map[string]snmp.Row) *Table {
	interfaces := make(map[string]int)

	for index, row := range aliasRows {
		entityIndex, _, _ := strings.Cut(index, ".")
		rawInterface, ok := strings.CutPrefix(row.String(2), oidInterfaceIndexColumn)

		if !ok {
			continue
		}

		if interfaceIndex, err := strconv.Atoi(rawInterface); err == nil {
			interfaces[entityIndex] = interfaceIndex
		}
	}

	return &Table{Rows: rows, interfaces: interfaces}
}

// Interface returns the interface an entity belongs to: the one it, or the closest entity containing it, is mapped to
// in entAliasMappingTable, or whose name or description matches that entity's name
func (t *Table) Interface(entityIndex string, interfaces []api.Interface) (int, bool) {
	for depth := 0; depth < maxDepth && entityIndex != "0" && len(entityIndex) > 0; depth++ {
		if interfaceIndex, ok := t.interfaces[entityIndex]; ok {
			return interfaceIndex, true
		}

		if item := FindInterfaceByName(interfaces, t.Rows[entityIndex].String(ColumnName)); item != nil {
			return item.Index, true
		}

		entityIndex = strconv.FormatInt(t.Rows[entityIndex].Int(ColumnContainedIn), 10)
	}

	return 0, false
}

// Depth is how many entities contain an entity, used to tell a transceiver apart from the port holding it
func (t *Table) Depth(entityIndex string) int {
	depth := 0

	for ; depth < maxDepth; depth++ {
		parent := t.Rows[entityIndex].Int(ColumnContainedIn)

		if parent < 1 {
			break
		}

		entityIndex = strconv.FormatInt(parent, 10)
	}

	return depth
}

// FindInterfaceByName returns the interface whose name or description is name, ignoring case
func FindInterfaceByName(interfaces []api.Interface, name string) *api.Interface {
	if len(name) < 1 {
		return nil
	}

	for i := range interfaces {
		if strings.EqualFold(interfaces[i].Name, name) || strings.EqualFold(interfaces[i].Description, name) {
			return &interfaces[i]
		}
	}

	return nil
}
