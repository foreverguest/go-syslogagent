//go:build windows

package main

import (
	"golang.org/x/sys/windows/registry"
)

// ReadRegistryString reads valueName from SOFTWARE\Syslog Agent
func ReadRegistryString(valueName string) (string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\\Syslog Agent`, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()

	s, _, err := k.GetStringValue(valueName)
	if err != nil {
		return "", err
	}
	return s, nil
}

// WriteRegistryString writes valueName to SOFTWARE\Syslog Agent
func WriteRegistryString(valueName, value string) error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, `SOFTWARE\\Syslog Agent`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	return k.SetStringValue(valueName, value)
}

// ReadRegistryTime reads an RFC3339 timestamp from registry valueName
func ReadRegistryTime(valueName string) (string, error) {
	s, err := ReadRegistryString(valueName)
	if err != nil {
		return "", err
	}
	return s, nil
}

// WriteRegistryTime writes an RFC3339 timestamp to registry valueName
func WriteRegistryTime(valueName, value string) error {
	return WriteRegistryString(valueName, value)
}
