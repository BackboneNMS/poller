package vendors

import (
	"regexp"
	"testing"
)

func TestConfigPlatformRegistry(t *testing.T) {
	id := regexp.MustCompile(`^[A-Z0-9_]+$`)

	for _, platform := range configPlatforms {
		if !id.MatchString(platform.ID) || len(platform.Name) < 1 || platform.Prompt == nil || platform.Commands == nil {
			t.Errorf("platform %+v needs an upper case ID, a name, a prompt and commands", platform)
		}
	}

	defer func() {
		if recover() == nil {
			t.Fatal("expected two platforms with the same ID to be refused")
		}
	}()

	indexConfigPlatforms([]*Vendor{{ConfigPlatforms: []*ConfigPlatform{{ID: "A"}}}, {ConfigPlatforms: []*ConfigPlatform{{ID: "A"}}}})
}

func TestDetectConfigPlatform(t *testing.T) {
	for _, test := range []struct {
		objectID    string
		description string
		expected    string
	}{
		{"1.3.6.1.4.1.9.1.2134", "Cisco IOS Software, C2960X Software (C2960X-UNIVERSALK9-M), Version 15.2(7)E8", "CISCO_IOS"},
		{"1.3.6.1.4.1.9.1.2591", "Cisco IOS Software [Cupertino], Catalyst L3 Switch Software (CAT9K_IOSXE), Version 17.9.4a", "CISCO_IOS"},
		{"1.3.6.1.4.1.9.12.3.1.3.1812", "Cisco NX-OS(tm) n9000, Software (n9000-dk9), Version 9.3(8)", "CISCO_NXOS"},
		{"1.3.6.1.4.1.9.1.1017", "Cisco IOS XR Software (Cisco ASR9K Series), Version 6.5.3[Default]", "CISCO_IOSXR"},
		{"1.3.6.1.4.1.9.1.669", "Cisco Adaptive Security Appliance Version 9.8(4)", ""},
		{"1.3.6.1.4.1.2636.1.1.1.2.31", "Juniper Networks, Inc. ex3300-48p Ethernet Switch, kernel JUNOS 15.1R7.9", "JUNIPER_JUNOS"},
		{"1.3.6.1.4.1.14988.1", "RouterOS CCR2004-16G-2S+", "MIKROTIK_ROUTEROS"},
		{"1.3.6.1.4.1.6527.1.3.17", "TiMOS-B-20.10.R3 both/x86_64 Nokia 7750 SR Copyright (c) 2000-2020 Nokia.", "NOKIA_SROS"},
		{"1.3.6.1.4.1.637.61.1", "ISAM 7360 FX-8", "NOKIA_ISAM"},
		{"1.3.6.1.4.1.30065.1.3011.7048.427.3648", "Arista Networks EOS version 4.28.3M running on an Arista Networks DCS-7048T-A", "ARISTA_EOS"},
		{"1.3.6.1.4.1.11.2.3.7.11.181.11", "Aruba JL261A 2930F-24G-PoE+-4SFP Switch, revision WC.16.10.0001, ROM WC.16.01.0008", "HPE_PROCURVE"},
		{"1.3.6.1.4.1.8072.3.2.10", "EdgeOS v2.0.9-hotfix.7.5622731.230615.0857", "UBIQUITI_EDGEOS"},
		{"1.3.6.1.4.1.8072.3.2.10", "Linux server 6.1.0", ""},
		{"", "", ""},
	} {
		platform := DetectConfigPlatform(test.objectID, test.description)

		if detected := ""; platform != nil {
			detected = platform.ID

			if detected != test.expected {
				t.Errorf("%q: expected %q, got %q", test.description, test.expected, detected)
			}
		} else if len(test.expected) > 0 {
			t.Errorf("%q: expected %q, got none", test.description, test.expected)
		}
	}
}

func TestConfigPlatformPrompts(t *testing.T) {
	for _, test := range []struct {
		platform *ConfigPlatform
		prompts  []string
		others   []string
	}{
		{ciscoIOS, []string{"core-01>", "core-01#", "sw1(config)# "}, []string{"Username: ", " description uplink", "!"}},
		{ciscoIOSXR, []string{"RP/0/RSP0/CPU0:pe1#"}, []string{"Mon Nov 11 12:00:00.123 UTC"}},
		{juniperJunos, []string{"backup@ex3300> ", "root@ex3300:RE:0% ", "backup@mx# "}, []string{"{master:0}", "    description uplink;"}},
		{mikroTikRouterOS, []string{"[admin@MikroTik] > ", "[backup@core-rtr] /interface> "}, []string{"/interface bridge", "add name=bridge1"}},
		{nokiaSROSConfig, []string{"*A:pe1# ", "A:pe1#", "A:admin@pe1# ", "B:pe1>config>router# "}, []string{"[/]", "        exit"}},
		{nokiaISAMConfig, []string{"typ:isadmin>#", "leg:isadmin># "}, []string{"configure system id isam-01"}},
		{ubiquitiEdgeOS, []string{"ubnt@edge-01:~$ ", "vyos@vyos# "}, []string{"set interfaces ethernet eth0"}},
	} {
		for _, prompt := range test.prompts {
			if !test.platform.Prompt.MatchString(prompt) {
				t.Errorf("%s: expected %q to be a prompt", test.platform.ID, prompt)
			}
		}

		for _, line := range test.others {
			if test.platform.Prompt.MatchString(line) {
				t.Errorf("%s: expected %q not to be a prompt", test.platform.ID, line)
			}
		}
	}
}

func TestConfigPlatformClean(t *testing.T) {
	for _, test := range []struct {
		platform *ConfigPlatform
		config   string
		expected string
	}{
		{
			ciscoIOS,
			"Building configuration...\n\nCurrent configuration : 4321 bytes\n!\n! Last configuration change at 12:00:00 UTC Mon Nov 11 2026 by admin\n!\nversion 15.2\nntp clock-period 36028797\nend",
			"\n!\n!\nversion 15.2\nend",
		},
		{
			juniperJunos,
			"## Last commit: 2026-11-11 12:00:00 UTC by admin\nversion 15.1R7.9;\n{master:0}",
			"version 15.1R7.9;",
		},
		{
			mikroTikRouterOS,
			"# 2026-11-11 12:00:00 by RouterOS 7.16.1\n# software id = ABCD-1234\n/interface bridge",
			"# by RouterOS 7.16.1\n# software id = ABCD-1234\n/interface bridge",
		},
		{
			nokiaSROSConfig,
			"# TiMOS-B-20.10.R3\n# Generated THU NOV 11 12:00:00 2026 UTC\nconfigure\nexit all\n# Finished THU NOV 11 12:00:01 2026 UTC\n[/]",
			"# TiMOS-B-20.10.R3\nconfigure\nexit all",
		},
	} {
		if cleaned := test.platform.Clean(test.config); cleaned != test.expected {
			t.Errorf("%s: expected %q, got %q", test.platform.ID, test.expected, cleaned)
		}
	}
}

func TestJunosStartsTheCLIFromTheShell(t *testing.T) {
	if commands := juniperJunos.Commands("root@ex3300:RE:0% "); commands[0] != "cli" {
		t.Fatalf("expected root's shell to start the CLI first, got %v", commands)
	}

	if commands := juniperJunos.Commands("backup@ex3300> "); commands[0] == "cli" {
		t.Fatalf("expected the CLI not to be started twice, got %v", commands)
	}

	if commands := nokiaSROSConfig.Commands("A:admin@pe1# "); commands[len(commands)-1] != "admin show configuration" {
		t.Fatalf("expected the model-driven CLI's command, got %v", commands)
	}
}
