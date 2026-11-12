package snmp

import (
	"time"

	"github.com/gosnmp/gosnmp"
)

// Limits bound the requests sent to an agent. The agents of many switches compute each value on the spot (interface
// counters read from the switching chip, forwarding tables searched in full for every lookup) or build each response
// in a fixed buffer, and some freeze or reboot when a single request asks for too much.
type Limits struct {
	// MaxOIDs is the most objects named in one request: table columns walked side by side, or scalars read together
	MaxOIDs int
	// MaxRepetitions is the most rows one GetBulk asks for
	MaxRepetitions int
	// Interval is the shortest time between two requests, which gives a busy agent a break between them
	Interval time.Duration
}

// DefaultLimits apply to agents whose vendor sets none
var DefaultLimits = Limits{MaxOIDs: 10, MaxRepetitions: 25}

// ApplyLimits makes client keep to limits. Get, GetNext and GetBulk refuse requests for more than MaxOIDs objects, so
// GetRow, FillColumns and WalkTable split their requests to fit, and WalkTable asks for at most MaxRepetitions rows at
// a time. The pause between requests is taken just before each one is sent, after its timeout has started, so
// Interval must stay far below the timeout.
func ApplyLimits(client *gosnmp.GoSNMP, limits Limits) {
	client.MaxOids = max(1, limits.MaxOIDs)
	client.MaxRepetitions = uint32(max(1, limits.MaxRepetitions))
	client.PreSend = nil

	if limits.Interval > 0 {
		var lastSentAt time.Time

		client.PreSend = func(*gosnmp.GoSNMP) {
			time.Sleep(time.Until(lastSentAt.Add(limits.Interval)))
			lastSentAt = time.Now()
		}
	}
}

// maxOIDs is the most objects client allows in one request
func maxOIDs(client *gosnmp.GoSNMP) int {
	if client.MaxOids < 1 {
		return gosnmp.MaxOids
	}

	return client.MaxOids
}

// maxRepetitions is the most rows one GetBulk from client asks for
func maxRepetitions(client *gosnmp.GoSNMP) int {
	if client.MaxRepetitions < 1 {
		return DefaultLimits.MaxRepetitions
	}

	return int(client.MaxRepetitions)
}

// splitEvenly splits items into as few groups of at most size items as possible, of nearly equal sizes, so 11 columns
// with room for 10 per request are walked as 6 and 5 rather than 10 and 1
func splitEvenly[T any](items []T, size int) [][]T {
	if len(items) < 1 {
		return nil
	}

	count := (len(items) + size - 1) / size
	per := (len(items) + count - 1) / count
	groups := make([][]T, 0, count)

	for start := 0; start < len(items); start += per {
		groups = append(groups, items[start:min(start+per, len(items))])
	}

	return groups
}
