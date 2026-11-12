package snmp_test

import (
	"main/src/snmp"
	"main/src/snmp/snmptest"
	"strconv"
	"testing"

	"github.com/gosnmp/gosnmp"
)

const testEntryOID = "1.3.6.1.2.1.2.2.1"

// tableValues fills columns of a table at testEntryOID with rows 1 to count, plus an object after the table
func tableValues(count int, columns ...int) map[string]gosnmp.SnmpPDU {
	values := map[string]gosnmp.SnmpPDU{"1.3.6.1.2.1.31.1.1.1.1.1": snmptest.OctetString("after the table")}

	for _, column := range columns {
		for row := 1; row <= count; row++ {
			values[testEntryOID+"."+strconv.Itoa(column)+"."+strconv.Itoa(row)] = snmptest.Integer(column*1000 + row)
		}
	}

	return values
}

func expectRows(t *testing.T, rows map[string]snmp.Row, count int, columns ...int) {
	t.Helper()

	if len(rows) != count {
		t.Fatalf("expected %d rows, got %d", count, len(rows))
	}

	for row := 1; row <= count; row++ {
		for _, column := range columns {
			if value, ok := rows[strconv.Itoa(row)].Number(column); !ok || value != int64(column*1000+row) {
				t.Fatalf("row %d column %d: expected %d, got %d (present %v)", row, column, column*1000+row, value, ok)
			}
		}
	}
}

func TestWalkTableKeepsToLimits(t *testing.T) {
	agent := snmptest.StartAgent(t, tableValues(30, 2, 3, 4, 5, 6, 7, 8))
	client := agent.Client(t)

	snmp.ApplyLimits(client, snmp.Limits{MaxOIDs: 3, MaxRepetitions: 4})

	rows, err := snmp.WalkTable(client, testEntryOID, 2, 3, 4, 5, 6, 7, 8)

	if err != nil {
		t.Fatal(err)
	}

	expectRows(t, rows, 30, 2, 3, 4, 5, 6, 7, 8)

	requests := agent.Requests()

	for _, request := range requests {
		if request.Type != gosnmp.GetBulkRequest || len(request.OIDs) > 3 || request.MaxRepetitions > 4 {
			t.Fatalf("expected GetBulk requests for at most 3 objects and 4 rows, got %+v", request)
		}
	}

	if first := requests[0]; first.MaxRepetitions != 1 {
		t.Fatalf("expected the walk to start with one row, got %+v", first)
	}
}

// TestWalkTableOfMissingTable checks a table the agent does not have costs one small request, rather than one the agent
// fills with whatever comes after it
func TestWalkTableOfMissingTable(t *testing.T) {
	agent := snmptest.StartAgent(t, tableValues(30, 2))

	rows, err := snmp.WalkTable(agent.Client(t), "1.3.6.1.2.1.1.9.1", 2, 3, 4, 5, 6)

	if err != nil {
		t.Fatal(err)
	}

	requests := agent.Requests()

	if len(rows) > 0 || len(requests) != 1 || requests[0].MaxRepetitions != 1 {
		t.Fatalf("expected no rows from a single one-row request, got %d rows from %+v", len(rows), requests)
	}
}

func TestWalkTableShrinksTooBigRequests(t *testing.T) {
	agent := snmptest.StartAgent(t, tableValues(40, 2))
	client := agent.Client(t)

	agent.SetMaxVariables(5)

	rows, err := snmp.WalkTable(client, testEntryOID, 2)

	if err != nil {
		t.Fatal(err)
	}

	expectRows(t, rows, 40, 2)
}

func TestWalkTableWithoutBulk(t *testing.T) {
	agent := snmptest.StartAgent(t, tableValues(5, 2, 3))

	rows, err := snmp.WalkTableWithoutBulk(agent.Client(t), testEntryOID, 2, 3)

	if err != nil {
		t.Fatal(err)
	}

	expectRows(t, rows, 5, 2, 3)

	for _, request := range agent.Requests() {
		if request.Type != gosnmp.GetNextRequest {
			t.Fatalf("expected GetNext requests only, got %+v", request)
		}
	}
}

func TestGetRowSplitsRequests(t *testing.T) {
	agent := snmptest.StartAgent(t, tableValues(5, 2))
	client := agent.Client(t)

	snmp.ApplyLimits(client, snmp.Limits{MaxOIDs: 2, MaxRepetitions: 10})

	row, err := snmp.GetRow(client, testEntryOID+".2.1", testEntryOID+".2.2", testEntryOID+".9.9", testEntryOID+".2.4", testEntryOID+".2.5")

	if err != nil {
		t.Fatal(err)
	}

	if len(row) != 4 || row.Int(0) != 2001 || row.Int(4) != 2005 {
		t.Fatalf("expected the four objects the agent has by position, got %+v", row)
	}

	if _, ok := row[2]; ok {
		t.Fatal("expected the missing object to be left out")
	}

	if requests := agent.Requests(); len(requests) != 3 {
		t.Fatalf("expected 3 requests of at most 2 objects, got %+v", requests)
	}
}

// TestFillColumns checks a column few rows need is read with Get and one most rows need is walked
func TestFillColumns(t *testing.T) {
	agent := snmptest.StartAgent(t, tableValues(10, 2, 3, 4))
	client := agent.Client(t)

	rows, err := snmp.WalkTable(client, testEntryOID, 2)

	if err != nil {
		t.Fatal(err)
	}

	needs := map[int][]string{3: {"4", "7"}, 4: {"1", "2", "3", "4", "5", "6", "7", "8"}}
	before := len(agent.Requests())

	if err = snmp.FillColumns(client, testEntryOID, rows, needs); err != nil {
		t.Fatal(err)
	}

	if rows["4"].Int(3) != 3004 || rows["7"].Int(3) != 3007 {
		t.Fatalf("expected the values read with Get, got %+v %+v", rows["4"], rows["7"])
	}

	if _, ok := rows["5"][3]; ok {
		t.Fatal("expected only the rows needing a column read with Get to get it")
	}

	expectRows(t, rows, 10, 2, 4)

	gets, walks := 0, 0

	for _, request := range agent.Requests()[before:] {
		switch request.Type {
		case gosnmp.GetRequest:
			gets++
		case gosnmp.GetBulkRequest:
			walks++
		}
	}

	if gets != 1 || walks < 1 {
		t.Fatalf("expected one Get and a walk, got %d Gets and %d GetBulks", gets, walks)
	}
}
