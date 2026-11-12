package main

import (
	"context"
	"errors"
	"fmt"
	"main/src/api"
	"main/src/cli"
	"main/src/vendors"
	"strings"
	"time"
)

// maxConfigBytes is the largest configuration the polling server accepts (its api.MaxConfigBytes). Larger ones are
// reported as a failed backup rather than submitted, so the server records the attempt instead of rejecting it.
const maxConfigBytes = 8 << 20

// configBackups holds the devices whose configuration is due to be backed up. Backups log in over SSH or Telnet,
// which is slow and ties up a session on the device, so they run in the background on config_backup.concurrency
// workers. Requested backups go first, then devices never backed up, then the ones backed up longest ago.
var configBackups = newDeviceQueue(func(device api.Device) bool { return device.ConfigBackup != nil }, compareConfigBackupPriority)

func compareConfigBackupPriority(a, b api.Device) int {
	return compareRequestedThenOldest(a.ConfigBackup.RequestedAt, b.ConfigBackup.RequestedAt, a.ConfigBackup.LastAttemptAt, b.ConfigBackup.LastAttemptAt)
}

// RunConfigBackupWorkers backs up queued devices until ctx is done, submitting each result as soon as it is ready
func RunConfigBackupWorkers(ctx context.Context) {
	configBackups.runWorkers(ctx, "BackUpConfig", conf.ConfigBackup.Concurrency, backUpConfig)
}

func backUpConfig(ctx context.Context, device api.Device) {
	result := BackUpDeviceConfig(ctx, device)

	if ctx.Err() != nil {
		return
	}

	if err := client.SubmitConfigResults(ctx, []api.ConfigResult{result}); err != nil {
		logger.Error("Failed to submit config backup to polling server", "device", device.Name, "error", err)

		return
	}

	attributes := []any{"device", device.Name, "host", device.Host, "duration", (time.Duration(result.DurationMS) * time.Millisecond).String()}

	if result.Error != nil {
		logger.Warn("Failed to back up device config", append(attributes, "error", *result.Error)...)
	} else {
		logger.Info("Backed up device config", append(attributes, "platform", *result.Platform, "bytes", len(*result.Config))...)
	}
}

// BackUpDeviceConfig logs in to a device and reads its configuration, trying its credentials in order until one is
// accepted. Only rejected credentials move on to the next one: any other failure (the device is unreachable, the
// host key changed) would fail the same way with every credential.
func BackUpDeviceConfig(ctx context.Context, device api.Device) api.ConfigResult {
	backup := device.ConfigBackup
	startedAt := time.Now()

	result := api.ConfigResult{DeviceID: device.ID, StartedAt: startedAt}

	fail := func(err error) api.ConfigResult {
		result.DurationMS = time.Since(startedAt).Milliseconds()
		result.Error = PointerOf(TruncateString(err.Error(), maxErrorLength))

		return result
	}

	platform, err := configPlatform(backup)

	if err != nil {
		return fail(err)
	}

	result.Platform = &platform.ID

	if len(backup.Credentials) < 1 {
		return fail(errors.New("no login credentials to try"))
	}

	target := cli.Target{
		Protocol: backup.Protocol,
		Host:     device.Host,
		Port:     backup.Port,
		HostKey:  backup.HostKey,
		Timeout:  conf.ConfigBackup.Timeout,
	}

	var output cli.Result

	for _, credential := range backup.Credentials {
		output, err = cli.Fetch(ctx, target, cli.Credential{Username: credential.Username, Password: credential.Password, EnablePassword: credential.EnablePassword}, platform.Profile)

		if len(output.HostKey) > 0 {
			result.HostKey = &output.HostKey
		}

		if err == nil {
			result.CredentialID = credential.ID
		}

		if !errors.Is(err, cli.ErrAuthentication) {
			break
		}
	}

	if errors.Is(err, cli.ErrAuthentication) && len(backup.Credentials) > 1 {
		return fail(fmt.Errorf("the device rejected all %d login credentials", len(backup.Credentials)))
	}

	if err != nil {
		return fail(err)
	}

	config := output.Output

	if platform.Clean != nil {
		config = strings.Trim(platform.Clean(config), "\n")
	}

	if len(config) < 1 {
		return fail(errors.New("the device returned an empty configuration"))
	}

	if len(config) > maxConfigBytes {
		return fail(fmt.Errorf("the configuration is larger than %d MiB", maxConfigBytes>>20))
	}

	result.DurationMS = time.Since(startedAt).Milliseconds()
	result.Config = &config

	return result
}

// configPlatform returns the platform a device was set to, or detects it from what its last discovery found
func configPlatform(backup *api.ConfigBackup) (*vendors.ConfigPlatform, error) {
	if len(backup.Platform) > 0 {
		if platform := vendors.FindConfigPlatform(backup.Platform); platform != nil {
			return platform, nil
		}

		return nil, fmt.Errorf("this poller does not support the %s platform, it may need to be updated", backup.Platform)
	}

	if platform := vendors.DetectConfigPlatform(backup.ObjectID, backup.Description); platform != nil {
		return platform, nil
	}

	return nil, errors.New("the platform could not be detected from SNMP, choose it in the device's config backup settings")
}
