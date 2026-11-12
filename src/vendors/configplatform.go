package vendors

import (
	"fmt"
	"main/src/cli"
	"regexp"
	"strings"
)

// ciscoStylePrompt matches the prompts of Cisco and the many platforms that copied its command line: a hostname
// followed by ">" in user mode or "#" in privileged mode, such as "core-01#" or "RP/0/RSP0/CPU0:pe1#"
var ciscoStylePrompt = regexp.MustCompile(`^[\w.\-@/:()]+[>#] ?$`)

// ConfigPlatform is a command line a device's configuration can be backed up from. ID is what the dashboard stores
// when someone picks the platform by hand (dashboard ConfigPlatform enum, keep both in sync), Description, when set,
// must match a device's sysDescr for the platform to be detected, and Clean, when set, removes lines that change on
// every read (timestamps, byte counts), so only real changes produce new revisions.
type ConfigPlatform struct {
	ID          string
	Name        string
	Description *regexp.Regexp
	Clean       func(config string) string
	cli.Profile
}

var configPlatforms = indexConfigPlatforms(registry)

// indexConfigPlatforms maps the platform IDs of every vendor's platforms, refusing to start with two platforms
// claiming the same ID
func indexConfigPlatforms(vendors []*Vendor) map[string]*ConfigPlatform {
	result := make(map[string]*ConfigPlatform)

	for _, vendor := range vendors {
		for _, platform := range vendor.ConfigPlatforms {
			if _, ok := result[platform.ID]; ok {
				panic(fmt.Sprintf("vendors: config platform %s is defined twice", platform.ID))
			}

			result[platform.ID] = platform
		}
	}

	return result
}

// FindConfigPlatform returns the platform with an ID, or nil
func FindConfigPlatform(id string) *ConfigPlatform {
	return configPlatforms[id]
}

// DetectConfigPlatform picks the platform of a device from its sysObjectID and sysDescr: the first of its vendor's
// platforms whose Description matches (or that has none). Devices from vendors without a definition, or whose vendor's
// platforms do not match, can still be recognized by a platform's Description alone, which covers platforms whose
// SNMP agent reports a generic sysObjectID. Returns nil when no platform fits.
func DetectConfigPlatform(objectID, description string) *ConfigPlatform {
	for _, platform := range Identify(objectID).ConfigPlatforms {
		if platform.Description == nil || platform.Description.MatchString(description) {
			return platform
		}
	}

	for _, vendor := range registry {
		for _, platform := range vendor.ConfigPlatforms {
			if platform.Description != nil && platform.Description.MatchString(description) {
				return platform
			}
		}
	}

	return nil
}

// fixedCommands is the Commands of a platform that runs the same commands whatever its prompt
func fixedCommands(commands ...string) func(string) []string {
	return func(string) []string {
		return commands
	}
}

// dropLines builds a Clean that removes every line matching one of patterns
func dropLines(patterns ...string) func(string) string {
	expression := regexp.MustCompile(strings.Join(patterns, "|"))

	return func(config string) string {
		lines := strings.Split(config, "\n")
		kept := make([]string, 0, len(lines))

		for _, line := range lines {
			if !expression.MatchString(line) {
				kept = append(kept, line)
			}
		}

		return strings.Join(kept, "\n")
	}
}
